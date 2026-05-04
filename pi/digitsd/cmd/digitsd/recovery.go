package main

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
	"github.com/justinlindh/digits/pi/digitsd/internal/bootcount"
	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
)

//go:embed recovery_static
var recoveryStaticFS embed.FS

// recoveryState tracks whether a factory reset is currently running,
// preventing duplicate triggers from the web UI and voice menu.
type recoveryState struct {
	mu       sync.Mutex
	reseting bool
}

func (rs *recoveryState) startReset() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.reseting {
		return false
	}
	rs.reseting = true
	return true
}

func recoveryInitSetup() {
	slog.Info("recovery init: running as PID 1")

	// Set PATH and LD_LIBRARY_PATH for recovery partition layout.
	os.Setenv("PATH", "/bin:/sbin:/usr/bin:/usr/sbin")
	os.Setenv("LD_LIBRARY_PATH", "/lib")

	// Mount essential filesystems (may already be moved from initramfs).
	for _, m := range []struct{ src, dst, fstype string }{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"tmpfs", "/tmp", "tmpfs"},
		{"devtmpfs", "/dev", "devtmpfs"},
	} {
		os.MkdirAll(m.dst, 0755)
		syscall.Mount(m.src, m.dst, m.fstype, 0, "")
	}

	// Mount data partition for boot counter and recovery log.
	os.MkdirAll("/data", 0755)
	if err := syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, ""); err != nil {
		slog.Warn("recovery init: mount /data failed (non-fatal)", "error", err)
	}

	// Tee log to /data/digits/recovery.log for post-mortem.
	os.MkdirAll("/data/digits", 0755)
	if f, err := os.OpenFile("/data/digits/recovery.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
		w := io.MultiWriter(os.Stderr, f)
		slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})))
		log.SetOutput(w)
		slog.Info("recovery init: log file opened")
	}

	// Load WiFi kernel module.
	slog.Info("recovery init: loading brcmfmac")
	exec.Command("/sbin/modprobe", "brcmfmac").Run()
	time.Sleep(2 * time.Second)

	// Unblock WiFi radio.
	entries, _ := os.ReadDir("/sys/class/rfkill")
	for _, entry := range entries {
		typePath := "/sys/class/rfkill/" + entry.Name() + "/type"
		if data, err := os.ReadFile(typePath); err == nil && string(data) == "wlan\n" {
			os.WriteFile("/sys/class/rfkill/"+entry.Name()+"/soft", []byte("0"), 0644)
		}
	}

	// Wait for wlan0.
	slog.Info("recovery init: waiting for wlan0")
	for i := 0; i < 15; i++ {
		if _, err := os.Stat("/sys/class/net/wlan0"); err == nil {
			break
		}
		time.Sleep(time.Second)
	}

	// Start AP.
	slog.Info("recovery init: starting AP")
	exec.Command("/bin/ip", "link", "set", "wlan0", "up").Run()
	exec.Command("/bin/ip", "addr", "flush", "dev", "wlan0").Run()
	exec.Command("/bin/ip", "addr", "add", "192.168.4.1/24", "dev", "wlan0").Run()

	hostapdConf := "/tmp/hostapd.conf"
	os.WriteFile(hostapdConf, []byte("interface=wlan0\ndriver=nl80211\nssid=Digits-Recovery\nhw_mode=g\nchannel=6\nauth_algs=1\nwpa=0\ncountry_code=US\nieee80211d=1\n"), 0644)

	dnsmasqConf := "/tmp/dnsmasq.conf"
	os.WriteFile(dnsmasqConf, []byte("interface=wlan0\nbind-interfaces\nuser=root\npid-file=/tmp/dnsmasq.pid\ndhcp-range=192.168.4.10,192.168.4.50,255.255.255.0,5m\naddress=/#/192.168.4.1\nno-resolv\ndomain-needed\ndhcp-leasefile=/tmp/dnsmasq-recovery.leases\n"), 0644)

	if out, err := exec.Command("/bin/hostapd", "-B", hostapdConf).CombinedOutput(); err != nil {
		slog.Warn("recovery init: hostapd failed", "error", err, "output", string(out))
	}
	if out, err := exec.Command("/bin/dnsmasq", "-C", dnsmasqConf).CombinedOutput(); err != nil {
		slog.Warn("recovery init: dnsmasq failed", "error", err, "output", string(out))
	}

	slog.Info("recovery init: AP started (Digits-Recovery)")

	// Load audio kernel modules from the rootfs (mounted read-only temporarily).
	os.MkdirAll("/tmp/rootfs", 0755)
	if err := syscall.Mount("/dev/mmcblk0p2", "/tmp/rootfs", "ext4", syscall.MS_RDONLY, ""); err == nil {
		os.MkdirAll("/lib/modules", 0755)
		syscall.Mount("/tmp/rootfs/lib/modules", "/lib/modules", "", syscall.MS_BIND, "")
		for _, mod := range []string{"snd_soc_bcm2835_i2s", "snd_soc_tlv320aic3x_i2c", "snd_soc_simple_card"} {
			if out, err := exec.Command("/sbin/modprobe", mod).CombinedOutput(); err != nil {
				slog.Warn("recovery init: modprobe failed", "module", mod, "error", err, "output", string(out))
			} else {
				slog.Info("recovery init: loaded module", "module", mod)
			}
		}
		syscall.Unmount("/lib/modules", 0)
		syscall.Unmount("/tmp/rootfs", 0)
	} else {
		slog.Warn("recovery init: could not mount rootfs for audio modules", "error", err)
	}
	time.Sleep(time.Second)

	// Reap zombies (PID 1 responsibility).
	go func() {
		for {
			syscall.Wait4(-1, nil, 0, nil)
		}
	}()

	slog.Info("recovery init: setup complete")
}

func runRecoveryMode() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	slog.Info("digitsd: entering recovery mode")

	if os.Getpid() == 1 {
		recoveryInitSetup()
	}

	// Open serial port with retry.
	var sp *phone.SerialPort
	for attempt := 1; attempt <= 10; attempt++ {
		var err error
		sp, err = phone.OpenSerial(*serialDev, 115200, logger)
		if err != nil {
			slog.Warn("serial: open failed, retrying", "attempt", attempt, "error", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err = sp.Ping(); err != nil {
			slog.Warn("serial: ping failed, retrying", "attempt", attempt, "error", err)
			_ = sp.Close()
			sp = nil
			time.Sleep(500 * time.Millisecond)
			continue
		}
		break
	}
	if sp == nil {
		log.Fatal("serial: failed to open and ping after 10 attempts")
	}
	slog.Info("serial: connected")

	// Set LED state for recovery mode.
	sp.LED("LOCK")
	time.Sleep(50 * time.Millisecond)
	sp.LED("HEARTBEAT")

	// Initialize ALSA playback.
	pbCfg := audio.DefaultPlaybackConfig()
	if *alsaDevice != "" {
		pbCfg.Device = *alsaDevice
	}
	pb, err := audio.NewPlayback(pbCfg)
	if err != nil {
		// Fallback: try the raw hardware device directly.
		slog.Warn("playback: default device failed, trying hw:CARD=digitscodec,DEV=0", "error", err)
		pbCfg.Device = "hw:CARD=digitscodec,DEV=0"
		pb, err = audio.NewPlayback(pbCfg)
		if err != nil {
			log.Fatalf("playback: cannot open any device: %v", err)
		}
	}
	slog.Info("playback: opened", "device", pbCfg.Device)

	// Create mixer and load tones.
	mixer := audio.NewMixer(pb)
	if err := mixer.LoadTonesFromDir(*toneDir); err != nil {
		slog.Warn("mixer: failed to load tones (voice menu will be silent)", "dir", *toneDir, "error", err)
	}
	mixer.Start()
	defer mixer.Stop()

	state := &recoveryState{}

	// Start recovery web UI.
	go serveRecoveryWeb(state, mixer, sp)

	// Run voice menu event loop.
	recoveryEventLoop(sp, mixer, state)
}

// serveRecoveryWeb starts the recovery HTTP server on :80.
func serveRecoveryWeb(state *recoveryState, mixer *audio.Mixer, sp *phone.SerialPort) {
	staticFS, err := fs.Sub(recoveryStaticFS, "recovery_static")
	if err != nil {
		log.Fatalf("recovery web: embed sub: %v", err)
	}

	mux := http.NewServeMux()

	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		count, _ := bootcount.Read(bootcount.DefaultPath)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"boot_count": count}) //nolint:errcheck
	})

	mux.HandleFunc("/try-again", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		slog.Info("recovery web: try-again requested")
		_ = bootcount.Clear(bootcount.DefaultPath)
		w.WriteHeader(http.StatusOK)
		go doReboot()
	})

	mux.HandleFunc("/factory-reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !state.startReset() {
			http.Error(w, "factory reset already in progress", http.StatusConflict)
			return
		}
		slog.Info("recovery web: factory-reset requested")
		w.WriteHeader(http.StatusOK)
		go doRecoveryFactoryReset(mixer)
	})

	slog.Info("recovery web: listening on :80")
	if err := http.ListenAndServe(":80", mux); err != nil {
		slog.Error("recovery web: listen failed", "error", err)
	}
}

// recoveryEventLoop runs the voice menu on the phone handset.
func recoveryEventLoop(sp *phone.SerialPort, mixer *audio.Mixer, state *recoveryState) {
	events := sp.Events()
	for {
		// Wait for off-hook.
		ev := <-events
		if ev != "HOOK:OFF" {
			continue
		}
		slog.Info("recovery: handset off-hook, playing menu")
		recoveryHandsetSession(sp, mixer, state, events)
	}
}

// recoveryHandsetSession handles one off-hook session: plays menu, waits for input.
func recoveryHandsetSession(sp *phone.SerialPort, mixer *audio.Mixer, state *recoveryState, events <-chan string) {
	for {
		mixer.PlayOnce("recovery_menu")

		// Wait for the menu audio to finish, a key press, or on-hook.
		key, hungUp := waitForKeyOrHangup(events, 30*time.Second)
		if hungUp {
			mixer.StopAll()
			slog.Info("recovery: handset on-hook")
			return
		}

		switch key {
		case "1":
			slog.Info("recovery: user pressed 1, restarting")
			mixer.PlayOnce("restarting")
			waitForOnceComplete(mixer, 5*time.Second)
			_ = bootcount.Clear(bootcount.DefaultPath)
			doReboot()
			return

		case "2":
			slog.Info("recovery: user pressed 2, confirming factory reset")
			mixer.PlayOnce("confirm_factory_reset")
			// Wait for confirmation (KEY:2 again) or bail.
			confirm, hungUp := waitForKeyOrHangup(events, 15*time.Second)
			if hungUp {
				mixer.StopAll()
				return
			}
			if confirm != "2" {
				slog.Info("recovery: factory reset not confirmed, replaying menu")
				continue
			}
			if !state.startReset() {
				slog.Warn("recovery: factory reset already in progress")
				return
			}
			doRecoveryFactoryReset(mixer)
			return

		case "":
			// Timeout: replay menu.
			slog.Info("recovery: menu timeout, replaying")
			continue

		default:
			slog.Info("recovery: unknown key, replaying menu", "key", key)
			continue
		}
	}
}

// waitForKeyOrHangup waits for a KEY event, HOOK:ON, or timeout.
// Returns the key digit (e.g. "1") and whether the user hung up.
// On timeout, returns ("", false).
func waitForKeyOrHangup(events <-chan string, timeout time.Duration) (string, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev := <-events:
			if ev == "HOOK:ON" {
				return "", true
			}
			if len(ev) > 4 && ev[:4] == "KEY:" {
				return ev[4:], false
			}
			// Ignore other events (DIAL:, FSM:, etc.)
		case <-timer.C:
			return "", false
		}
	}
}

// waitForOnceComplete waits up to timeout for all one-shot tones to finish.
func waitForOnceComplete(mixer *audio.Mixer, timeout time.Duration) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			return
		case <-ticker.C:
			if !mixer.OncePlaying() {
				return
			}
		}
	}
}

// doRecoveryFactoryReset performs the full factory reset sequence.
func doRecoveryFactoryReset(mixer *audio.Mixer) {
	slog.Info("recovery: starting factory reset")
	mixer.PlayOnce("factory_reset_in_progress")
	waitForOnceComplete(mixer, 10*time.Second)

	// Restore root filesystem.
	mixer.PlayOnce("restoring_system")
	waitForOnceComplete(mixer, 10*time.Second)

	rootfsImg := "/rootfs.img.zst"
	if _, err := os.Stat(rootfsImg); err != nil {
		slog.Error("recovery: rootfs.img.zst not found", "path", rootfsImg, "error", err)
		mixer.PlayOnce("error_tone")
		return
	}

	slog.Info("recovery: decompressing rootfs to /dev/mmcblk0p2")
	zstdCmd := exec.Command("/bin/zstd", "-d", "-c", rootfsImg)
	ddCmd := exec.Command("/bin/dd", "of=/dev/mmcblk0p2", "bs=4M", "conv=fsync")
	ddCmd.Stdin, _ = zstdCmd.StdoutPipe()
	ddCmd.Stderr = os.Stderr
	zstdCmd.Stderr = os.Stderr
	if err := ddCmd.Start(); err != nil {
		slog.Error("recovery: dd start failed", "error", err)
		return
	}
	if err := zstdCmd.Run(); err != nil {
		slog.Error("recovery: zstd failed", "error", err)
		return
	}
	if err := ddCmd.Wait(); err != nil {
		slog.Error("recovery: dd failed", "error", err)
		return
	}
	slog.Info("recovery: rootfs restored")

	// Format /data partition.
	mixer.PlayOnce("formatting_data")
	waitForOnceComplete(mixer, 10*time.Second)

	slog.Info("recovery: unmounting /data")
	_ = syscall.Unmount("/data", 0)

	slog.Info("recovery: formatting /dev/mmcblk0p4")
	mkfs := exec.Command("/bin/mkfs.ext4", "-L", "data", "-F", "/dev/mmcblk0p4")
	mkfs.Stdout = os.Stdout
	mkfs.Stderr = os.Stderr
	if err := mkfs.Run(); err != nil {
		slog.Error("recovery: mkfs.ext4 failed", "error", err)
		mixer.PlayOnce("error_tone")
		return
	}

	// Remount /data and extract skeleton.
	slog.Info("recovery: mounting /data")
	if err := syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, ""); err != nil {
		slog.Error("recovery: mount /data failed", "error", err)
		mixer.PlayOnce("error_tone")
		return
	}

	skeletonPath := "/data-skeleton.tar.zst"
	if _, err := os.Stat(skeletonPath); err == nil {
		slog.Info("recovery: extracting data skeleton")
		zstdSkel := exec.Command("/bin/zstd", "-d", "-c", skeletonPath)
		tarCmd := exec.Command("/bin/tar", "xf", "-", "-C", "/data")
		tarCmd.Stdin, _ = zstdSkel.StdoutPipe()
		tarCmd.Stderr = os.Stderr
		zstdSkel.Stderr = os.Stderr
		tarCmd.Start()
		if err := zstdSkel.Run(); err != nil {
			slog.Error("recovery: skeleton zstd failed", "error", err)
		}
		if err := tarCmd.Wait(); err != nil {
			slog.Error("recovery: skeleton tar failed", "error", err)
		}
	} else {
		slog.Info("recovery: no data skeleton found, skipping", "path", skeletonPath)
	}

	slog.Info("recovery: factory reset complete")
	mixer.PlayOnce("reset_complete")
	waitForOnceComplete(mixer, 10*time.Second)

	doReboot()
}

// doReboot reboots the system. If running as PID 1 (init in initramfs),
// uses the raw reboot syscall. Otherwise uses systemctl.
func doReboot() {
	slog.Info("recovery: rebooting")
	time.Sleep(500 * time.Millisecond) // let final log flush

	if os.Getpid() == 1 {
		syscall.Sync()
		_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	} else {
		cmd := exec.Command("systemctl", "reboot")
		if err := cmd.Run(); err != nil {
			// Last resort: raw reboot.
			slog.Warn("recovery: systemctl reboot failed, trying raw syscall", "error", err)
			syscall.Sync()
			_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
		}
	}
	// Block forever if reboot doesn't kill us immediately.
	select {}
}

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

	// Load kernel modules explicitly via insmod in dependency order.
	// BusyBox modprobe fails on the recovery partition because modules.dep
	// references thousands of modules that don't exist here. insmod bypasses
	// that entirely.
	kver := ""
	if entries, err := os.ReadDir("/lib/modules"); err == nil && len(entries) > 0 {
		kver = entries[0].Name()
	}
	slog.Info("recovery init: kernel version", "kver", kver)

	loadMod := func(path string) {
		full := "/lib/modules/" + kver + "/" + path
		if out, err := exec.Command("/sbin/insmod", full).CombinedOutput(); err != nil {
			slog.Warn("recovery init: insmod failed", "path", path, "error", err, "output", string(out))
		} else {
			slog.Info("recovery init: insmod ok", "path", path)
		}
	}

	// WiFi chain
	loadMod("rfkill.ko")
	loadMod("cfg80211.ko")
	loadMod("brcmutil.ko")
	loadMod("brcmfmac.ko")
	loadMod("brcmfmac-wcc.ko")

	// Audio chain
	loadMod("kernel/sound/core/snd.ko")
	loadMod("kernel/sound/core/snd-timer.ko")
	loadMod("kernel/sound/core/snd-pcm.ko")
	loadMod("kernel/sound/core/snd-pcm-dmaengine.ko")
	loadMod("kernel/sound/core/snd-compress.ko")
	loadMod("kernel/drivers/base/regmap/regmap-i2c.ko")
	loadMod("kernel/sound/soc/snd-soc-core.ko")
	loadMod("kernel/sound/soc/bcm/snd-soc-bcm2835-i2s.ko")
	loadMod("kernel/sound/soc/codecs/snd-soc-tlv320aic3x.ko")
	loadMod("kernel/sound/soc/codecs/snd-soc-tlv320aic3x-i2c.ko")
	loadMod("kernel/sound/soc/generic/snd-soc-simple-card-utils.ko")
	loadMod("kernel/sound/soc/generic/snd-soc-simple-card.ko")

	slog.Info("recovery init: modules loaded, waiting for devices")
	time.Sleep(3 * time.Second)

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

	// Audio in recovery is not yet working. The kernel modules load
	// successfully but the device tree binding doesn't trigger from
	// insmod alone, and writing to the sysfs bind file crashes PID 1.
	// The voice menu runs silently; keypad controls still work.
	if data, err := os.ReadFile("/proc/asound/cards"); err == nil {
		slog.Info("recovery init: ALSA cards", "cards", string(data))
	}

	// Reap zombies (PID 1 responsibility).
	go func() {
		for {
			syscall.Wait4(-1, nil, 0, nil)
		}
	}()

	slog.Info("recovery init: setup complete")
	syscall.Sync()
}

func runRecoveryMode() {
	slog.Info("digitsd: entering recovery mode")

	if os.Getpid() == 1 {
		recoveryInitSetup()
	}
	slog.Info("recovery: init done, starting serial/audio")
	syscall.Sync()

	// Open serial port with retry.
	serialLogger := slog.Default()
	var sp *phone.SerialPort
	for attempt := 1; attempt <= 10; attempt++ {
		var err error
		sp, err = phone.OpenSerial(*serialDev, 115200, serialLogger)
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
	syscall.Sync()

	// Set LED state for recovery mode.
	sp.LED("LOCK")
	time.Sleep(50 * time.Millisecond)
	sp.LED("HEARTBEAT")

	// Initialize ALSA playback. In recovery mode, /etc/asound.conf is
	// unavailable (rootfs unmounted), so try the raw hardware device first.
	// Log what ALSA sees for diagnostics.
	if data, err := os.ReadFile("/proc/asound/cards"); err == nil {
		slog.Info("ALSA cards", "cards", string(data))
	} else {
		slog.Warn("ALSA: cannot read /proc/asound/cards", "error", err)
	}
	var mixer *audio.Mixer
	for _, dev := range []string{"hw:CARD=digitscodec,DEV=0", "hw:1,0", "hw:0,0", "plughw:CARD=Zero,DEV=0", "default"} {
		slog.Info("playback: trying device", "device", dev)
		pbCfg := audio.Config{
			Device:     dev,
			SampleRate: 48000,
			Channels:   1,
			FrameSize:  960,
		}
		pb, err := audio.NewPlayback(pbCfg)
		if err != nil {
			slog.Warn("playback: device failed", "device", dev, "error", err)
			continue
		}
		slog.Info("playback: opened", "device", dev)
		mixer = audio.NewMixer(pb)
		if err := mixer.LoadTonesFromDir(*toneDir); err != nil {
			slog.Warn("mixer: failed to load tones (voice menu will be silent)", "dir", *toneDir, "error", err)
		}
		mixer.Start()
		defer mixer.Stop()
		break
	}
	if mixer == nil {
		slog.Warn("playback: no audio device available, voice menu will be silent")
	}
	syscall.Sync()

	// Wrap mixer in a nil-safe helper for recovery code.
	play := func(name string) {
		if mixer != nil {
			mixer.PlayOnce(name)
		}
	}
	stopAll := func() {
		if mixer != nil {
			mixer.StopAll()
		}
	}
	oncePlaying := func() bool {
		if mixer != nil {
			return mixer.OncePlaying()
		}
		return false
	}

	state := &recoveryState{}

	// Start recovery web UI.
	go serveRecoveryWeb(state, play, sp)

	// Run voice menu event loop.
	recoveryEventLoop(sp, play, stopAll, oncePlaying, state)
}

type playFunc func(string)

// serveRecoveryWeb starts the recovery HTTP server on :80.
func serveRecoveryWeb(state *recoveryState, play playFunc, sp *phone.SerialPort) {
	staticFS, err := fs.Sub(recoveryStaticFS, "recovery_static")
	if err != nil {
		log.Fatalf("recovery web: embed sub: %v", err)
	}

	mux := http.NewServeMux()

	// Captive portal detection redirects.
	captiveRedirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.Handle("/generate_204", captiveRedirect)
	mux.Handle("/hotspot-detect.html", captiveRedirect)
	mux.Handle("/connecttest.txt", captiveRedirect)
	mux.Handle("/library/test/success.html", captiveRedirect)

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
		os.Remove("/data/digits/recovery-mode")
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
		go doRecoveryFactoryReset(play)
	})

	slog.Info("recovery web: listening on :80")
	syscall.Sync()
	if err := http.ListenAndServe(":80", mux); err != nil {
		slog.Error("recovery web: listen failed", "error", err)
	}
}

// recoveryEventLoop runs the voice menu on the phone handset.
func recoveryEventLoop(sp *phone.SerialPort, play playFunc, stopAll func(), oncePlaying func() bool, state *recoveryState) {
	events := sp.Events()
	for {
		ev := <-events
		if ev != "HOOK:OFF" {
			continue
		}
		slog.Info("recovery: handset off-hook, playing menu")
		recoveryHandsetSession(play, stopAll, oncePlaying, state, events)
	}
}

func recoveryHandsetSession(play playFunc, stopAll func(), oncePlaying func() bool, state *recoveryState, events <-chan string) {
	for {
		play("recovery_menu")

		key, hungUp := waitForKeyOrHangup(events, 30*time.Second)
		if hungUp {
			stopAll()
			slog.Info("recovery: handset on-hook")
			return
		}

		switch key {
		case "1":
			slog.Info("recovery: user pressed 1, restarting")
			play("restarting")
			waitForOnceComplete(oncePlaying, 5*time.Second)
			_ = bootcount.Clear(bootcount.DefaultPath)
			os.Remove("/data/digits/recovery-mode")
			doReboot()
			return

		case "2":
			slog.Info("recovery: user pressed 2, confirming factory reset")
			play("confirm_factory_reset")
			confirm, hungUp := waitForKeyOrHangup(events, 15*time.Second)
			if hungUp {
				stopAll()
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
			doRecoveryFactoryReset(play)
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

func waitForOnceComplete(oncePlaying func() bool, timeout time.Duration) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			return
		case <-ticker.C:
			if !oncePlaying() {
				return
			}
		}
	}
}

// doRecoveryFactoryReset performs the full factory reset sequence.
func doRecoveryFactoryReset(play playFunc) {
	slog.Info("recovery: starting factory reset")
	play("factory_reset_in_progress")
	time.Sleep(5 * time.Second)

	play("restoring_system")
	time.Sleep(3 * time.Second)

	rootfsImg := "/rootfs.img.zst"
	if _, err := os.Stat(rootfsImg); err != nil {
		slog.Error("recovery: rootfs.img.zst not found", "path", rootfsImg, "error", err)
		play("error_tone")
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
	play("formatting_data")
	time.Sleep(3 * time.Second)

	slog.Info("recovery: unmounting /data")
	_ = syscall.Unmount("/data", 0)

	slog.Info("recovery: formatting /dev/mmcblk0p4")
	mkfs := exec.Command("/bin/mkfs.ext4", "-L", "data", "-F", "/dev/mmcblk0p4")
	mkfs.Stdout = os.Stdout
	mkfs.Stderr = os.Stderr
	if err := mkfs.Run(); err != nil {
		slog.Error("recovery: mkfs.ext4 failed", "error", err)
		play("error_tone")
		return
	}

	// Remount /data and extract skeleton.
	slog.Info("recovery: mounting /data")
	if err := syscall.Mount("/dev/mmcblk0p4", "/data", "ext4", 0, ""); err != nil {
		slog.Error("recovery: mount /data failed", "error", err)
		play("error_tone")
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
	play("reset_complete")
	time.Sleep(3 * time.Second)

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

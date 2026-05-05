package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

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

// enableGPCLK0 configures BCM2835 GPCLK0 on GPIO4 to output ~12.288 MHz.
// The TLV320AIC3104 codec requires this as its MCLK source.
// Source: 19.2 MHz oscillator, divider 1.5625 (DIVI=1, DIVF=2304).
func enableGPCLK0() {
	const (
		clkBase  = 0x3F101000
		gpioBase = 0x3F200000
		ctlOff   = 0x70 // CM_GP0CTL
		divOff   = 0x74 // CM_GP0DIV
		passwd   = 0x5A000000
		mash1    = 1 << 9
		srcOSC   = 1
		enab     = 1 << 4
		busy     = 1 << 7
		divi     = 1
		divf     = 2304
	)

	f, err := os.OpenFile("/dev/mem", os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		slog.Warn("gpclk0: cannot open /dev/mem", "error", err)
		return
	}
	defer f.Close()

	clkMem, err := syscall.Mmap(int(f.Fd()), clkBase, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		slog.Warn("gpclk0: mmap clk failed", "error", err)
		return
	}
	defer syscall.Munmap(clkMem)

	gpioMem, err := syscall.Mmap(int(f.Fd()), gpioBase, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		slog.Warn("gpclk0: mmap gpio failed", "error", err)
		return
	}
	defer syscall.Munmap(gpioMem)

	w32 := func(mem []byte, off int, val uint32) {
		atomic.StoreUint32((*uint32)(unsafe.Pointer(&mem[off])), val)
	}
	r32 := func(mem []byte, off int) uint32 {
		return atomic.LoadUint32((*uint32)(unsafe.Pointer(&mem[off])))
	}

	// Disable, poll until BUSY clears, write divider, enable.
	w32(clkMem, ctlOff, passwd|0)
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 200; i++ {
		if r32(clkMem, ctlOff)&busy == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	w32(clkMem, divOff, passwd|uint32(divi<<12)|uint32(divf))
	w32(clkMem, ctlOff, passwd|mash1|srcOSC|enab)
	time.Sleep(10 * time.Millisecond)

	// Set GPIO4 to ALT0 (GPCLK0).
	fsel := r32(gpioMem, 0) // GPFSEL0
	shift := uint(12)       // GPIO4 is bits 14:12
	fsel &^= 0x7 << shift   // clear
	fsel |= 0x4 << shift    // ALT0 = 0b100
	w32(gpioMem, 0, fsel)

	slog.Info("gpclk0: enabled 12.288 MHz on GPIO4")
}

func recoveryInitSetup() {
	slog.Info("recovery init: running as PID 1")

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

	// Enable GPCLK0 on GPIO4 for the codec's MCLK (12.288 MHz).
	enableGPCLK0()

	// Load kernel modules via insmod in dependency order.
	// BusyBox modprobe cannot resolve deps on the recovery partition
	// (modules.dep references thousands of modules not present here).
	kver := ""
	if entries, err := os.ReadDir("/lib/modules"); err == nil && len(entries) > 0 {
		kver = entries[0].Name()
	}
	slog.Info("recovery init: loading modules", "kver", kver)

	loadMod := func(path string) {
		full := "/lib/modules/" + kver + "/" + path
		if out, err := exec.Command("/sbin/insmod", full).CombinedOutput(); err != nil {
			slog.Warn("recovery init: insmod failed", "path", path, "error", err, "output", string(out))
		}
	}

	// WiFi chain
	loadMod("rfkill.ko")
	loadMod("cfg80211.ko")
	loadMod("brcmutil.ko")
	loadMod("brcmfmac.ko")
	loadMod("brcmfmac-wcc.ko")

	// I2C bus (needed for codec probe)
	loadMod("kernel/drivers/i2c/busses/i2c-bcm2835.ko")

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

	slog.Info("recovery init: modules loaded, triggering deferred probe")

	// Trigger uevent re-emission to simulate udev coldplug. Without this,
	// the kernel's deferred probe never re-evaluates device tree bindings
	// after late module load (no udev in recovery).
	for _, uevent := range []string{
		"/sys/bus/i2c/devices/1-0018/uevent",
		"/sys/bus/platform/devices/digits-sound/uevent",
	} {
		if err := os.WriteFile(uevent, []byte("add"), 0644); err != nil {
			slog.Warn("recovery init: uevent trigger failed", "path", uevent, "error", err)
		}
	}

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

	// Configure static IP and start AP.
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

	// Trigger device tree rebind for the audio card. Run from a subprocess
	// to avoid crashing PID 1 if the driver probe fails.
	bindCmd := exec.Command("/bin/sh", "-c", "echo digits-sound > /sys/bus/platform/drivers/asoc-simple-card/bind 2>/dev/null; exit 0")
	if out, err := bindCmd.CombinedOutput(); err != nil {
		slog.Warn("recovery init: sound bind subprocess failed", "error", err, "output", string(out))
	}
	time.Sleep(2 * time.Second)

	os.Setenv("ALSA_CONFIG_PATH", "/usr/share/alsa/alsa.conf")
	os.Setenv("ALSA_PLUGIN_DIR", "/lib")

	// Reap zombies (PID 1 responsibility).
	go func() {
		for {
			syscall.Wait4(-1, nil, 0, nil)
		}
	}()

	slog.Info("recovery init: setup complete")
}

func runRecoveryMode() {
	slog.Info("digitsd: entering recovery mode")

	if os.Getpid() == 1 {
		recoveryInitSetup()
	}

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

	sp.LED("LOCK")
	time.Sleep(50 * time.Millisecond)
	sp.LED("HEARTBEAT")

	// Restore codec mixer state before any playback.
	for _, stateFile := range []string{"/mixer.state", "/data/digits_mixer.state"} {
		if _, err := os.Stat(stateFile); err != nil {
			continue
		}
		if out, err := exec.Command("/bin/alsactl", "restore", "-f", stateFile, "0").CombinedOutput(); err != nil {
			slog.Warn("playback: alsactl restore failed", "file", stateFile, "error", err, "output", string(out))
		} else {
			slog.Info("playback: mixer state restored", "file", stateFile)
			break
		}
	}
	exec.Command("/bin/amixer", "-c", "digitscodec", "sset", "PCM", "127").Run()

	// Re-enable GPCLK0 after the sound card is registered so the codec
	// sees MCLK stable on its output stage.
	enableGPCLK0()

	play := func(name string) {
		wavPath := *toneDir + "/" + name + ".wav"
		if _, err := os.Stat(wavPath); err != nil {
			return
		}
		exec.Command("/bin/aplay", "-D", "plughw:0,0", wavPath).Run()
	}

	state := &recoveryState{}

	go serveRecoveryWeb(state, play, sp)

	recoveryEventLoop(sp, play, state)
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

	mux.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "=== RECOVERY LOG ===")
		if data, err := os.ReadFile("/data/digits/recovery.log"); err == nil {
			w.Write(data)
		}
		fmt.Fprintln(w, "\n=== /proc/asound/cards ===")
		if data, err := os.ReadFile("/proc/asound/cards"); err == nil {
			w.Write(data)
		}
		fmt.Fprintln(w, "\n=== /dev/snd/ ===")
		if entries, err := os.ReadDir("/dev/snd"); err == nil {
			for _, e := range entries {
				fmt.Fprintln(w, e.Name())
			}
		}
		fmt.Fprintln(w, "\n=== amixer contents ===")
		if out, err := exec.Command("/bin/amixer", "-c", "0", "contents").CombinedOutput(); err == nil {
			w.Write(out)
		} else {
			fmt.Fprintf(w, "amixer failed: %v\n", err)
		}
		fmt.Fprintln(w, "\n=== GPCLK0 ===")
		if data, err := os.ReadFile("/sys/kernel/debug/gpio"); err == nil {
			w.Write(data)
		}
	})

	slog.Info("recovery web: listening on :80")
	if err := http.ListenAndServe(":80", mux); err != nil {
		slog.Error("recovery web: listen failed", "error", err)
	}
}

// recoveryEventLoop runs the voice menu on the phone handset.
func recoveryEventLoop(sp *phone.SerialPort, play playFunc, state *recoveryState) {
	events := sp.Events()
	for {
		ev := <-events
		if ev != "HOOK:OFF" {
			continue
		}
		slog.Info("recovery: handset off-hook, playing menu")
		recoveryHandsetSession(play, state, events)
	}
}

func recoveryHandsetSession(play playFunc, state *recoveryState, events <-chan string) {
	for {
		play("recovery_menu")
		// Wait up to 10 seconds for a key press before replaying the menu.
		key, hungUp := waitForKeyOrHangup(events, 10*time.Second)
		if hungUp {
			slog.Info("recovery: handset on-hook")
			return
		}

		switch key {
		case "1":
			slog.Info("recovery: user pressed 1, restarting")
			play("restarting")
			_ = bootcount.Clear(bootcount.DefaultPath)
			os.Remove("/data/digits/recovery-mode")
			doReboot()
			return

		case "2":
			slog.Info("recovery: user pressed 2, confirming factory reset")
			play("confirm_factory_reset")
			confirm, hungUp := waitForKeyOrHangup(events, 15*time.Second)
			if hungUp {
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
			continue

		default:
			continue
		}
	}
}

// waitForKeyOrHangup waits for a KEY event, HOOK:ON, or timeout.
// Returns the key digit and whether the user hung up.
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
		case <-timer.C:
			return "", false
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
	}

	slog.Info("recovery: factory reset complete")
	play("reset_complete")
	time.Sleep(3 * time.Second)

	doReboot()
}

// doReboot reboots the system. If running as PID 1, uses the raw reboot
// syscall. Otherwise delegates to systemctl.
func doReboot() {
	slog.Info("recovery: rebooting")
	time.Sleep(500 * time.Millisecond)

	if os.Getpid() == 1 {
		syscall.Sync()
		_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	} else {
		cmd := exec.Command("systemctl", "reboot")
		if err := cmd.Run(); err != nil {
			slog.Warn("recovery: systemctl reboot failed, trying raw syscall", "error", err)
			syscall.Sync()
			_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
		}
	}
	select {}
}

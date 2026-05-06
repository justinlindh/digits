package main

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// recoveryInit sets up the minimal environment when digitsd is PID 1 on the
// recovery partition: mount virtual filesystems, load kernel modules, enable
// GPCLK0 for the codec, start the WiFi AP, and restore ALSA mixer state.
func recoveryInit() {
	slog.Info("recovery init: running as PID 1")

	// Mount tmpfs on /tmp for hostapd/dnsmasq config files and live web log.
	os.MkdirAll("/tmp", 0755)
	syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "size=64M")

	// Log to /tmp for the live web UI tail.
	if f, err := os.OpenFile("/tmp/recovery.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
		recoveryLogFile = f
		logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{Level: slog.LevelInfo}))
		slog.SetDefault(logger)
	}
	slog.Info("recovery init: started")

	os.Setenv("LD_LIBRARY_PATH", "/lib")

	// Enable GPCLK0 for codec MCLK before loading audio modules.
	if err := enableGPCLK0(); err != nil {
		slog.Warn("recovery init: GPCLK0 failed", "error", err)
	}

	// Load kernel modules. The recovery partition has decompressed .ko files.
	kver := kernelVersion()
	modDir := filepath.Join("/lib/modules", kver)

	// WiFi module chain
	for _, mod := range []string{"rfkill.ko", "cfg80211.ko", "brcmutil.ko", "brcmfmac.ko", "brcmfmac-wcc.ko"} {
		insmod(filepath.Join(modDir, mod))
	}

	// I2C + audio module chain
	i2cAudioMods := []string{
		"kernel/drivers/i2c/busses/i2c-bcm2835.ko",
		"kernel/sound/core/snd.ko",
		"kernel/sound/core/snd-timer.ko",
		"kernel/sound/core/snd-pcm.ko",
		"kernel/sound/core/snd-pcm-dmaengine.ko",
		"kernel/sound/core/snd-compress.ko",
		"kernel/drivers/base/regmap/regmap-i2c.ko",
		"kernel/sound/soc/snd-soc-core.ko",
		"kernel/sound/soc/bcm/snd-soc-bcm2835-i2s.ko",
		"kernel/sound/soc/codecs/snd-soc-tlv320aic3x.ko",
		"kernel/sound/soc/codecs/snd-soc-tlv320aic3x-i2c.ko",
		"kernel/sound/soc/generic/snd-soc-simple-card-utils.ko",
		"kernel/sound/soc/generic/snd-soc-simple-card.ko",
	}
	for _, mod := range i2cAudioMods {
		insmod(filepath.Join(modDir, mod))
	}

	// Trigger deferred device binding via uevents.
	slog.Info("recovery init: triggering deferred probe")
	for _, uevent := range []string{
		"/sys/bus/i2c/devices/1-0018/uevent",
		"/sys/bus/platform/devices/digits-sound/uevent",
	} {
		if err := os.WriteFile(uevent, []byte("add"), 0); err == nil {
			slog.Info("recovery init: uevent triggered", "path", uevent)
		}
	}

	// Wait for wlan0 to appear.
	slog.Info("recovery init: waiting for wlan0")
	waitForInterface("wlan0", 15*time.Second)

	// Unblock WiFi radio.
	unblockWifi()

	// Start WiFi AP.
	slog.Info("recovery init: starting AP")
	if err := startRecoveryAP(); err != nil {
		slog.Warn("recovery init: AP setup failed", "error", err)
	} else {
		slog.Info("recovery init: AP started (Digits-Recovery)")
	}

	// Wait for sound card to register.
	waitForSoundCard("digitscodec", 3*time.Second)

	// Restore ALSA mixer state from the recovery partition root.
	mixerState := "/mixer.state"
	if _, err := os.Stat(mixerState); err == nil {
		cmd := exec.Command("alsactl", "restore", "-f", mixerState)
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH=/lib")
		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Warn("recovery init: alsactl restore failed", "error", err, "output", string(out))
		} else {
			slog.Info("recovery init: mixer state restored", "file", mixerState)
		}
	}

	// Re-toggle GPCLK0 after DAPM power-up.
	if err := enableGPCLK0(); err != nil {
		slog.Warn("recovery init: GPCLK0 re-toggle failed", "error", err)
	}

	// Override flag defaults for the recovery partition environment.
	// When exec'd as /sbin/init, no flags are passed. Tones live on
	// the recovery partition itself so recovery is fully self-contained.
	*toneDir = "/tones"

	// PID 1 must reap zombie children.
	go reapChildren()

	slog.Info("recovery init: setup complete")
}

var recoveryLogFile *os.File

func kernelVersion() string {
	var uname syscall.Utsname
	syscall.Uname(&uname)
	var buf []byte
	for _, b := range uname.Release {
		if b == 0 {
			break
		}
		buf = append(buf, byte(b))
	}
	return string(buf)
}

func insmod(path string) {
	cmd := exec.Command("insmod", path)
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH=/lib")
	if out, err := cmd.CombinedOutput(); err != nil {
		outStr := strings.TrimSpace(string(out))
		if !strings.Contains(outStr, "File exists") {
			slog.Warn("recovery init: insmod failed", "path", filepath.Base(path), "error", err, "output", outStr)
		}
	} else {
		slog.Info("recovery init: insmod ok", "path", filepath.Base(path))
	}
}

func waitForInterface(name string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := net.InterfaceByName(name); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	slog.Warn("recovery init: interface not found", "name", name, "timeout", timeout)
}

func waitForSoundCard(name string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile("/proc/asound/cards"); err == nil {
			if strings.Contains(string(data), name) {
				slog.Info("recovery init: sound card ready", "name", name)
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	slog.Warn("recovery init: sound card not found", "name", name, "timeout", timeout)
}

func unblockWifi() {
	exec.Command("rfkill", "unblock", "wifi").Run()
}

func startRecoveryAP() error {
	// Configure IP on wlan0.
	run := func(args ...string) error {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH=/lib")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	if err := run("ip", "addr", "add", "192.168.4.1/24", "dev", "wlan0"); err != nil {
		return fmt.Errorf("ip addr: %w", err)
	}
	if err := run("ip", "link", "set", "wlan0", "up"); err != nil {
		return fmt.Errorf("ip link up: %w", err)
	}

	hostapdConf := "/tmp/recovery-hostapd.conf"
	os.WriteFile(hostapdConf, []byte(`interface=wlan0
driver=nl80211
ssid=Digits-Recovery
channel=6
hw_mode=g
wmm_enabled=0
auth_algs=1
wpa=0
`), 0644)

	dnsmasqConf := "/tmp/recovery-dnsmasq.conf"
	os.WriteFile(dnsmasqConf, []byte(`interface=wlan0
bind-interfaces
user=root
dhcp-range=192.168.4.10,192.168.4.50,24h
pid-file=/tmp/dnsmasq.pid
address=/#/192.168.4.1
log-queries
log-facility=/tmp/dnsmasq.log
dhcp-leasefile=/tmp/dnsmasq-recovery.leases
`), 0644)

	if err := run("/bin/hostapd", "-B", hostapdConf); err != nil {
		return fmt.Errorf("hostapd: %w", err)
	}

	// hostapd needs a moment to bring the interface into AP mode before
	// dnsmasq can bind to it.
	time.Sleep(500 * time.Millisecond)

	cmd := exec.Command("/bin/dnsmasq", "-C", dnsmasqConf)
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH=/lib")
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("recovery init: dnsmasq failed", "error", err, "output", string(out))
		return fmt.Errorf("dnsmasq: %w", err)
	}

	return nil
}

func reapChildren() {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, syscall.SIGCHLD)
	var ws syscall.WaitStatus
	for range sigchld {
		for {
			pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
		}
	}
}

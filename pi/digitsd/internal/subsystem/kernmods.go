package subsystem

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// KernModsModule loads the kernel modules needed for WiFi and audio on the
// recovery partition, where udev is not running.
type KernModsModule struct {
	status ModuleStatus
}

func NewKernModsModule() *KernModsModule {
	return &KernModsModule{status: ModuleStatus{State: StatePending}}
}

func (k *KernModsModule) Name() string { return "kernel-modules" }

func (k *KernModsModule) Init(ctx context.Context) error {
	k.status.State = StateInitializing

	kver := kernelVersion()
	modDir := filepath.Join("/lib/modules", kver)

	// The build (tools/build-image.sh) decompresses every recovery module to a
	// FLAT layout: /lib/modules/$KVER/<basename>.ko, with no kernel/ subtree.
	// Load by basename so these paths match what is actually on disk; loading
	// by the kernel-tree nested path would resolve to a nonexistent file and
	// every insmod would silently fail (the codec card would never register).
	//
	// Order matters: insmod does no dependency resolution, so each module's
	// symbol dependencies must already be loaded. The lists below are ordered
	// leaf-deps-first to satisfy that.

	// WiFi module chain.
	for _, mod := range []string{"rfkill.ko", "cfg80211.ko", "brcmutil.ko", "brcmfmac.ko", "brcmfmac-wcc.ko"} {
		insmod(filepath.Join(modDir, mod))
	}

	// I2C + audio module chain. regmap-i2c and the i2c-bcm2835 bus controller
	// load before the codec's I2C driver (snd-soc-tlv320aic3x-i2c) so the
	// codec's control bus can bind; without i2c-bcm2835 the TLV320AIC3104
	// never probes and the digitscodec card never appears.
	i2cAudioMods := []string{
		"snd.ko",
		"snd-timer.ko",
		"snd-pcm.ko",
		"snd-pcm-dmaengine.ko",
		"snd-compress.ko",
		"snd-soc-core.ko",
		"regmap-i2c.ko",
		"i2c-bcm2835.ko",
		"snd-soc-bcm2835-i2s.ko",
		"snd-soc-tlv320aic3x.ko",
		"snd-soc-tlv320aic3x-i2c.ko",
		"snd-soc-simple-card-utils.ko",
		"snd-soc-simple-card.ko",
	}
	for _, mod := range i2cAudioMods {
		insmod(filepath.Join(modDir, mod))
	}

	// Trigger deferred device binding via uevents.
	slog.Info("subsystem kernel-modules: triggering deferred probe")
	for _, uevent := range []string{
		"/sys/bus/i2c/devices/1-0018/uevent",
		"/sys/bus/platform/devices/digits-sound/uevent",
	} {
		if err := os.WriteFile(uevent, []byte("add"), 0); err == nil {
			slog.Info("subsystem kernel-modules: uevent triggered", "path", uevent)
		}
	}

	// Wait for sound card to register.
	waitForSoundCard("digitscodec", 3*time.Second)

	k.status.State = StateReady
	return nil
}

func (k *KernModsModule) Status() ModuleStatus               { return k.status }
func (k *KernModsModule) Shutdown(ctx context.Context) error { return nil }

func kernelVersion() string {
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err != nil {
		return "unknown"
	}
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		if !strings.Contains(outStr, "File exists") {
			slog.Warn("subsystem kernel-modules: insmod failed", "path", filepath.Base(path), "error", err, "output", outStr)
		}
	} else {
		slog.Info("subsystem kernel-modules: insmod ok", "path", filepath.Base(path))
	}
}

func waitForSoundCard(name string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile("/proc/asound/cards"); err == nil {
			if strings.Contains(string(data), name) {
				slog.Info("subsystem kernel-modules: sound card ready", "name", name)
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	slog.Warn("subsystem kernel-modules: sound card not found", "name", name, "timeout", timeout)
}


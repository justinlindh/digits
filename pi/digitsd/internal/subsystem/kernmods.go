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

func (k *KernModsModule) IsReady() bool                      { return k.status.State == StateReady }
func (k *KernModsModule) Status() ModuleStatus               { return k.status }
func (k *KernModsModule) Shutdown(ctx context.Context) error { return nil }

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


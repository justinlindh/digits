package subsystem

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
)

type AudioConfig struct {
	ToneDir        string
	ALSADevice     string
	MixerStateFile string
	GPCLK0Retrigger func() error
}

type AudioModule struct {
	cfg   AudioConfig
	pb    *audio.Playback
	mixer *audio.Mixer
	ready bool
}

func NewAudioModule(cfg AudioConfig) *AudioModule {
	return &AudioModule{cfg: cfg}
}

func (a *AudioModule) Name() string { return "audio" }

func (a *AudioModule) Init(ctx context.Context) error {
	if a.cfg.MixerStateFile != "" {
		cmd := exec.Command("alsactl", "restore", "-f", a.cfg.MixerStateFile)
		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Warn("subsystem audio: alsactl restore failed", "error", err, "output", string(out))
		} else {
			slog.Info("subsystem audio: mixer state restored", "file", a.cfg.MixerStateFile)
		}
	}

	pbCfg := audio.DefaultPlaybackConfig()
	if a.cfg.ALSADevice != "" {
		pbCfg.Device = a.cfg.ALSADevice
	}
	pb, err := audio.NewPlayback(pbCfg)
	if err != nil {
		slog.Warn("subsystem audio: default device failed, trying hw:CARD=digitscodec,DEV=0", "error", err)
		pbCfg.Device = "hw:CARD=digitscodec,DEV=0"
		pb, err = audio.NewPlayback(pbCfg)
		if err != nil {
			return fmt.Errorf("playback open: %w", err)
		}
	}
	a.pb = pb
	slog.Info("subsystem audio: playback opened", "device", pbCfg.Device)

	if a.cfg.GPCLK0Retrigger != nil {
		if err := a.cfg.GPCLK0Retrigger(); err != nil {
			slog.Warn("subsystem audio: GPCLK0 retrigger failed", "error", err)
		}
	}

	a.mixer = audio.NewMixer(pb)
	if a.cfg.ToneDir != "" {
		if err := a.mixer.LoadTonesFromDir(a.cfg.ToneDir); err != nil {
			slog.Warn("subsystem audio: tone loading failed (voice will be silent)", "dir", a.cfg.ToneDir, "error", err)
		}
	}
	a.mixer.Start()

	// Re-apply the mixer state now that playback is open and the render loop
	// has powered the codec output. The restore above runs before NewPlayback,
	// so the TLV320's DAPM-gated output controls (HP DAC, HP, HPCOM) come up at
	// register defaults when the output powers on, leaving the earpiece path
	// ~19 dB quiet. Re-applying with the output live makes them stick (same fix
	// as the normal-mode path in cmd/digitsd). No-op when no state file is
	// configured (setup mode), which is tracked separately.
	if a.cfg.MixerStateFile != "" {
		cmd := exec.Command("alsactl", "restore", audio.CodecCardName(), "-f", a.cfg.MixerStateFile)
		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Warn("subsystem audio: mixer re-apply after playback open failed", "error", err, "output", string(out))
		} else {
			slog.Info("subsystem audio: mixer re-applied after playback open")
		}
	}

	a.ready = true
	return nil
}

func (a *AudioModule) Mixer() *audio.Mixer { return a.mixer }
func (a *AudioModule) IsReady() bool       { return a.ready }

func (a *AudioModule) Shutdown(ctx context.Context) error {
	if a.mixer != nil {
		a.mixer.Stop()
	}
	if a.pb != nil {
		a.pb.Close()
	}
	return nil
}

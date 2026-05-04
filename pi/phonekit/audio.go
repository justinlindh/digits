package phonekit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type audioPlayer struct {
	codecMarkerPath string
	aplayPath       string
	probeFunc       func(card string) bool
}

func newAudioPlayer() *audioPlayer {
	return &audioPlayer{
		codecMarkerPath: "/data/digits/codec-device",
		aplayPath:       "aplay",
		probeFunc:       probeALSACard,
	}
}

// probeALSACard returns true if amixer can query the named ALSA card.
func probeALSACard(card string) bool {
	err := exec.Command("amixer", "-c", card, "info").Run()
	return err == nil
}

// detectCodec returns the ALSA device string and sample rate to use for
// playback. It checks the codec marker file written by digitsd first, then
// falls back to probing with amixer.
func (a *audioPlayer) detectCodec() (device string, rate int) {
	data, err := os.ReadFile(a.codecMarkerPath)
	if err == nil && len(data) > 0 {
		dev := strings.TrimSpace(string(data))
		if strings.Contains(dev, "Zero") {
			return dev, 48000
		}
		return dev, 44100
	}

	if a.probeFunc("digitscodec") {
		return "hw:CARD=digitscodec,DEV=0", 44100
	}

	if a.probeFunc("Zero") {
		return "plughw:CARD=Zero,DEV=0", 48000
	}

	return "default", 48000
}

// Play plays WAV audio data by spawning aplay and piping the bytes to its
// stdin. The context can be used to cancel playback.
func (a *audioPlayer) Play(ctx context.Context, wav []byte) error {
	device, _ := a.detectCodec()

	cmd := exec.CommandContext(ctx, a.aplayPath, "-D", device, "-t", "wav")
	cmd.Stdin = bytes.NewReader(wav)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("aplay -D %s: %w: %s", device, err, stderr.String())
	}
	return nil
}

// PlayFile reads the WAV file at path and plays it via Play.
func (a *audioPlayer) PlayFile(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read wav file: %w", err)
	}
	return a.Play(ctx, data)
}

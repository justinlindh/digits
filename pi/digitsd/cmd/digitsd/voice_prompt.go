package main

import (
	"log/slog"
	"strings"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
)

const pickupDelay = 1230 * time.Millisecond

// voicePromptConfig configures a voicePromptLoop session.
type voicePromptConfig struct {
	// Clip is the mixer tone name to play each loop iteration.
	Clip string
	// ReplayInterval is the pause between end of playback and the next play.
	ReplayInterval time.Duration
	// OnKey is called when the user presses a key. It receives the raw key
	// string (e.g. "1"). Return true to end the session.
	// If nil, key presses are ignored and playback continues.
	OnKey func(key string) bool
}

// voicePromptLoop waits for HOOK:OFF on events, then plays cfg.Clip in a loop
// until the handset is hung up.
func voicePromptLoop(events <-chan string, mixer *audio.Mixer, cfg voicePromptConfig) {
	for {
		ev := <-events
		if ev != "HOOK:OFF" {
			continue
		}
		slog.Info("voice prompt: handset off-hook")
		time.Sleep(pickupDelay)
		if voicePromptSession(events, mixer, cfg) {
			slog.Info("voice prompt: session ended by key handler")
		} else {
			slog.Info("voice prompt: session ended by hang-up")
		}
	}
}

// voicePromptSession handles one off-hook session. Returns true if the key
// handler requested exit, false if the user hung up.
func voicePromptSession(events <-chan string, mixer *audio.Mixer, cfg voicePromptConfig) bool {
	for {
		slog.Info("voice prompt: playing clip", "clip", cfg.Clip)
		mixer.PlayOnce(cfg.Clip)

		// Wait for clip to finish, checking for hang-up or key press.
		for mixer.OncePlaying() {
			key, hungUp := tryReadEvent(events)
			if hungUp {
				mixer.StopAll()
				return false
			}
			if key != "" {
				mixer.StopAll()
				if tone := dtmfToneName(key); tone != "" {
					mixer.PlayOnce(tone)
					waitForOnceComplete(mixer, 500*time.Millisecond)
				}
				if cfg.OnKey != nil && cfg.OnKey(key) {
					return true
				}
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		// Pause between replays. Exit on hang-up or key press during pause.
		pauseEnd := time.After(cfg.ReplayInterval)
	pauseLoop:
		for {
			select {
			case ev := <-events:
				if ev == "HOOK:ON" {
					return false
				}
				if strings.HasPrefix(ev, "KEY:") {
					key := ev[4:]
					mixer.StopAll()
					if tone := dtmfToneName(key); tone != "" {
						mixer.PlayOnce(tone)
						waitForOnceComplete(mixer, 500*time.Millisecond)
					}
					if cfg.OnKey != nil && cfg.OnKey(key) {
						return true
					}
					break pauseLoop
				}
			case <-pauseEnd:
				break pauseLoop
			}
		}
	}
}

// tryReadEvent does a non-blocking read of the events channel.
// Returns the first KEY or HOOK:ON found, or empty strings if nothing pending.
func tryReadEvent(events <-chan string) (key string, hungUp bool) {
	select {
	case ev := <-events:
		if ev == "HOOK:ON" {
			return "", true
		}
		if strings.HasPrefix(ev, "KEY:") {
			return ev[4:], false
		}
		return "", false
	default:
		return "", false
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
			if strings.HasPrefix(ev, "KEY:") {
				return ev[4:], false
			}
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

package main

import (
	"log/slog"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
)

// voicePromptConfig configures a voicePromptLoop session.
type voicePromptConfig struct {
	// Clip is the mixer tone name to play each loop iteration.
	Clip string
	// PickupDelay is an optional pause after HOOK:OFF before the first play.
	PickupDelay time.Duration
	// ReplayInterval is the pause between end of playback and the next play.
	ReplayInterval time.Duration
	// OnKey is called when the user presses a key. It receives the raw key
	// string (e.g. "1"). Return true to end the session (hang-up equivalent).
	// If nil, key presses are ignored and playback continues.
	OnKey func(key string) bool
}

// voicePromptLoop waits for HOOK:OFF on events, then plays cfg.Clip in a loop
// until the handset is hung up. It handles the DTMF feedback tone before
// calling cfg.OnKey.
func voicePromptLoop(events <-chan string, mixer *audio.Mixer, cfg voicePromptConfig) {
	for {
		ev := <-events
		if ev != "HOOK:OFF" {
			continue
		}
		slog.Info("voice prompt: handset off-hook")
		if cfg.PickupDelay > 0 {
			time.Sleep(cfg.PickupDelay)
		}
		if voicePromptSession(events, mixer, cfg) {
			// Session ended by key handler requesting exit.
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

		key, hungUp := waitForKeyOrHangup(events, 30*time.Second)
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
			continue
		}

		// Timeout: pause between replays, exit on hang-up during pause.
		if cfg.ReplayInterval > 0 {
			pauseEnd := time.After(cfg.ReplayInterval)
		pauseLoop:
			for {
				select {
				case ev := <-events:
					if ev == "HOOK:ON" {
						mixer.StopAll()
						return false
					}
				case <-pauseEnd:
					break pauseLoop
				}
			}
		}
	}
}

// waitForKeyOrHangup waits for a KEY event, HOOK:ON, or timeout.
// Returns the key digit (e.g. "1") and whether the user hung up.
// On timeout, returns ("", false).
func waitForKeyOrHangup(events <-chan string, timeout time.Duration) (string, bool) {
	slog.Info("voice prompt: waiting for key/hangup", "timeout", timeout)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev := <-events:
			if ev == "HOOK:ON" {
				return "", true
			}
			if len(ev) > 4 && ev[:4] == "KEY:" {
				slog.Info("voice prompt: got key", "key", ev[4:])
				return ev[4:], false
			}
			slog.Info("voice prompt: ignored event", "event", ev)
		case <-timer.C:
			slog.Info("voice prompt: timeout")
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

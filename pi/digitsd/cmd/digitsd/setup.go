package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/bootcount"
	"github.com/justinlindh/digits/pi/digitsd/internal/subsystem"
	"github.com/justinlindh/digits/pi/digitsd/internal/wifi"
)

//go:embed setup_static
var setupStaticFS embed.FS

func setupVoiceLoop(serial *subsystem.SerialModule, audioMod *subsystem.AudioModule) {
	sp := serial.Port()
	mixer := audioMod.Mixer()
	events := sp.Events()

	cfg := voicePromptConfig{
		Clip:           "wifi_setup_instructions",
		ReplayInterval: 15 * time.Second,
	}
	voicePromptLoop(events, mixer, cfg)
}

func runSetupMode(web *subsystem.WebModule, serial *subsystem.SerialModule, audio *subsystem.AudioModule) {

	// Clear boot counter so repeated setup boots don't trigger recovery.
	if err := bootcount.Clear(bootcount.DefaultPath); err != nil {
		slog.Warn("setup: failed to clear boot counter", "error", err)
	}

	if serial != nil && subsystem.IsReady(serial) {
		serial.Port().StateSet("SETUP")
	}

	mux := web.Mux()

	staticSub, err := fs.Sub(setupStaticFS, "setup_static")
	if err != nil {
		slog.Error("setup: embed sub", "error", err)
		return
	}
	mux.Handle("/", http.FileServer(http.FS(staticSub)))

	mountCaptivePortalRedirects(mux, "/")

	mux.HandleFunc("/api/networks", func(w http.ResponseWriter, r *http.Request) {
		networks, err := wifi.Scan()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(networks) //nolint:errcheck
	})

	var state struct {
		mu          sync.Mutex
		verifying   bool
		lastAttempt *wifi.VerifyResult
	}

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"verifying":    state.verifying,
			"last_attempt": state.lastAttempt,
		})
	})

	mux.HandleFunc("/api/configure", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req wifi.ConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if req.SSID == "" {
			http.Error(w, "SSID required", http.StatusBadRequest)
			return
		}

		state.mu.Lock()
		if state.verifying {
			state.mu.Unlock()
			http.Error(w, "verification already in progress", http.StatusConflict)
			return
		}
		state.verifying = true
		state.mu.Unlock()

		backupPath, err := wifi.SaveToBackup(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "verifying"}) //nolint:errcheck

		if serial != nil && subsystem.IsReady(serial) {
			serial.Port().LED("LOCK")
			serial.Port().LED("CONNECTING")
		}

		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("setup: verify goroutine panic", "panic", fmt.Sprintf("%v", r))
					state.mu.Lock()
					state.verifying = false
					state.lastAttempt = &wifi.VerifyResult{Error: fmt.Sprintf("internal error: %v", r)}
					state.mu.Unlock()
					if serial != nil && subsystem.IsReady(serial) {
						serial.Port().LED("UNLOCK")
					}
				}
			}()

			slog.Info("setup: verifying WiFi credentials", "ssid", req.SSID)
			if audio != nil && subsystem.IsReady(audio) {
				audio.Mixer().PlayOnce("wifi_connecting")
				waitForOnceComplete(audio.Mixer(), 5*time.Second)
			}
			result := wifi.Verify(req.SSID, backupPath, req.Hidden)

			if result.Connected {
				slog.Info("setup: verification succeeded, committing")
				if err := wifi.CommitToOperational(backupPath); err != nil {
					slog.Error("setup: commit failed", "error", err)
					result = wifi.VerifyResult{Error: "commit failed: " + err.Error()}
				}
			} else {
				slog.Warn("setup: verification failed", "error", result.Error)
			}

			state.mu.Lock()
			state.verifying = false
			state.lastAttempt = &result
			state.mu.Unlock()

			if result.Connected && result.Error == "" {
				slog.Info("setup: WiFi configured, rebooting")
				if serial != nil && subsystem.IsReady(serial) {
					serial.Port().LED("UNLOCK")
					serial.Port().LED("ON")
				}
				if audio != nil && subsystem.IsReady(audio) {
					audio.Mixer().PlayOnce("wifi_connected")
					waitForOnceComplete(audio.Mixer(), 10*time.Second)
				}
				doReboot()
			} else {
				if serial != nil && subsystem.IsReady(serial) {
					serial.Port().LED("UNLOCK")
				}
				if audio != nil && subsystem.IsReady(audio) {
					audio.Mixer().PlayOnce("wifi_failed")
				}
			}
		}()
	})

	slog.Info("setup: waiting for WiFi configuration via web UI")

	// Voice prompt loop: when the user picks up the handset, play setup
	// instructions on a timer, similar to recovery's voice menu.
	if serial != nil && subsystem.IsReady(serial) && audio != nil && subsystem.IsReady(audio) {
		go setupVoiceLoop(serial, audio)
	}

	select {}
}

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

func runSetupMode(mgr *subsystem.Manager, web *subsystem.WebModule, serial *subsystem.SerialModule, audio *subsystem.AudioModule, wifiAP *subsystem.WiFiAPModule) {
	_ = mgr

	// LED: signal setup mode if serial is available.
	if serial != nil && serial.IsReady() {
		sp := serial.Port()
		sp.LED("LOCK")
		time.Sleep(50 * time.Millisecond)
		sp.LED("DOUBLE_PULSE")
	}

	mux := web.Mux()

	staticSub, _ := fs.Sub(setupStaticFS, "setup_static")
	mux.Handle("/", http.FileServer(http.FS(staticSub)))

	for _, path := range []string{"/generate_204", "/hotspot-detect.html", "/connecttest.txt", "/library/test/success.html"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/", http.StatusFound)
		})
	}

	scanner := &wifi.SystemScanner{}
	mux.HandleFunc("/api/networks", func(w http.ResponseWriter, r *http.Request) {
		networks, err := scanner.Scan()
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

		backupPath, err := wifi.SaveToBackup(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "verifying"}) //nolint:errcheck

		// LED: fast blink during verification
		if serial != nil && serial.IsReady() {
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
					if serial != nil && serial.IsReady() {
						serial.Port().LED("DOUBLE_PULSE")
					}
				}
			}()

			state.mu.Lock()
			state.verifying = true
			state.mu.Unlock()

			slog.Info("setup: verifying WiFi credentials", "ssid", req.SSID)
			result := wifi.Verify(req.SSID, backupPath)

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
				if serial != nil && serial.IsReady() {
					serial.Port().LED("ON")
				}
				if audio != nil && audio.IsReady() {
					audio.Mixer().PlayOnce("confirm_wifi_setup")
					waitForOnceComplete(audio.Mixer(), 10*time.Second)
				}
				doReboot()
			} else {
				// Restore LED to setup pattern
				if serial != nil && serial.IsReady() {
					serial.Port().LED("DOUBLE_PULSE")
				}
			}
		}()
	})

	slog.Info("setup: waiting for WiFi configuration via web UI")

	// Voice prompt loop: when the user picks up the handset, play setup
	// instructions on a timer, similar to recovery's voice menu.
	if serial != nil && serial.IsReady() && audio != nil && audio.IsReady() {
		go setupVoiceLoop(serial, audio)
	}

	select {}
}

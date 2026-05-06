package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"

	"github.com/justinlindh/digits/pi/digitsd/internal/subsystem"
	"github.com/justinlindh/digits/pi/digitsd/internal/wifi"
)

//go:embed setup_static
var setupStaticFS embed.FS

func runSetupMode(mgr *subsystem.Manager, web *subsystem.WebModule, serial *subsystem.SerialModule, audio *subsystem.AudioModule, wifiAP *subsystem.WiFiAPModule) {
	_ = mgr
	_ = serial
	_ = audio

	mux := web.Mux()

	staticSub, _ := fs.Sub(setupStaticFS, "setup_static")
	mux.Handle("/", http.FileServer(http.FS(staticSub)))

	// Captive portal redirects
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

		go func() {
			state.mu.Lock()
			state.verifying = true
			state.mu.Unlock()

			result := wifi.Verify(req.SSID, backupPath)

			if result.Connected {
				if err := wifi.CommitToOperational(backupPath); err != nil {
					slog.Error("setup: commit failed", "error", err)
					result = wifi.VerifyResult{Error: "commit failed: " + err.Error()}
				}
			}

			state.mu.Lock()
			state.verifying = false
			state.lastAttempt = &result
			state.mu.Unlock()

			if result.Connected && result.Error == "" {
				slog.Info("setup: WiFi configured, rebooting")
				if err := wifiAP.Teardown(); err != nil {
					slog.Warn("setup: AP teardown failed", "error", err)
				}
				doReboot()
			}
		}()
	})

	slog.Info("setup: waiting for WiFi configuration via web UI")
	select {}
}

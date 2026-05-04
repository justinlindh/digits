package portal

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/justinlindh/digits/pi/digits-setup/internal/wifi"
	"github.com/justinlindh/digits/pi/phonekit"
)

//go:embed static/*
var staticFiles embed.FS

//go:embed audio/*
var audioFS embed.FS

// apTeardownDelay defers `digits-ap-check down` after a successful configure
// so two things can happen in order: (1) the HTTP response bytes leave wlan0
// before hostapd stops, and (2) the client has a moment to read them before
// losing AP association.
const apTeardownDelay = 2 * time.Second

// LastAttempt records the most recent Wi-Fi verification failure so the
// frontend can poll /api/status and display the error.
type LastAttempt struct {
	SSID  string `json:"ssid"`
	Error string `json:"error"`
}

type portalState struct {
	mu          sync.Mutex
	lastAttempt *LastAttempt
	verifying   bool
}

// loadSetupAudio returns the named WAV file from the embedded audio directory,
// or nil if the file does not exist (safe to call with placeholder-only builds).
func loadSetupAudio(name string) []byte {
	data, err := audioFS.ReadFile("audio/" + name)
	if err != nil {
		return nil
	}
	return data
}

// NewHandler returns the HTTP mux for the captive portal. phone may be nil if
// the serial connection to the Pico could not be established; LED and audio
// feedback are silently skipped in that case.
func NewHandler(scanner wifi.Scanner, ap wifi.APController, phone *phonekit.Phone) http.Handler {
	mux := http.NewServeMux()

	var state portalState

	// teardownScheduled gates the deferred AP teardown so a duplicate or
	// retried /api/configure cannot stack multiple `digits-ap-check down`
	// goroutines.
	var teardownScheduled atomic.Bool

	// Captive portal detection: redirect to setup page
	captiveRedirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.Handle("/generate_204", captiveRedirect)
	mux.Handle("/hotspot-detect.html", captiveRedirect)
	mux.Handle("/connecttest.txt", captiveRedirect)
	mux.Handle("/library/test/success.html", captiveRedirect)

	// API: scan networks
	mux.HandleFunc("/api/networks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		networks, err := scanner.Scan()
		if err != nil {
			log.Printf("scan error: %v", err)
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(networks); err != nil {
			log.Printf("networks: encode response: %v", err)
		}
	})

	// API: status (polled by frontend during verification)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state.mu.Lock()
		resp := struct {
			LastAttempt *LastAttempt `json:"last_attempt"`
			Verifying   bool        `json:"verifying"`
		}{
			LastAttempt: state.lastAttempt,
			Verifying:   state.verifying,
		}
		state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("status: encode response: %v", err)
		}
	})

	// API: configure (save, verify, commit)
	mux.HandleFunc("/api/configure", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req wifi.ConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		backupPath, err := wifi.SaveToBackup(req)
		if err != nil {
			log.Printf("save error: %v", err)
			status := http.StatusInternalServerError
			if errors.Is(err, wifi.ErrInvalidRequest) {
				status = http.StatusBadRequest
			}
			http.Error(w, err.Error(), status)
			return
		}

		state.mu.Lock()
		state.verifying = true
		state.lastAttempt = nil
		state.mu.Unlock()

		// Return 202 immediately so the frontend can show verifying state.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "verifying"}); err != nil {
			log.Printf("configure: encode response: %v", err)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Verify in background.
		go func() {
			if phone != nil {
				_ = phone.LED("DOUBLE_PULSE")
			}

			result := wifi.Verify(req.SSID, backupPath)

			if result.Connected {
				log.Printf("wifi verified: %s connected", req.SSID)
				if err := wifi.CommitToOperational(backupPath); err != nil {
					log.Printf("commit error: %v", err)
				}
				if phone != nil {
					_ = phone.SetPhase("UNPAIRED")
					if clip := loadSetupAudio("wifi_connected.wav"); clip != nil {
						_ = phone.Play(context.Background(), clip)
					}
				}
				state.mu.Lock()
				state.verifying = false
				state.mu.Unlock()

				// Schedule AP teardown.
				if teardownScheduled.CompareAndSwap(false, true) {
					go func() {
						time.Sleep(apTeardownDelay)
						if err := ap.Down(); err != nil {
							log.Printf("ap down failed: %v", err)
						}
					}()
				}
			} else {
				log.Printf("wifi verify failed: %s: %s", req.SSID, result.Error)
				os.Remove(backupPath)
				if phone != nil {
					_ = phone.LED("BLINK")
					if clip := loadSetupAudio("wifi_failed.wav"); clip != nil {
						_ = phone.Play(context.Background(), clip)
					}
				}
				state.mu.Lock()
				state.verifying = false
				state.lastAttempt = &LastAttempt{
					SSID:  req.SSID,
					Error: result.Error,
				}
				state.mu.Unlock()
			}
		}()
	})

	// Static files (index.html, style.css, app.js)
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic("portal: static embed broken: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle("/", fileServer)

	return mux
}

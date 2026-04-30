package portal

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/justinlindh/digits/pi/digits-setup/internal/wifi"
)

//go:embed static/*
var staticFiles embed.FS

// apTeardownDelay defers `digits-ap-check down` after a successful configure
// so two things can happen in order: (1) the HTTP response bytes leave wlan0
// before hostapd stops, and (2) the client has a moment to read them before
// losing AP association.
const apTeardownDelay = 2 * time.Second

// NewHandler returns the HTTP mux for the captive portal. The configure
// argument is invoked for each POST /api/configure with the request body.
func NewHandler(scanner wifi.Scanner, configure func(wifi.ConfigRequest) error, ap wifi.APController) http.Handler {
	mux := http.NewServeMux()

	// teardownScheduled gates the deferred AP teardown so a duplicate or
	// retried /api/configure cannot stack multiple `digits-ap-check down`
	// goroutines.
	var teardownScheduled atomic.Bool

	// Captive portal detection — redirect to setup page
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

	// API: configure
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

		if err := configure(req); err != nil {
			log.Printf("configure error: %v", err)
			status := http.StatusInternalServerError
			if errors.Is(err, wifi.ErrInvalidRequest) {
				status = http.StatusBadRequest
			}
			http.Error(w, err.Error(), status)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"message": "Configuration saved. Reconnect to your normal network.",
		}); err != nil {
			log.Printf("configure: encode response: %v", err)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		if teardownScheduled.CompareAndSwap(false, true) {
			go func() {
				time.Sleep(apTeardownDelay)
				if err := ap.Down(); err != nil {
					log.Printf("configure: ap down failed: %v", err)
				}
			}()
		}
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

package portal

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"

	"github.com/justinlindh/digits/pi/digits-setup/internal/wifi"
)

//go:embed static/*
var staticFiles embed.FS

// NewHandler returns the HTTP mux for the captive portal.
func NewHandler(scanner wifi.Scanner, configurator wifi.Configurator) http.Handler {
	mux := http.NewServeMux()

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

		if err := configurator.Configure(req); err != nil {
			log.Printf("configure error: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"message": "Configuration saved. Rebooting in 5 seconds...",
		}); err != nil {
			log.Printf("configure: encode response: %v", err)
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

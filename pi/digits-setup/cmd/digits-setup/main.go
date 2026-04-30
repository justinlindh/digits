package main

import (
	"log"
	"net/http"
	"os"

	"github.com/justinlindh/digits/pi/digits-setup/internal/portal"
	"github.com/justinlindh/digits/pi/digits-setup/internal/wifi"
)

func main() {
	addr := ":80"
	if p := os.Getenv("DIGITS_SETUP_PORT"); p != "" {
		addr = ":" + p
	}

	scanner := &wifi.SystemScanner{}
	ap := wifi.SystemAPController{}

	mux := portal.NewHandler(scanner, wifi.Configure, ap)

	log.Printf("digits-setup listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

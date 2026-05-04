package main

import (
	"log"
	"net/http"
	"os"

	"github.com/justinlindh/digits/pi/digits-setup/internal/portal"
	"github.com/justinlindh/digits/pi/digits-setup/internal/wifi"
	"github.com/justinlindh/digits/pi/phonekit"
)

func main() {
	addr := ":80"
	if p := os.Getenv("DIGITS_SETUP_PORT"); p != "" {
		addr = ":" + p
	}

	var phone *phonekit.Phone
	p, err := phonekit.Open("/dev/serial0", 115200)
	if err != nil {
		log.Printf("phonekit: %v (LED and audio disabled)", err)
	} else {
		phone = p
		defer phone.Close()
		if err := phone.Ping(); err != nil {
			log.Printf("phonekit: ping failed: %v", err)
		}
	}

	scanner := &wifi.SystemScanner{}
	ap := wifi.SystemAPController{}

	mux := portal.NewHandler(scanner, ap, phone)

	log.Printf("digits-setup listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

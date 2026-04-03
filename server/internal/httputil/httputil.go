package httputil

import "net/http"

// Healthz returns an http.HandlerFunc that responds 200 "ok".
func Healthz() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	}
}

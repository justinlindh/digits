package httputil

import (
	"encoding/json"
	"net/http"
)

// HealthResponse is the body returned by Healthz. autodeploy decodes the
// same struct to confirm a just-pulled container is serving the expected
// version, so producer and consumer stay in lockstep.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Healthz returns an http.HandlerFunc that responds 200 with HealthResponse.
func Healthz(version string) http.HandlerFunc {
	body, err := json.Marshal(HealthResponse{Status: "ok", Version: version})
	if err != nil {
		// HealthResponse holds two strings; Marshal cannot fail.
		panic(err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
	}
}

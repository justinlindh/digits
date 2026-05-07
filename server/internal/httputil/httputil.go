package httputil

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
)

// HealthResponse is the body returned by Healthz. autodeploy decodes the
// same struct to confirm a just-pulled container is serving the expected
// version, so producer and consumer stay in lockstep.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// StatusRecorder captures the response status code without buffering the body.
// Flush and Hijack pass through to the underlying ResponseWriter so SSE
// streaming and WebSocket upgrades work correctly.
type StatusRecorder struct {
	http.ResponseWriter
	Status      int
	WroteHeader bool
}

func (s *StatusRecorder) WriteHeader(code int) {
	if s.WroteHeader {
		return
	}
	s.Status = code
	s.WroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *StatusRecorder) Write(b []byte) (int, error) {
	if !s.WroteHeader {
		s.Status = http.StatusOK
		s.WroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *StatusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *StatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
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

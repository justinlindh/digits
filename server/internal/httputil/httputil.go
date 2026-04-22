package httputil

import (
	"net/http"
)

// Healthz returns an http.HandlerFunc that responds 200 with a small JSON
// payload including the build version. autodeploy reads the version field
// to confirm a just-pulled container is actually serving traffic.
func Healthz(version string) http.HandlerFunc {
	body := []byte(`{"status":"ok","version":` + quoteJSON(version) + `}`)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
	}
}

func quoteJSON(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			out = append(out, '\\', s[i])
		default:
			out = append(out, s[i])
		}
	}
	out = append(out, '"')
	return string(out)
}

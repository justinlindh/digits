package autodeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/justinlindh/digits/server/internal/httputil"
)

// PollHealth issues GETs to url at interval until the response is a 200 with
// version == wantVersion, or ctx is done.
func PollHealth(ctx context.Context, url, wantVersion string, interval time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
	var lastErr error
	lastBody := ""
	lastStatus := 0

	try := func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		lastStatus = resp.StatusCode
		lastBody = string(b)
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var h httputil.HealthResponse
		if err := json.Unmarshal(b, &h); err != nil {
			lastErr = fmt.Errorf("decode: %w", err)
			return false
		}
		if h.Version != wantVersion {
			lastErr = fmt.Errorf("version=%q want=%q", h.Version, wantVersion)
			return false
		}
		lastErr = nil
		return true
	}

	if try() {
		return nil
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("healthcheck timeout: %s (last status=%d body=%q): %w",
					url, lastStatus, lastBody, lastErr)
			}
			return fmt.Errorf("healthcheck timeout: %s (last status=%d body=%q)",
				url, lastStatus, lastBody)
		case <-t.C:
			if try() {
				return nil
			}
		}
	}
}

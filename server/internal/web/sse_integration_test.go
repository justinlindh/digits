//go:build integration

package web

// Integration tests for GET /api/call/{id}/link-health/stream.
//
// Verified behaviours:
//   - 200 with Content-Type: text/event-stream for an endpoint owner.
//   - Initial "sample" frame emitted immediately on connect.
//   - Subsequent Record calls emit "sample" frames.
//   - HealthStore.Evict causes an "ended" frame and stream close.
//   - HealthStore.NotifyDisconnected causes a "disconnect" frame with the label.
//   - Unauthenticated requests are redirected (not 200).
//   - Unrelated-household user gets 404.
//   - Ended call (status="ended" in DB) returns 404 before any bytes are written.
//
// All tests require TEST_DATABASE_URL (skipped otherwise via setupCallsTestServer).
// Each test uses newLHEnv for distinct numbers and -count=2 safety.

import (
	"bufio"
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/calls"
)

// sseReader wraps a single bufio.Scanner so the internal read buffer is
// shared across successive readSSEFrame calls on the same response body.
// Creating a fresh bufio.Scanner per call would silently discard bytes that
// were read-ahead but not yet consumed by the previous call.
type sseReader struct {
	scanner *bufio.Scanner
}

func newSSEReader(r *http.Response) *sseReader {
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	return &sseReader{scanner: sc}
}

// readSSEFrame reads the next complete SSE event from the shared scanner.
// Blocks until an event arrives, the body closes, or ctx fires.
func readSSEFrame(t *testing.T, ctx context.Context, sr *sseReader) (event, data string, err error) {
	t.Helper()
	type frame struct{ event, data string }
	ch := make(chan frame, 1)
	errCh := make(chan error, 1)
	go func() {
		var ev, d strings.Builder
		for sr.scanner.Scan() {
			line := sr.scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Reset()
				ev.WriteString(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				if d.Len() > 0 {
					d.WriteByte('\n')
				}
				d.WriteString(strings.TrimPrefix(line, "data: "))
			case line == "":
				if ev.Len() > 0 {
					ch <- frame{event: ev.String(), data: d.String()}
					return
				}
				ev.Reset()
				d.Reset()
			}
		}
		if err := sr.scanner.Err(); err != nil {
			errCh <- err
		}
	}()
	select {
	case f := <-ch:
		return f.event, f.data, nil
	case err := <-errCh:
		return "", "", err
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

// sseStreamURL builds the SSE stream endpoint URL for a call ID.
func sseStreamURL(s lhSetup, callID int64) string {
	return s.env.srv.URL + "/api/call/" + strconv.FormatInt(callID, 10) + "/link-health/stream"
}

func TestSSEStream_ReceivesSamples(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	client := authedClient(t, s, s.userA)
	req, err := http.NewRequest("GET", sseStreamURL(s, callID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type: got %q want text/event-stream", ct)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sr := newSSEReader(resp)

	// The initial snapshot must arrive as a "sample" event.
	ev, _, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read initial frame: %v", err)
	}
	if ev != "sample" {
		t.Fatalf("initial event: got %q want sample", ev)
	}

	// Give the handler goroutine time to reach Subscribe() after flushing
	// the initial snapshot. Subscribe happens after the flush, so a brief
	// pause prevents a Record broadcast that fires before any subscriber
	// is registered.
	time.Sleep(50 * time.Millisecond)

	// Record a new sample; the handler must forward it as another "sample".
	loss := float32(0.7)
	s.env.healthStore.Record(callID, s.numA, calls.Sample{TS: time.Now(), LossPct: &loss})

	ev2, _, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read sample frame: %v", err)
	}
	if ev2 != "sample" {
		t.Fatalf("second event: got %q want sample", ev2)
	}
}

func TestSSEStream_ReceivesEndedOnEvict(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	client := authedClient(t, s, s.userA)
	req, err := http.NewRequest("GET", sseStreamURL(s, callID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sr := newSSEReader(resp)

	// Drain the initial snapshot.
	if _, _, err := readSSEFrame(t, ctx, sr); err != nil {
		t.Fatalf("read initial frame: %v", err)
	}

	// Evict closes all subscriber channels; the handler sends "ended" and returns.
	s.env.healthStore.Evict(callID)

	ev, _, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read ended frame: %v", err)
	}
	if ev != "ended" {
		t.Fatalf("event after evict: got %q want ended", ev)
	}
}

func TestSSEStream_ReceivesDisconnect(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	client := authedClient(t, s, s.userA)
	req, err := http.NewRequest("GET", sseStreamURL(s, callID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sr := newSSEReader(resp)

	// Drain the initial snapshot.
	if _, _, err := readSSEFrame(t, ctx, sr); err != nil {
		t.Fatalf("read initial frame: %v", err)
	}

	// Broadcast a disconnect notification with a known label.
	s.env.healthStore.NotifyDisconnected(callID, "Alice")

	ev, data, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read disconnect frame: %v", err)
	}
	if ev != "disconnect" {
		t.Fatalf("event: got %q want disconnect", ev)
	}
	if !strings.Contains(data, "Alice") {
		t.Fatalf("disconnect data missing label: %q", data)
	}
}

func TestSSEStream_AuthRequired(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	// No session cookie -- unauthenticated client.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(sseStreamURL(s, callID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("unauthenticated stream returned 200; want redirect or 4xx")
	}
}

func TestSSEStream_UnrelatedHouseholdGets404(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	client := authedClient(t, s, s.userC) // userC is in an unrelated household
	req, err := http.NewRequest("GET", sseStreamURL(s, callID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unrelated household: got %d want 404", resp.StatusCode)
	}
}

func TestSSEStream_EndedCallGets404(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	// End the call so DB status = 'ended'.
	if err := s.env.tracker.OnCallEnded(s.numA, s.numB); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	client := authedClient(t, s, s.userA)
	req, err := http.NewRequest("GET", sseStreamURL(s, callID), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ended call: got %d want 404", resp.StatusCode)
	}
}

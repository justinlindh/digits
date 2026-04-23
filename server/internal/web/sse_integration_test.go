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

	"github.com/google/uuid"
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
	if err := s.env.tracker.OnCallEnded(context.Background(), s.numA, s.numB); err != nil {
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

// startConference seeds two 2-party calls (host->A, host->B where host is
// numA, A is numB, B is numC), then merges them into a conference. Returns
// the conference UUID. Caller owns cleanup via t.Cleanup on any rows
// created (newLHEnv already cleans up calls table).
func startConference(t *testing.T, s lhSetup) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tr := s.env.tracker
	if _, err := tr.OnCallInitiated(ctx, s.numA, s.numB); err != nil {
		t.Fatalf("OnCallInitiated A->B: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, s.numA, s.numC); err != nil {
		t.Fatalf("OnCallInitiated A->C: %v", err)
	}
	if err := tr.OnCallAnswered(ctx, s.numA, s.numB); err != nil {
		t.Fatalf("OnCallAnswered A->B: %v", err)
	}
	if err := tr.OnCallAnswered(ctx, s.numA, s.numC); err != nil {
		t.Fatalf("OnCallAnswered A->C: %v", err)
	}
	originatingCallID := tr.CallIDForPair(ctx, s.numA, s.numB)
	conf, err := tr.CreateConferencePersistent(ctx, s.numA, originatingCallID, []string{s.numB, s.numC})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}
	return conf.ID
}

// confStreamURL builds the SSE stream endpoint URL for a conference UUID.
func confStreamURL(s lhSetup, confID uuid.UUID) string {
	return s.env.srv.URL + "/api/conference/" + confID.String() + "/link-health/stream"
}

func TestConferenceSSEStream_ReceivesInitialSample(t *testing.T) {
	s := newLHEnv(t)
	confID := startConference(t, s)

	client := authedClient(t, s, s.userA)
	req, err := http.NewRequest("GET", confStreamURL(s, confID), nil)
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

	event, data, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read initial frame: %v", err)
	}
	if event != "sample" {
		t.Fatalf("initial event: got %q want sample", event)
	}
	// The data is the rendered matrix partial -- just verify it contains
	// the matrix wrapper element so we know it is the conference partial.
	if !strings.Contains(data, "deck-matrix") {
		t.Fatalf("initial data missing matrix marker; data=%q", data)
	}
}

func TestConferenceSSEStream_ReceivesSampleOnRecordEdge(t *testing.T) {
	s := newLHEnv(t)
	confID := startConference(t, s)

	client := authedClient(t, s, s.userA)
	req, _ := http.NewRequest("GET", confStreamURL(s, confID), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sr := newSSEReader(resp)

	// Drain the initial sample.
	if _, _, err := readSSEFrame(t, ctx, sr); err != nil {
		t.Fatalf("drain initial: %v", err)
	}

	// RecordEdge after subscription is live.
	loss := float32(1.25)
	s.env.healthStore.RecordEdge(confID, s.numA, s.numB,
		calls.Sample{TS: time.Now(), LossPct: &loss})

	ev, data, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read post-record frame: %v", err)
	}
	if ev != "sample" {
		t.Fatalf("post-record event: got %q want sample", ev)
	}
	if !strings.Contains(data, "1.2%") && !strings.Contains(data, "1.3%") {
		// The matrix renders LossPct with "%.1f%%" so 1.25 displays as "1.2%"
		// or "1.3%" depending on rounding mode. Accept either.
		t.Errorf("post-record data missing rendered lossPct; data=%q", data)
	}
}

func TestConferenceSSEStream_ReceivesEndedOnEvict(t *testing.T) {
	s := newLHEnv(t)
	confID := startConference(t, s)

	client := authedClient(t, s, s.userA)
	req, _ := http.NewRequest("GET", confStreamURL(s, confID), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sr := newSSEReader(resp)

	if _, _, err := readSSEFrame(t, ctx, sr); err != nil {
		t.Fatalf("drain initial: %v", err)
	}

	// EndConferencePersistent fires EvictConference which closes the subscription.
	if err := s.env.tracker.EndConferencePersistent(context.Background(), confID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	ev, _, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read ended frame: %v", err)
	}
	if ev != "ended" {
		t.Fatalf("event on evict: got %q want ended", ev)
	}
}

func TestConferenceSSEStream_UnrelatedHouseholdGets404(t *testing.T) {
	s := newLHEnv(t)
	confID := startConference(t, s)

	// Create a fourth user in a fourth household that owns a line but is
	// NOT a conference member.
	numD := nextPhone()
	hwD := "conf-sse-d-" + numD
	emailD := "conf-sse-d-" + numD + "@example.com"
	hhNameD := "Conf SSE D " + numD
	t.Cleanup(func() {
		db := s.env.database.DB
		_, _ = db.Exec("DELETE FROM devices WHERE hardware_id = $1", hwD)
		_, _ = db.Exec("DELETE FROM lines WHERE number = $1", numD)
		_, _ = db.Exec("DELETE FROM household_members WHERE household_id IN (SELECT id FROM households WHERE name = $1)", hhNameD)
		_, _ = db.Exec("DELETE FROM households WHERE name = $1", hhNameD)
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email = $1)", emailD)
		_, _ = db.Exec("DELETE FROM users WHERE email = $1", emailD)
	})
	userD, err := s.env.authStore.CreateUser(context.Background(), emailD, "Conf SSE D", nil)
	if err != nil {
		t.Fatalf("create user D: %v", err)
	}
	hhD, err := s.env.householdStore.Create(context.Background(), hhNameD, userD.ID)
	if err != nil {
		t.Fatalf("create household D: %v", err)
	}
	codeD, err := s.env.pairingStore.GenerateCode(context.Background(), hwD)
	if err != nil {
		t.Fatalf("gen code D: %v", err)
	}
	if _, _, err := s.env.pairingStore.ClaimDevice(context.Background(), codeD, numD, "Phone D", hhD.ID); err != nil {
		t.Fatalf("claim D: %v", err)
	}

	client := authedClient(t, s, userD)
	req, _ := http.NewRequest("GET", confStreamURL(s, confID), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unrelated user: got %d want 404", resp.StatusCode)
	}
}

func TestConferenceSSEStream_EndedConferenceGets404(t *testing.T) {
	s := newLHEnv(t)
	confID := startConference(t, s)
	if err := s.env.tracker.EndConferencePersistent(context.Background(), confID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	client := authedClient(t, s, s.userA)
	req, _ := http.NewRequest("GET", confStreamURL(s, confID), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ended conference: got %d want 404", resp.StatusCode)
	}
}

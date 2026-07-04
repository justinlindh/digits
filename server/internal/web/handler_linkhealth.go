package web

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/line"
)

// measurement returned by GET /api/call/{id}/link-health.
type LinkHealthSample struct {
	TS       int64    `json:"ts"`
	LossPct  *float32 `json:"loss_pct,omitempty"`
	JitterMs *float32 `json:"jitter_ms,omitempty"`
	RttMs    *float32 `json:"rtt_ms,omitempty"`
	ConnType string   `json:"conn_type,omitempty"`
	BytesIn  *int64   `json:"bytes_in,omitempty"`
	BytesOut *int64   `json:"bytes_out,omitempty"`
}

// LinkHealthEndpointResp is the per-endpoint section of a LinkHealthResp.
type LinkHealthEndpointResp struct {
	Number      string             `json:"number"`
	DisplayName string             `json:"display_name"`
	Latest      *LinkHealthSample  `json:"latest,omitempty"`
	Window      []LinkHealthSample `json:"window"`
}

// LinkHealthResp is the top-level response body for GET /api/call/{id}/link-health.
type LinkHealthResp struct {
	CallID    int64                  `json:"call_id"`
	StartedAt time.Time              `json:"started_at"`
	Caller    LinkHealthEndpointResp `json:"caller"`
	Callee    LinkHealthEndpointResp `json:"callee"`
}

func toAPISample(s calls.Sample) LinkHealthSample {
	return LinkHealthSample{
		TS:       s.TS.UnixMilli(),
		LossPct:  s.LossPct,
		JitterMs: s.JitterMs,
		RttMs:    s.RttMs,
		ConnType: s.ConnType,
		BytesIn:  s.BytesIn,
		BytesOut: s.BytesOut,
	}
}

// samplesToWindow converts a calls.Sample slice into a LinkHealthSample window
// and a pointer to the most recent sample. Returns an empty (non-nil) window
// and a nil latest when samples is empty, matching the JSON shape callers
// emit for endpoints with no observations.
func samplesToWindow(samples []calls.Sample) ([]LinkHealthSample, *LinkHealthSample) {
	if len(samples) == 0 {
		return []LinkHealthSample{}, nil
	}
	window := make([]LinkHealthSample, len(samples))
	for i, s := range samples {
		window[i] = toAPISample(s)
	}
	latest := window[len(window)-1]
	return window, &latest
}

func (h *Handler) handleCallLinkHealth(w http.ResponseWriter, r *http.Request) {
	callID, ok := parseCallID(w, r)
	if !ok {
		return
	}
	call, ownedLines, primaryHH, ok := h.requireCallEndpointOwnership(w, r, callID)
	if !ok {
		return
	}

	// Display-name resolution: same helpers as /calls page. No new data exposure.
	// Linked-household names are shown for peers that the user already sees in
	// their call log; the underlying auth check does not grant read access to
	// calls the user was not part of.
	nr := nameResolver{ownedLines: ownedLines, linkedIndex: h.linkedIndexForHousehold(r.Context(), primaryHH)}

	caller, callee, err := h.buildCallEndpoints(r.Context(), call, nr)
	if err != nil {
		slog.ErrorContext(r.Context(), "link_health: build endpoints failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := LinkHealthResp{CallID: call.ID, StartedAt: call.StartedAt, Caller: caller, Callee: callee}

	if err := writeJSON(w, resp); err != nil {
		slog.ErrorContext(r.Context(), "link_health encode failed", "call_id", callID, "err", err)
	}
}

// buildCallEndpoints assembles the caller and callee LinkHealthEndpointResp
// values for a 2-party call in one go. The same caller/callee pair is needed
// by the JSON endpoint, the live-detail page, and the SSE sample frame; this
// helper keeps the duplicated nil-error short-circuit out of all three.
func (h *Handler) buildCallEndpoints(ctx context.Context, call calls.Call, nr nameResolver) (LinkHealthEndpointResp, LinkHealthEndpointResp, error) {
	caller, err := h.buildLinkHealthEndpoint(ctx, call.ID, call.Caller, nr)
	if err != nil {
		return LinkHealthEndpointResp{}, LinkHealthEndpointResp{}, fmt.Errorf("caller: %w", err)
	}
	callee, err := h.buildLinkHealthEndpoint(ctx, call.ID, call.Callee, nr)
	if err != nil {
		return LinkHealthEndpointResp{}, LinkHealthEndpointResp{}, fmt.Errorf("callee: %w", err)
	}
	return caller, callee, nil
}

func (h *Handler) buildLinkHealthEndpoint(ctx context.Context, callID int64, number string, nr nameResolver) (LinkHealthEndpointResp, error) {
	out := LinkHealthEndpointResp{Number: number, Window: []LinkHealthSample{}}

	out.DisplayName = nr.display(number)

	if windowMem := h.healthStore.Window(callID, number); len(windowMem) > 0 {
		out.Window, out.Latest = samplesToWindow(windowMem)
		return out, nil
	}

	dbSamples, err := h.healthStore.Readback(ctx, callID, number, calls.RingCapacity)
	if err != nil {
		return out, fmt.Errorf("readback %d/%s: %w", callID, number, err)
	}
	out.Window, out.Latest = samplesToWindow(dbSamples)
	return out, nil
}

// sseHeartbeatInterval is how often we emit a synthetic heartbeat event
// on the link-health stream. Clients use this for liveness detection:
// absence of any event for >2x this interval triggers a "connection lost"
// banner.
const sseHeartbeatInterval = 15 * time.Second

// handleCallLinkHealthStream opens an SSE stream for a call's telemetry.
// Delivers:
//   - one initial "sample" event per endpoint with the current snapshot
//   - one "sample" event per future Record
//   - one "disconnect" event if a user force-disconnects
//   - one "ended" event when the call ends (any cause), then closes
//   - periodic "heartbeat" events for client-side liveness
//
// Auth: same as the JSON endpoint (direct-endpoint-ownership). Ended calls
// return 404 before any stream bytes are written.
func (h *Handler) handleCallLinkHealthStream(w http.ResponseWriter, r *http.Request) {
	callID, ok := parseCallID(w, r)
	if !ok {
		return
	}
	call, ownedLines, _, ok := h.requireCallEndpointOwnership(w, r, callID)
	if !ok {
		return
	}
	if call.Status == calls.CallStatusEnded {
		http.NotFound(w, r)
		return
	}

	flusher, ok := startSSE(w, r)
	if !ok {
		return
	}

	// Compute the linked-families index once at subscribe time. It can't
	// change mid-call (household membership changes don't retroactively
	// apply to a live call), and buildLinkedFamilies + buildLinkedLineIndex
	// together issue DB queries we don't want on the per-sample hot path.
	nr := nameResolver{ownedLines: ownedLines, linkedIndex: h.linkedIndexForCall(r.Context(), ownedLines)}

	if err := h.writeSampleEvent(r.Context(), w, flusher, call, nr); err != nil {
		slog.DebugContext(r.Context(), "SSE stream: initial snapshot write failed", "call_id", callID, "err", err)
		return
	}

	sub := h.healthStore.Subscribe(callID)
	defer sub.Close()

	// Re-check liveness AFTER subscribing. Subscribe lazily creates the
	// session, so if the call ended on another pod between the ownership
	// check and the Subscribe, the cross-pod evict has already passed and
	// this session would never receive EndedKind. The DB status is
	// authoritative; flip straight to the terminal state.
	if cur, err := h.tracker.GetCall(r.Context(), callID); err == nil && cur.Status == calls.CallStatusEnded {
		_ = writeSSE(w, sseEventEnded, renderEndedFragment(""))
		flusher.Flush()
		return
	}

	streamSSE(r.Context(), w, flusher, sub, renderEndedFragment(""), func(ev calls.Event) error {
		if err := h.writeEvent(r.Context(), w, flusher, call, nr, ev); err != nil {
			slog.DebugContext(r.Context(), "SSE stream: write failed; client gone", "call_id", callID, "err", err)
			return err
		}
		return nil
	})
}

// streamSSE runs the heartbeat/event select loop shared by the link-health
// SSE streams (2-party calls and conferences). It forwards each subscription
// event to onEvent, writes endedFragment when the subscription channel closes
// (the cross-pod Evict path), and emits a heartbeat on every tick. It returns
// when the client disconnects, the subscription closes, or onEvent reports a
// write failure. Callers do their own pre-loop setup (initial snapshot,
// subscribe ordering) because that part legitimately differs between the two
// streams.
func streamSSE(ctx context.Context, w io.Writer, flusher http.Flusher, sub *calls.Subscription, endedFragment string, onEvent func(ev calls.Event) error) {
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.C:
			if !ok {
				// Channel closed by Evict. Send one final ended event and return.
				_ = writeSSE(w, sseEventEnded, endedFragment)
				flusher.Flush()
				return
			}
			if err := onEvent(ev); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := writeSSE(w, sseEventHeartbeat, "{}"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// startSSE asserts that w supports flushing, writes the standard
// Server-Sent Events response headers, and flushes them so the client sees
// the stream open immediately. On success it returns the flusher with
// ok=true; on failure it has already written a 500 response and the caller
// must return without writing further.
func startSSE(w http.ResponseWriter, r *http.Request) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.ErrorContext(r.Context(), "SSE: ResponseWriter does not implement Flusher")
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return flusher, true
}

// SSE event names emitted by the link-health streams. These are a wire
// contract: the htmx sse-swap attributes in call-live-detail.html and
// conference-live-detail.html subscribe to "sample", "ended", and "disconnect"
// by these exact names, so a typo here silently breaks the client swap with no
// compile error. The two stream handlers (2-party and conference) must agree on
// the vocabulary, which is why it lives in one place. "heartbeat" keeps the
// connection warm and has no client swap.
const (
	sseEventSample     = "sample"
	sseEventEnded      = "ended"
	sseEventDisconnect = "disconnect"
	sseEventHeartbeat  = "heartbeat"
)

// writeSSE emits one SSE event frame: "event: <name>\ndata: <data>\n\n".
func writeSSE(w io.Writer, event, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	// Each line of data must be prefixed per the SSE spec.
	for _, line := range strings.Split(data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\n")
	return err
}

// writeSampleEvent renders the call-live panel for both endpoints and emits
// it as a "sample" SSE frame. Used for the initial stream snapshot and for
// every subsequent SampleKind event; the two are the same frame.
func (h *Handler) writeSampleEvent(ctx context.Context, w io.Writer, flusher http.Flusher, call calls.Call, nr nameResolver) error {
	callerEp, calleeEp, err := h.buildCallEndpoints(ctx, call, nr)
	if err != nil {
		return err
	}
	fragment, err := h.renderLinkHealthPanel(call, callerEp, calleeEp)
	if err != nil {
		return err
	}
	if err := writeSSE(w, sseEventSample, fragment); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// writeTerminalEvent writes an SSE frame for an EndedKind or DisconnectKind
// event, rendering the ended/disconnect body via renderEnded so the 2-party
// and conference streams can share the same terminal-event handling while
// keeping their own deck wording. Returns true if the event was a terminal
// kind and was handled, false for SampleKind (which the caller handles).
func writeTerminalEvent(w io.Writer, flusher http.Flusher, ev calls.Event, renderEnded func(endedBy string) string) (bool, error) {
	switch ev.Kind {
	case calls.EndedKind:
		if err := writeSSE(w, sseEventEnded, renderEnded("")); err != nil {
			return true, err
		}
	case calls.DisconnectKind:
		if err := writeSSE(w, sseEventDisconnect, renderEnded(ev.EndedBy)); err != nil {
			return true, err
		}
	default:
		return false, nil
	}
	flusher.Flush()
	return true, nil
}

func (h *Handler) writeEvent(ctx context.Context, w io.Writer, flusher http.Flusher, call calls.Call, nr nameResolver, ev calls.Event) error {
	if handled, err := writeTerminalEvent(w, flusher, ev, renderEndedFragment); handled {
		return err
	}
	// SampleKind
	return h.writeSampleEvent(ctx, w, flusher, call, nr)
}

// linkedIndexForCall builds the linked-families index for display-name
// resolution. Returns nil when the user belongs to no households.
func (h *Handler) linkedIndexForCall(ctx context.Context, ownedLines map[string]*line.Line) map[string]string {
	for _, ln := range ownedLines {
		if ln != nil {
			return buildLinkedLineIndex(h.buildLinkedFamilies(ctx, ln.HouseholdID))
		}
	}
	return nil
}

// renderLinkHealthPanel executes the _call-live-panel.html template against
// a LinkHealthResp and returns the rendered HTML.
func (h *Handler) renderLinkHealthPanel(call calls.Call, caller, callee LinkHealthEndpointResp) (string, error) {
	data := LinkHealthResp{CallID: call.ID, StartedAt: call.StartedAt, Caller: caller, Callee: callee}
	return renderFragment(h.tmplCallLivePanel, "call-live-panel", data)
}

// renderEndedFragment returns the small HTML shown when a call ends.
func renderEndedFragment(endedBy string) string {
	if endedBy != "" {
		return fmt.Sprintf(`<div class="deck-ended">Ended by %s.</div>`, html.EscapeString(endedBy))
	}
	return `<div class="deck-ended">Call ended.</div>`
}

// renderEndedConferenceFragment is the matrix-deck counterpart of
// renderEndedFragment. Returns the small HTML shown when a conference ends.
func renderEndedConferenceFragment(endedBy string) string {
	if endedBy != "" {
		return fmt.Sprintf(`<div class="deck-ended">Conference ended by %s.</div>`, html.EscapeString(endedBy))
	}
	return `<div class="deck-ended">Conference ended.</div>`
}

// handleCallDisconnect force-ends an active call. Any user whose household
// owns either endpoint (direct ownership only; linked households do NOT
// qualify) can trigger this. The server records the actor in calls.force_ended_by,
// notifies any open SSE subscribers, sends hangup to both peers via the
// relay, and calls Tracker.OnCallEnded for deterministic DB close.
//
// Idempotent: calling against an already-ended call returns 200 without
// overwriting the audit column.
func (h *Handler) handleCallDisconnect(w http.ResponseWriter, r *http.Request) {
	callID, ok := parseCallID(w, r)
	if !ok {
		return
	}
	call, _, _, ok := h.requireCallEndpointOwnership(w, r, callID)
	if !ok {
		return
	}

	// Idempotency: if the call already ended, just return 200 without
	// touching the audit column.
	if call.Status == calls.CallStatusEnded {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
		return
	}

	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return
	}

	// Record who force-ended the call BEFORE the teardown fires, so the
	// audit row is in place even if we crash mid-teardown.
	if err := h.tracker.MarkForceEnded(r.Context(), callID, user.ID); err != nil {
		slog.ErrorContext(r.Context(), "force-disconnect audit write failed", "call_id", callID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Notify SSE subscribers before teardown so open pages flip to terminal
	// state immediately rather than flickering through a dropped stream.
	label := userDisplayLabel(user)
	h.healthStore.NotifyDisconnected(callID, label)

	// Send hangup to both peers. Errors per-peer are logged in ForceHangup.
	h.relay.ForceHangup(r.Context(), call.Caller, call.Callee)

	// Close the DB row deterministically. OnCallEnded is idempotent; a
	// peer-initiated hangup arriving later is a safe no-op.
	if err := h.tracker.OnCallEnded(r.Context(), call.Caller, call.Callee); err != nil {
		slog.ErrorContext(r.Context(), "force-disconnect OnCallEnded failed", "call_id", callID, "err", err)
		// Phones are hung up regardless; the status transition will happen
		// when the next peer hangup arrives or during the daily cleanup.
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

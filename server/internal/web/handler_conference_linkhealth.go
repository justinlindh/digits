package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/version"
)

// ConferenceLinkHealthEdge is the per-directed-edge section of
// ConferenceLinkHealthResp. For a 3-way conference there are always 6 edges
// (each of 3 members reports 2 remote peers).
type ConferenceLinkHealthEdge struct {
	From   string             `json:"from"`
	Peer   string             `json:"peer"`
	Latest *LinkHealthSample  `json:"latest,omitempty"`
	Window []LinkHealthSample `json:"window"`
}

// ConferenceMemberInfo is the per-member section of ConferenceLinkHealthResp.
type ConferenceMemberInfo struct {
	Number      string `json:"number"`
	DisplayName string `json:"display_name"`
	IsHost      bool   `json:"is_host"`
}

// ConferenceLinkHealthResp is the top-level response body for
// GET /api/conference/{uuid}/link-health.
type ConferenceLinkHealthResp struct {
	ConfID    uuid.UUID                  `json:"conf_id"`
	CreatedAt time.Time                  `json:"created_at"`
	Ended     bool                       `json:"ended"`
	Members   []ConferenceMemberInfo     `json:"members"`
	Edges     []ConferenceLinkHealthEdge `json:"edges"`
}

func (h *Handler) handleConferenceLinkHealth(w http.ResponseWriter, r *http.Request) {
	confID, err := uuid.Parse(r.PathValue("uuid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	conf, ownedLines, primaryHH, ok := h.requireConferenceOwnership(w, r, confID)
	if !ok {
		return
	}

	var linkedIndex map[string]string
	if primaryHH != "" {
		linkedIndex = buildLinkedLineIndex(h.buildLinkedFamilies(r.Context(), primaryHH))
	}

	resp := h.buildConferenceLinkHealthResp(r.Context(), conf, ownedLines, linkedIndex)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("conference_link_health encode failed", "conf_id", confID, "err", err)
	}
}

// buildConferenceLinkHealthResp builds the response in-memory (or via DB
// readback for an ended conference). Always emits all N*(N-1) edges,
// even if an edge has no samples yet.
func (h *Handler) buildConferenceLinkHealthResp(ctx context.Context, conf *calls.ConferenceSummary, ownedLines map[string]*line.Line, linkedIndex map[string]string) ConferenceLinkHealthResp {
	members := make([]ConferenceMemberInfo, 0, len(conf.Members))
	for _, m := range conf.Members {
		members = append(members, ConferenceMemberInfo{
			Number:      m,
			DisplayName: resolveMemberDisplayName(m, ownedLines, linkedIndex),
			IsHost:      m == conf.Host,
		})
	}

	edges := make([]ConferenceLinkHealthEdge, 0, len(conf.Members)*(len(conf.Members)-1))
	for _, from := range conf.Members {
		for _, peer := range conf.Members {
			if from == peer {
				continue
			}
			edges = append(edges, h.buildConferenceLinkHealthEdge(ctx, conf.ID, from, peer))
		}
	}
	return ConferenceLinkHealthResp{
		ConfID:    conf.ID,
		CreatedAt: conf.CreatedAt,
		Ended:     conf.EndedAt != nil,
		Members:   members,
		Edges:     edges,
	}
}

// lastSample returns a pointer to the last element of window, or nil if empty.
// Copies the value to avoid aliasing the slice backing array.
func lastSample(window []LinkHealthSample) *LinkHealthSample {
	if len(window) == 0 {
		return nil
	}
	s := window[len(window)-1]
	return &s
}

func (h *Handler) buildConferenceLinkHealthEdge(ctx context.Context, confID uuid.UUID, from, peer string) ConferenceLinkHealthEdge {
	out := ConferenceLinkHealthEdge{From: from, Peer: peer, Window: []LinkHealthSample{}}

	// Memory first.
	windowMem := h.healthStore.WindowEdge(confID, from, peer)
	if len(windowMem) > 0 {
		out.Window = make([]LinkHealthSample, len(windowMem))
		for i, s := range windowMem {
			out.Window[i] = toAPISample(s)
		}
		out.Latest = lastSample(out.Window)
		return out
	}
	// DB fallback.
	dbSamples, err := h.healthStore.ReadbackEdge(ctx, confID, from, peer, calls.RingCapacity)
	if err != nil {
		slog.Warn("ReadbackEdge failed; serving empty window",
			"conf_id", confID, "from", from, "peer", peer, "err", err)
		return out
	}
	out.Window = make([]LinkHealthSample, len(dbSamples))
	for i, s := range dbSamples {
		out.Window[i] = toAPISample(s)
	}
	out.Latest = lastSample(out.Window)
	return out
}

// handleConferenceLinkHealthStream opens an SSE stream for a conference's
// per-edge telemetry. Delivers:
//   - one initial "sample" event with the full 6-edge matrix snapshot
//   - one "sample" event per future per-edge Record
//   - one "ended" event when the conference ends, then closes
//   - periodic "heartbeat" events for client-side liveness
//
// Auth: same as the JSON endpoint (requireConferenceOwnership). Ended
// conferences return 404 before any stream bytes are written.
func (h *Handler) handleConferenceLinkHealthStream(w http.ResponseWriter, r *http.Request) {
	confID, err := uuid.Parse(r.PathValue("uuid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	conf, ownedLines, primaryHH, ok := h.requireConferenceOwnership(w, r, confID)
	if !ok {
		return
	}
	if conf.EndedAt != nil {
		http.NotFound(w, r)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("SSE conference stream: ResponseWriter does not implement Flusher")
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var linkedIndex map[string]string
	if primaryHH != "" {
		linkedIndex = buildLinkedLineIndex(h.buildLinkedFamilies(r.Context(), primaryHH))
	}

	// Subscribe FIRST so samples arriving between the initial snapshot and
	// the select loop are buffered rather than silently dropped.
	sub := h.healthStore.SubscribeConference(confID)
	defer sub.Close()

	// Initial snapshot.
	snapshot := h.buildConferenceLinkHealthResp(r.Context(), conf, ownedLines, linkedIndex)
	fragment, err := h.renderConferenceLinkHealthPanel(snapshot)
	if err != nil {
		slog.Error("SSE conference stream: initial render failed", "conf_id", confID, "err", err)
		return
	}
	if werr := writeSSE(w, "sample", fragment); werr != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, okEv := <-sub.C:
			if !okEv {
				_ = writeSSE(w, "ended", renderEndedConferenceFragment(""))
				flusher.Flush()
				return
			}
			if err := h.writeConferenceEvent(r.Context(), w, flusher, conf, ownedLines, linkedIndex, ev); err != nil {
				slog.Debug("SSE conference stream: write failed", "conf_id", confID, "err", err)
				return
			}
		case <-heartbeat.C:
			if err := writeSSE(w, "heartbeat", "{}"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *Handler) writeConferenceEvent(ctx context.Context, w io.Writer, flusher http.Flusher, conf *calls.ConferenceSummary, ownedLines map[string]*line.Line, linkedIndex map[string]string, ev calls.Event) error {
	switch ev.Kind {
	case calls.SampleKind:
		snapshot := h.buildConferenceLinkHealthResp(ctx, conf, ownedLines, linkedIndex)
		fragment, err := h.renderConferenceLinkHealthPanel(snapshot)
		if err != nil {
			return err
		}
		if err := writeSSE(w, "sample", fragment); err != nil {
			return err
		}
	case calls.EndedKind:
		if err := writeSSE(w, "ended", renderEndedConferenceFragment("")); err != nil {
			return err
		}
	case calls.DisconnectKind:
		if err := writeSSE(w, "disconnect", renderEndedConferenceFragment(ev.EndedBy)); err != nil {
			return err
		}
	}
	flusher.Flush()
	return nil
}

func (h *Handler) renderConferenceLinkHealthPanel(resp ConferenceLinkHealthResp) (string, error) {
	var buf bytes.Buffer
	if err := h.tmplConferenceLivePanel.ExecuteTemplate(&buf, "conference-live-panel", resp); err != nil {
		return "", fmt.Errorf("render conference-live-panel: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// conferenceLiveDetailData is the render payload for conference-live-detail.html.
type conferenceLiveDetailData struct {
	Page               string
	Version            string
	User               *auth.User
	HouseholdName      string
	CallHistoryEnabled bool
	Resp               ConferenceLinkHealthResp
}

// handleConferenceLiveDetail renders the observation deck for a conference.
// Ended conferences render in terminal state (no SSE wiring, no kick button).
func (h *Handler) handleConferenceLiveDetail(w http.ResponseWriter, r *http.Request) {
	confID, err := uuid.Parse(r.PathValue("uuid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	conf, ownedLines, primaryHH, ok := h.requireConferenceOwnership(w, r, confID)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())

	var linkedIndex map[string]string
	if primaryHH != "" {
		linkedIndex = buildLinkedLineIndex(h.buildLinkedFamilies(r.Context(), primaryHH))
	}
	resp := h.buildConferenceLinkHealthResp(r.Context(), conf, ownedLines, linkedIndex)

	data := conferenceLiveDetailData{
		Page:               "conference-live",
		Version:            version.Version,
		User:               user,
		HouseholdName:      h.householdNameFromContext(r),
		CallHistoryEnabled: h.callHistoryEnabled(r),
		Resp:               resp,
	}
	renderWith(w, h.tmplConferenceLiveDetail, layoutFor(r), data)
}

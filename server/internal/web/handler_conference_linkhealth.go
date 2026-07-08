package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
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
	DurationS int                        `json:"duration_s"`
	Members   []ConferenceMemberInfo     `json:"members"`
	Edges     []ConferenceLinkHealthEdge `json:"edges"`
}

func (h *Handler) handleConferenceLinkHealth(w http.ResponseWriter, r *http.Request) {
	confID, ok := parseConfID(w, r)
	if !ok {
		return
	}
	conf, ownedLines, primaryHH, ok := h.requireConferenceOwnership(w, r, confID)
	if !ok {
		return
	}

	nr := nameResolver{ownedLines: ownedLines, linkedIndex: h.linkedIndexForHousehold(r.Context(), primaryHH)}

	resp := h.buildConferenceLinkHealthResp(r.Context(), conf, nr)

	if err := writeJSON(w, resp); err != nil {
		slog.ErrorContext(r.Context(), "conference_link_health encode failed", "conf_id", confID, "err", err)
	}
}

// buildConferenceLinkHealthResp builds the response in-memory (or via DB
// readback for an ended conference). Always emits all N*(N-1) edges,
// even if an edge has no samples yet.
func (h *Handler) buildConferenceLinkHealthResp(ctx context.Context, conf *calls.ConferenceSummary, nr nameResolver) ConferenceLinkHealthResp {
	members := make([]ConferenceMemberInfo, 0, len(conf.Members))
	for _, m := range conf.Members {
		members = append(members, ConferenceMemberInfo{
			Number:      m,
			DisplayName: nr.display(m),
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
		DurationS: conf.DurationS,
		Members:   members,
		Edges:     edges,
	}
}

func (h *Handler) buildConferenceLinkHealthEdge(ctx context.Context, confID uuid.UUID, from, peer string) ConferenceLinkHealthEdge {
	out := ConferenceLinkHealthEdge{From: from, Peer: peer, Window: []LinkHealthSample{}}

	window, latest, err := resolveWindow(h.healthStore.WindowEdge(confID, from, peer), func() ([]calls.Sample, error) {
		return h.healthStore.ReadbackEdge(ctx, confID, from, peer)
	})
	if err != nil {
		slog.WarnContext(ctx, "ReadbackEdge failed; serving empty window",
			"conf_id", confID, "from", from, "peer", peer, "err", err)
		return out
	}
	out.Window, out.Latest = window, latest
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
	confID, ok := parseConfID(w, r)
	if !ok {
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

	flusher, ok := startSSE(w, r)
	if !ok {
		return
	}

	nr := nameResolver{ownedLines: ownedLines, linkedIndex: h.linkedIndexForHousehold(r.Context(), primaryHH)}

	// Subscribe FIRST so samples arriving between the initial snapshot and
	// the select loop are buffered rather than silently dropped.
	sub := h.healthStore.SubscribeConference(confID)
	defer sub.Close()

	// Re-check liveness AFTER subscribing. SubscribeConference lazily
	// creates the session, so if the conference ended on another pod
	// between the ownership check and the Subscribe, the cross-pod evict
	// has already passed and this session would never receive EndedKind.
	if cur, err := h.tracker.GetConferenceByID(r.Context(), confID); err == nil && cur != nil && cur.EndedAt != nil {
		_ = writeSSE(w, sseEventEnded, renderEndedConferenceFragment(""))
		flusher.Flush()
		return
	}

	// Initial snapshot.
	snapshot := h.buildConferenceLinkHealthResp(r.Context(), conf, nr)
	fragment, err := h.renderConferenceLinkHealthPanel(snapshot)
	if err != nil {
		slog.ErrorContext(r.Context(), "SSE conference stream: initial render failed", "conf_id", confID, "err", err)
		return
	}
	if werr := writeSSE(w, sseEventSample, fragment); werr != nil {
		return
	}
	flusher.Flush()

	streamSSE(r.Context(), w, flusher, sub, renderEndedConferenceFragment(""), func(ev calls.Event) error {
		if err := h.writeConferenceEvent(r.Context(), w, flusher, conf, nr, ev); err != nil {
			slog.DebugContext(r.Context(), "SSE conference stream: write failed", "conf_id", confID, "err", err)
			return err
		}
		return nil
	})
}

func (h *Handler) writeConferenceEvent(ctx context.Context, w io.Writer, flusher http.Flusher, conf *calls.ConferenceSummary, nr nameResolver, ev calls.Event) error {
	if handled, err := writeTerminalEvent(w, flusher, ev, renderEndedConferenceFragment); handled {
		return err
	}
	// SampleKind
	snapshot := h.buildConferenceLinkHealthResp(ctx, conf, nr)
	fragment, err := h.renderConferenceLinkHealthPanel(snapshot)
	if err != nil {
		return err
	}
	if err := writeSSE(w, sseEventSample, fragment); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (h *Handler) renderConferenceLinkHealthPanel(resp ConferenceLinkHealthResp) (string, error) {
	return renderFragment(h.tmplConferenceLivePanel, "conference-live-panel", resp)
}

// conferenceLiveDetailData is the render payload for conference-live-detail.html.
type conferenceLiveDetailData struct {
	chromeData
	Resp            ConferenceLinkHealthResp
	IsHostHousehold bool
}

// handleConferenceLiveDetail renders the observation deck for a conference.
// Ended conferences render in terminal state (no SSE wiring, no kick button).
func (h *Handler) handleConferenceLiveDetail(w http.ResponseWriter, r *http.Request) {
	confID, ok := parseConfID(w, r)
	if !ok {
		return
	}
	conf, ownedLines, primaryHH, ok := h.requireConferenceOwnership(w, r, confID)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())

	nr := nameResolver{ownedLines: ownedLines, linkedIndex: h.linkedIndexForHousehold(r.Context(), primaryHH)}
	resp := h.buildConferenceLinkHealthResp(r.Context(), conf, nr)

	_, isHostHH := ownedLines[conf.Host]
	data := conferenceLiveDetailData{
		chromeData:      newChromeData("conference-live", user, primaryHH),
		Resp:            resp,
		IsHostHousehold: isHostHH,
	}
	renderWith(r.Context(), w, h.tmplConferenceLiveDetail, layoutFor(r), data)
}

// handleConferenceKick force-ends a conference on behalf of the host
// household. Writes an audit row, notifies the kicked phone via
// TypeConferenceEnd, drops the member via Relay.KickMember (which
// cascades to the remaining pair), and fans out a DisconnectKind event
// to observer SSE streams.
func (h *Handler) handleConferenceKick(w http.ResponseWriter, r *http.Request) {
	confID, ok := parseConfID(w, r)
	if !ok {
		return
	}
	conf, _, _, ok := h.requireConferenceHostOwnership(w, r, confID)
	if !ok {
		return
	}
	if conf.EndedAt != nil {
		http.NotFound(w, r)
		return
	}

	if !parseForm(w, r) {
		return
	}
	kickedPhone := r.PostForm.Get("phone")
	if kickedPhone == "" {
		http.Error(w, "phone required", http.StatusBadRequest)
		return
	}
	if kickedPhone == conf.Host {
		http.Error(w, "cannot kick the host", http.StatusBadRequest)
		return
	}

	if !slices.Contains(conf.Members, kickedPhone) {
		http.NotFound(w, r)
		return
	}

	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.NotFound(w, r)
		return
	}

	// Audit first: if downstream teardown fails, the record still lands.
	if err := h.tracker.RecordKick(r.Context(), confID, kickedPhone, user.ID); err != nil {
		slog.ErrorContext(r.Context(), "conference_kick: audit write failed", "conf_id", confID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	reason := fmt.Sprintf("kicked by %s", userDisplayLabel(user))
	h.relay.KickMember(r.Context(), confID, kickedPhone, reason)

	// Fan out the actor's label so observer SSE decks show the terminal
	// state with attribution before the evict cascade closes the channel.
	h.healthStore.NotifyDisconnectedConference(confID, userDisplayLabel(user))

	writeEmptyJSON(w)
}

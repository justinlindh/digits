package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/line"
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

	// Display-name resolution via linked-families index (same as 2-party).
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
			DisplayName: h.resolveMemberDisplayName(m, ownedLines, linkedIndex),
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

func (h *Handler) buildConferenceLinkHealthEdge(ctx context.Context, confID uuid.UUID, from, peer string) ConferenceLinkHealthEdge {
	out := ConferenceLinkHealthEdge{From: from, Peer: peer, Window: []LinkHealthSample{}}

	// Memory first.
	windowMem := h.healthStore.WindowEdge(confID, from, peer)
	if len(windowMem) > 0 {
		out.Window = make([]LinkHealthSample, len(windowMem))
		for i, s := range windowMem {
			out.Window[i] = toAPISample(s)
		}
		la := toAPISample(windowMem[len(windowMem)-1])
		out.Latest = &la
		return out
	}
	// DB fallback.
	dbSamples, err := h.healthStore.ReadbackEdge(ctx, confID, from, peer, 60)
	if err != nil {
		slog.Warn("ReadbackEdge failed; serving empty window",
			"conf_id", confID, "from", from, "peer", peer, "err", err)
		return out
	}
	out.Window = make([]LinkHealthSample, len(dbSamples))
	for i, s := range dbSamples {
		out.Window[i] = toAPISample(s)
	}
	if len(dbSamples) > 0 {
		la := toAPISample(dbSamples[len(dbSamples)-1])
		out.Latest = &la
	}
	return out
}

// resolveMemberDisplayName picks the best label for a member phone.
// Priority: owned-line name (only if non-empty), linked-index peer name,
// bare number fallback. The non-empty guard on the owned line is an
// intentional tightening over the 2-party inline behavior: a blank line
// name should not preempt a useful linked-family name.
func (h *Handler) resolveMemberDisplayName(number string, ownedLines map[string]*line.Line, linkedIndex map[string]string) string {
	if ln, ok := ownedLines[number]; ok && ln != nil && ln.Name != "" {
		return ln.Name
	}
	if name := resolvePeerName(number, linkedIndex); name != "" {
		return name
	}
	return number
}

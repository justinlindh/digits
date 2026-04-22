package calls

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/dbutil"
)

// HistoryEntryKind discriminates between 2-party calls and conferences.
type HistoryEntryKind int

const (
	HistoryEntryCall       HistoryEntryKind = iota
	HistoryEntryConference HistoryEntryKind = iota
)

// HistoryEntry is a unified view of either a 2-party call or a 3-way
// conference. Exactly one of Call or Conference is non-nil.
type HistoryEntry struct {
	Kind       HistoryEntryKind
	Call       *Call
	Conference *ConferenceSummary
	// SortTime is the canonical ordering timestamp: StartedAt for a Call,
	// CreatedAt for a Conference.
	SortTime time.Time
}

// IsConference returns true when the entry represents a conference call.
// Used as a template helper for conditional rendering.
func (e HistoryEntry) IsConference() bool {
	return e.Kind == HistoryEntryConference
}

// ConferenceSummary is a read-only flat view of a past conference
// suitable for rendering in call history.
type ConferenceSummary struct {
	ID                uuid.UUID
	Host              string
	Members           []string // all phones: host first, then added in stable order
	CreatedAt         time.Time
	EndedAt           *time.Time
	DurationS         int
	EndReason         string
	OriginatingCallID int64
}

// RecentHistoryForPhones returns a unified history of 2-party calls and
// conferences involving any of the given phone numbers, most recent first,
// capped at limit total entries. Calls that were merged into a conference
// (end_reason = 'merged_to_conference') are excluded — they are represented
// by the conference entry instead.
func (t *Tracker) RecentHistoryForPhones(phones []string, limit int) ([]HistoryEntry, error) {
	if len(phones) == 0 {
		return nil, nil
	}

	calls, err := t.recentCallsForHistory(phones, limit)
	if err != nil {
		return nil, fmt.Errorf("recent calls: %w", err)
	}

	confs, err := t.recentConferencesForHistory(phones, limit)
	if err != nil {
		return nil, fmt.Errorf("recent conferences: %w", err)
	}

	// Merge into a single slice sorted by SortTime descending.
	entries := make([]HistoryEntry, 0, len(calls)+len(confs))
	for i := range calls {
		entries = append(entries, HistoryEntry{
			Kind:     HistoryEntryCall,
			Call:     &calls[i],
			SortTime: calls[i].StartedAt,
		})
	}
	for i := range confs {
		entries = append(entries, HistoryEntry{
			Kind:       HistoryEntryConference,
			Conference: &confs[i],
			SortTime:   confs[i].CreatedAt,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SortTime.After(entries[j].SortTime)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// recentCallsForHistory returns calls for the given phones, excluding rows
// that were merged into a conference.
func (t *Tracker) recentCallsForHistory(phones []string, limit int) ([]Call, error) {
	n := len(phones)
	ph := dbutil.Placeholders(n, 0)
	query := fmt.Sprintf(
		`SELECT id, caller, callee, status, started_at, answered_at, ended_at, duration_s,
		        end_reason, originating_conference_id
		 FROM calls
		 WHERE (caller IN (%s) OR callee IN (%s))
		   AND (end_reason IS NULL OR end_reason != 'merged_to_conference')
		 ORDER BY started_at DESC LIMIT $%d`,
		ph, ph, n+1,
	)
	args := make([]interface{}, 0, n+1)
	for _, p := range phones {
		args = append(args, p)
	}
	args = append(args, limit)

	rows, err := t.db.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var calls []Call
	for rows.Next() {
		var c Call
		if err := rows.Scan(&c.ID, &c.Caller, &c.Callee, &c.Status,
			&c.StartedAt, &c.AnsweredAt, &c.EndedAt, &c.DurationS,
			&c.EndReason, &c.OriginatingConferenceID); err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}

// recentConferencesForHistory returns conferences where any of the phones is
// a member, including the ordered member list for each conference.
func (t *Tracker) recentConferencesForHistory(phones []string, limit int) ([]ConferenceSummary, error) {
	// One query: join conferences + conference_members, aggregate member list.
	// host_phone always sorts first via CASE, remaining members sort alphabetically
	// so ordering is stable across queries.
	// $1 = phone array, $2 = limit
	const query = `
		SELECT c.id, c.host_phone, c.originating_call_id, c.created_at, c.ended_at,
		       c.end_reason,
		       COALESCE(EXTRACT(EPOCH FROM (c.ended_at - c.created_at))::INT, 0) AS duration_s,
		       array_agg(m.phone ORDER BY CASE WHEN m.phone = c.host_phone THEN 0 ELSE 1 END, m.phone) AS members
		FROM conferences c
		JOIN conference_members m ON m.conference_id = c.id
		WHERE EXISTS (
		  SELECT 1 FROM conference_members cm
		  WHERE cm.conference_id = c.id AND cm.phone = ANY($1)
		)
		GROUP BY c.id, c.host_phone, c.originating_call_id, c.created_at, c.ended_at, c.end_reason
		ORDER BY c.created_at DESC
		LIMIT $2`
	args := []interface{}{pq.Array(phones), limit}

	rows, err := t.db.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var confs []ConferenceSummary
	for rows.Next() {
		var cs ConferenceSummary
		var endReason *string
		var members pq.StringArray
		if err := rows.Scan(
			&cs.ID, &cs.Host, &cs.OriginatingCallID, &cs.CreatedAt, &cs.EndedAt,
			&endReason, &cs.DurationS, &members,
		); err != nil {
			return nil, err
		}
		if endReason != nil {
			cs.EndReason = *endReason
		}
		cs.Members = []string(members)
		confs = append(confs, cs)
	}
	return confs, rows.Err()
}

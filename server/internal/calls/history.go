package calls

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/dbutil"
)

// HistoryEntryKind discriminates between 2-party calls and conferences.
type HistoryEntryKind int

const (
	HistoryEntryCall HistoryEntryKind = iota
	HistoryEntryConference
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

// RecentHistoryForPhones returns a unified history page of 2-party calls and
// conferences involving any of the given phone numbers, most recent first.
// When cursor is non-nil, only entries strictly older than the cursor (in the
// merged timeline order) are returned. The function fetches limit+1 rows from
// each underlying subquery and returns up to limit+1 entries. Callers use the
// extra entry to detect whether an older page exists.
func (t *Tracker) RecentHistoryForPhones(ctx context.Context, phones []string, cursor *HistoryCursor, limit int) ([]HistoryEntry, error) {
	if len(phones) == 0 {
		return nil, nil
	}

	calls, err := t.recentCallsForHistory(ctx, phones, cursor, limit+1)
	if err != nil {
		return nil, fmt.Errorf("recent calls: %w", err)
	}

	confs, err := t.recentConferencesForHistory(ctx, phones, cursor, limit+1)
	if err != nil {
		return nil, fmt.Errorf("recent conferences: %w", err)
	}

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
	// Tie-break must mirror the SQL ORDER BY in each subquery so an OlderCursor
	// built from this slice paginates without gaps or duplicates.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SortTime.Equal(entries[j].SortTime) {
			if entries[i].Kind != entries[j].Kind {
				return entries[i].Kind == HistoryEntryCall
			}
			if entries[i].Kind == HistoryEntryCall {
				return entries[i].Call.ID > entries[j].Call.ID
			}
			return entries[i].Conference.ID.String() > entries[j].Conference.ID.String()
		}
		return entries[i].SortTime.After(entries[j].SortTime)
	})
	if len(entries) > limit+1 {
		entries = entries[:limit+1]
	}
	return entries, nil
}

// recentCallsForHistory returns calls for the given phones, excluding rows
// that were merged into a conference.
func (t *Tracker) recentCallsForHistory(ctx context.Context, phones []string, cursor *HistoryCursor, limit int) ([]Call, error) {
	n := len(phones)
	ph := dbutil.Placeholders(n)

	args := make([]any, 0, n+3)
	for _, p := range phones {
		args = append(args, p)
	}

	// Conference cursor uses strict < because Call sorts before Conference at
	// equal time in the in-memory merge, so the same-time call is already on
	// the prior page and must not be emitted again.
	cursorSQL := ""
	if cursor != nil {
		switch cursor.Kind {
		case HistoryEntryCall:
			cid, err := strconv.ParseInt(cursor.ID, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("calls cursor id: %w", err)
			}
			args = append(args, cursor.Time)
			tIdx := len(args)
			args = append(args, cid)
			idIdx := len(args)
			cursorSQL = fmt.Sprintf(" AND (started_at < $%d OR (started_at = $%d AND id < $%d))", tIdx, tIdx, idIdx)
		case HistoryEntryConference:
			args = append(args, cursor.Time)
			cursorSQL = fmt.Sprintf(" AND started_at < $%d", len(args))
		}
	}

	args = append(args, limit)
	limitIdx := len(args)

	query := fmt.Sprintf(
		`SELECT `+callColumns+`
		 FROM calls
		 WHERE (caller IN (%s) OR callee IN (%s))
		   AND (end_reason IS NULL OR end_reason != 'merged_to_conference')%s
		 ORDER BY started_at DESC, id DESC LIMIT $%d`,
		ph, ph, cursorSQL, limitIdx,
	)

	rows, err := t.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recent calls for history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanCallRows(rows)
}

// recentConferencesForHistory returns conferences where any of the phones is
// a member, including the ordered member list for each conference.
func (t *Tracker) recentConferencesForHistory(ctx context.Context, phones []string, cursor *HistoryCursor, limit int) ([]ConferenceSummary, error) {
	args := []any{pq.Array(phones)}

	// Call cursor uses <= because Conference sorts after Call at equal time in
	// the in-memory merge, so the same-time conference is the next entry on the
	// older page and must be included.
	cursorSQL := ""
	if cursor != nil {
		switch cursor.Kind {
		case HistoryEntryCall:
			args = append(args, cursor.Time)
			cursorSQL = fmt.Sprintf(" AND c.created_at <= $%d", len(args))
		case HistoryEntryConference:
			args = append(args, cursor.Time)
			tIdx := len(args)
			args = append(args, cursor.ID)
			idIdx := len(args)
			cursorSQL = fmt.Sprintf(" AND (c.created_at < $%d OR (c.created_at = $%d AND c.id < $%d::uuid))", tIdx, tIdx, idIdx)
		}
	}

	args = append(args, limit)
	limitIdx := len(args)

	query := fmt.Sprintf(`
		SELECT %s
		FROM conferences c
		JOIN conference_members m ON m.conference_id = c.id
		WHERE EXISTS (
		  SELECT 1 FROM conference_members cm
		  WHERE cm.conference_id = c.id AND cm.phone = ANY($1)
		)%s
		GROUP BY %s
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $%d`, conferenceSummaryColumns, cursorSQL, conferenceSummaryGroupBy, limitIdx)

	rows, err := t.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recent conferences for history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var confs []ConferenceSummary
	for rows.Next() {
		cs, err := scanConferenceSummary(rows)
		if err != nil {
			return nil, err
		}
		confs = append(confs, cs)
	}
	return confs, rows.Err()
}

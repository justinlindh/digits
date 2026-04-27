package calls

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind codes used in the encoded cursor token. Single characters keep the
// token short; the values are part of the URL contract and must not change
// once tokens are in the wild.
const (
	kindCodeCall = "c"
	kindCodeConf = "f"
)

// HistoryCursor identifies a position in the merged call+conference timeline.
// Time + Kind + ID together break ties when a call and a conference share a
// microsecond timestamp.
type HistoryCursor struct {
	Time time.Time
	Kind HistoryEntryKind
	ID   string // call ID stringified, or conference UUID string
}

// Encode produces a URL-safe opaque token suitable for the ?before= query
// param. Format: base64url(rfc3339nano | kind | id).
func (c HistoryCursor) Encode() string {
	return base64URL(fmt.Sprintf("%s|%s|%s",
		c.Time.UTC().Format(time.RFC3339Nano),
		kindCode(c.Kind),
		c.ID,
	))
}

// DecodeHistoryCursor parses a token produced by HistoryCursor.Encode. The
// empty string returns (nil, nil) to mean "no cursor".
func DecodeHistoryCursor(s string) (*HistoryCursor, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("cursor base64: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) != 3 {
		return nil, errors.New("cursor: expected 3 fields")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, fmt.Errorf("cursor time: %w", err)
	}
	k, err := parseKindCode(parts[1])
	if err != nil {
		return nil, err
	}
	if parts[2] == "" {
		return nil, errors.New("cursor: empty id")
	}
	return &HistoryCursor{Time: t, Kind: k, ID: parts[2]}, nil
}

// CursorForEntry builds a HistoryCursor pointing at the given entry's
// position in the merged timeline.
func CursorForEntry(e HistoryEntry) HistoryCursor {
	c := HistoryCursor{Time: e.SortTime, Kind: e.Kind}
	switch e.Kind {
	case HistoryEntryCall:
		c.ID = fmt.Sprintf("%d", e.Call.ID)
	case HistoryEntryConference:
		c.ID = e.Conference.ID.String()
	}
	return c
}

func kindCode(k HistoryEntryKind) string {
	switch k {
	case HistoryEntryCall:
		return kindCodeCall
	case HistoryEntryConference:
		return kindCodeConf
	default:
		panic(fmt.Sprintf("calls: unknown HistoryEntryKind %v", k))
	}
}

func parseKindCode(s string) (HistoryEntryKind, error) {
	switch s {
	case kindCodeCall:
		return HistoryEntryCall, nil
	case kindCodeConf:
		return HistoryEntryConference, nil
	default:
		return 0, fmt.Errorf("cursor: unknown kind %q", s)
	}
}

func base64URL(s string) string {
	return base64.URLEncoding.EncodeToString([]byte(s))
}

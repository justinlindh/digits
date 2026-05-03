package calls

import (
	"testing"
	"time"
)

func TestHistoryCursor_RoundTripCall(t *testing.T) {
	want := HistoryCursor{
		Time: time.Date(2026, 4, 27, 15, 4, 5, 123_456_789, time.UTC),
		Kind: HistoryEntryCall,
		ID:   "42",
	}
	tok := want.Encode()
	if tok == "" {
		t.Fatal("Encode returned empty token")
	}
	got, err := DecodeHistoryCursor(tok)
	if err != nil {
		t.Fatalf("DecodeHistoryCursor: %v", err)
	}
	if got == nil {
		t.Fatal("decoded cursor is nil")
	}
	if !got.Time.Equal(want.Time) || got.Kind != want.Kind || got.ID != want.ID {
		t.Errorf("round trip mismatch: got %+v, want %+v", *got, want)
	}
}

func TestHistoryCursor_RoundTripConference(t *testing.T) {
	want := HistoryCursor{
		Time: time.Date(2026, 4, 27, 15, 4, 5, 0, time.UTC),
		Kind: HistoryEntryConference,
		ID:   "11111111-2222-3333-4444-555555555555",
	}
	got, err := DecodeHistoryCursor(want.Encode())
	if err != nil {
		t.Fatalf("DecodeHistoryCursor: %v", err)
	}
	if got.Kind != HistoryEntryConference || got.ID != want.ID {
		t.Errorf("round trip mismatch: %+v", *got)
	}
}

func TestDecodeHistoryCursor_Empty(t *testing.T) {
	c, err := DecodeHistoryCursor("")
	if err != nil {
		t.Fatalf("empty cursor: unexpected error %v", err)
	}
	if c != nil {
		t.Errorf("empty cursor: expected nil, got %+v", *c)
	}
}

func TestDecodeHistoryCursor_BadBase64(t *testing.T) {
	if _, err := DecodeHistoryCursor("!!!not-base64!!!"); err == nil {
		t.Error("expected error for malformed base64")
	}
}

func TestDecodeHistoryCursor_BadFields(t *testing.T) {
	cases := []string{
		"only-one-field",
		"two|fields",
		"badtime|c|1",
		"2026-04-27T00:00:00Z|x|1",
		"2026-04-27T00:00:00Z|c|",
	}
	for _, raw := range cases {
		tok := base64URL(raw)
		if _, err := DecodeHistoryCursor(tok); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}

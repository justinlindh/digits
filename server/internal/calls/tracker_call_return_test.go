//go:build integration

package calls

import (
	"context"
	"testing"
)

func TestLastInboundCaller(t *testing.T) {
	d := setupTestDB(t)
	tracker := New(d)
	ctx := context.Background()

	// No calls yet: should return empty string.
	number, err := tracker.LastInboundCaller(ctx, "3140001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if number != "" {
		t.Fatalf("expected empty, got %q", number)
	}

	// Insert a call from 3140002 -> 3140001 (delivered, rang).
	_, err = tracker.OnCallInitiated(ctx, "3140002", "3140001")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if err := tracker.OnCallEnded(ctx, "3140002", "3140001"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	number, err = tracker.LastInboundCaller(ctx, "3140001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if number != "3140002" {
		t.Fatalf("expected 3140002, got %q", number)
	}

	// Insert a newer call from 3140003 -> 3140001.
	_, err = tracker.OnCallInitiated(ctx, "3140003", "3140001")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if err := tracker.OnCallEnded(ctx, "3140003", "3140001"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	number, err = tracker.LastInboundCaller(ctx, "3140001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if number != "3140003" {
		t.Fatalf("expected 3140003 (most recent), got %q", number)
	}
}

func TestLastInboundCallerExcludesMerged(t *testing.T) {
	d := setupTestDB(t)
	tracker := New(d)
	ctx := context.Background()

	// Insert a call that will be "merged to conference" (excluded).
	callID, err := tracker.OnCallInitiated(ctx, "3140002", "3140001")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	_, err = d.DB.ExecContext(ctx,
		`UPDATE calls SET status = 'ended', end_reason = 'merged_to_conference' WHERE id = $1`, callID)
	if err != nil {
		t.Fatalf("update end_reason: %v", err)
	}
	tracker.ClearByNumber(ctx, "3140002")

	number, err := tracker.LastInboundCaller(ctx, "3140001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if number != "" {
		t.Fatalf("expected empty (merged excluded), got %q", number)
	}
}

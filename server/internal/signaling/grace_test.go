package signaling

import (
	"testing"
	"time"
)

func TestNewRelaySetsDefaultGraceWindow(t *testing.T) {
	r := NewRelay(NewHub(), newMockTracker(), nil, nil)
	if r.GraceWindow != 20*time.Second {
		t.Fatalf("default GraceWindow = %v, want 20s", r.GraceWindow)
	}
	if r.graceTimers == nil {
		t.Fatal("graceTimers map not initialized")
	}
}

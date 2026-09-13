package phone

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/signal"
)

// A connect_timeout hangup in CONNECTED (from the local deadline or the far
// end) tears the call down with the reason and plays the failure
// announcement ahead of the off-hook sequence.
func TestController_ConnectTimeout_AnnouncesThenReorder(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")
	defer c.Close()
	c.setStateForTest(StateCONNECTED)

	c.HandleHangup("3140002", signal.HangupReasonConnectTimeout)

	if c.State() != StateCALL_FAILED {
		t.Fatalf("expected CALL_FAILED, got %s", c.State())
	}
	if got := cb.HangupReasons(); !slices.Equal(got, []string{signal.HangupReasonConnectTimeout}) {
		t.Fatalf("HangupCall reasons = %v, want [%s]", got, signal.HangupReasonConnectTimeout)
	}
	if !waitForTone(cb, ToneReorder) {
		t.Fatalf("reorder never played; tones=%v", cb.Tones())
	}
	tones := cb.Tones()
	announce, reorder := slices.Index(tones, ToneCallFailed), slices.Index(tones, ToneReorder)
	if announce < 0 || reorder < announce {
		t.Fatalf("expected CALL_FAILED announcement before reorder, tones=%v", tones)
	}
}

// A plain remote hangup stays announcement free and carries no reason.
func TestController_PlainRemoteHangup_NoAnnouncement(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")
	defer c.Close()
	c.setStateForTest(StateCONNECTED)

	c.HandleHangup("3140002", "")

	if c.State() != StateREMOTE_HANGUP {
		t.Fatalf("expected REMOTE_HANGUP, got %s", c.State())
	}
	if got := cb.HangupReasons(); !slices.Equal(got, []string{""}) {
		t.Fatalf("HangupCall reasons = %v, want [\"\"]", got)
	}
	if !waitForTone(cb, ToneReorder) {
		t.Fatalf("reorder never played; tones=%v", cb.Tones())
	}
	if cb.tonePlayed(ToneCallFailed) {
		t.Fatalf("plain remote hangup played the failure announcement; tones=%v", cb.Tones())
	}
}

// The off-hook sequence waits for the announcement to finish before reorder
// starts, so the spoken clip is never talked over.
func TestController_ConnectTimeout_WaitsForAnnouncement(t *testing.T) {
	var polls atomic.Int32
	cb := &mockCallbacks{}
	cb.oncePlaying = func() bool { return polls.Add(1) <= 5 }
	c := newTestController(cb, "")
	defer c.Close()
	c.setStateForTest(StateCONNECTED)

	c.HandleHangup("3140002", signal.HangupReasonConnectTimeout)

	if !waitForTone(cb, ToneReorder) {
		t.Fatalf("reorder never played; tones=%v", cb.Tones())
	}
	if polls.Load() < 6 {
		t.Fatalf("reorder started after %d OncePlaying polls, want the announcement polled to completion", polls.Load())
	}
}

// Hanging up during the announcement aborts the sequence: no reorder, no
// second hangup, back to IDLE.
func TestController_ConnectTimeout_HookOnAborts(t *testing.T) {
	cb := &mockCallbacks{}
	cb.oncePlaying = func() bool { return true }
	c := newTestController(cb, "")
	defer c.Close()
	c.setStateForTest(StateCONNECTED)

	c.HandleHangup("3140002", signal.HangupReasonConnectTimeout)
	if !waitForTone(cb, ToneCallFailed) {
		t.Fatalf("announcement never played; tones=%v", cb.Tones())
	}
	c.HandleEvent("HOOK:ON")

	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after hook-on, got %s", c.State())
	}
	time.Sleep(20 * time.Millisecond)
	if cb.tonePlayed(ToneReorder) {
		t.Fatalf("reorder played after hook-on; tones=%v", cb.Tones())
	}
	if got := cb.HangupReasons(); len(got) != 1 {
		t.Fatalf("expected exactly one HangupCall, got %v", got)
	}
}

// The reason rides on the ordinary hangup path, so every other state keeps
// its normal hangup handling: an add-leg that never connected still drops
// to ADD_INTERCEPT with the peer torn down.
func TestController_ConnectTimeout_InAddPrivateTearsDownLeg(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "5550001")
	defer c.Close()
	c.setStateForTest(StateADD_PRIVATE)
	c.setAddingPeerForTest("5550003")

	c.HandleHangup("5550003", signal.HangupReasonConnectTimeout)

	if c.State() != StateADD_INTERCEPT {
		t.Fatalf("expected ADD_INTERCEPT, got %s", c.State())
	}
	if !cb.peerTorndown("5550003") {
		t.Fatal("expected the add-leg peer torn down")
	}
	if cb.tonePlayed(ToneCallFailed) {
		t.Fatalf("add-leg failure played the 2-party announcement; tones=%v", cb.Tones())
	}
}

func TestController_ConnectTimeout_IgnoredInIdleStates(t *testing.T) {
	for _, st := range []State{StateIDLE, StateREMOTE_HANGUP} {
		cb := &mockCallbacks{}
		c := newTestController(cb, "")
		c.setStateForTest(st)
		c.HandleHangup("3140002", signal.HangupReasonConnectTimeout)
		if c.State() != st {
			t.Errorf("state %s: connect_timeout hangup moved to %s", st, c.State())
		}
		if got := cb.HangupReasons(); len(got) != 0 {
			t.Errorf("state %s: connect_timeout hangup hung up %v", st, got)
		}
		c.Close()
	}
}

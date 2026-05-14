package phone

import (
	"testing"
	"time"
)

func TestController_Star69DetectedInDialing(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	if ctrl.State() != StateDIALTONE {
		t.Fatalf("expected DIAL_TONE, got %s", ctrl.State())
	}

	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")

	if ctrl.State() != StateCALL_RETURN {
		t.Fatalf("expected CALL_RETURN, got %s", ctrl.State())
	}

	tones := cb.Tones()
	if len(tones) < 2 {
		t.Fatalf("expected at least 2 tones (DIAL, STOP), got %v", tones)
	}
	if tones[0] != ToneDial || tones[1] != ToneStop {
		t.Fatalf("expected [DIAL, STOP], got %v", tones[:2])
	}
}

func TestController_Star69HangupCancels(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")

	if ctrl.State() != StateCALL_RETURN {
		t.Fatalf("expected CALL_RETURN, got %s", ctrl.State())
	}

	ctrl.HandleEvent("HOOK:ON")
	if ctrl.State() != StateIDLE {
		t.Fatalf("expected IDLE after hangup, got %s", ctrl.State())
	}
}

func TestController_Star69IgnoresNon1Keys(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")

	if ctrl.State() != StateCALL_RETURN {
		t.Fatalf("expected CALL_RETURN, got %s", ctrl.State())
	}

	for _, key := range []string{"0", "2", "3", "4", "5", "6", "7", "8", "9", "*", "#"} {
		ctrl.HandleEvent("KEY:" + key)
		if ctrl.State() != StateCALL_RETURN {
			t.Fatalf("expected CALL_RETURN after KEY:%s, got %s", key, ctrl.State())
		}
	}
}

func TestController_Star69Press1InitiatesCall(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")

	ctrl.SetCallReturnNumber("3140002")

	ctrl.HandleEvent("KEY:1")

	if ctrl.State() != StateCALLING {
		t.Fatalf("expected CALLING, got %s", ctrl.State())
	}

	time.Sleep(1 * time.Second)
	calls := cb.Calls()
	if len(calls) != 1 || calls[0] != "3140002" {
		t.Fatalf("expected call to 3140002, got %v", calls)
	}
}

func TestController_Star69Press1NoNumber(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")

	ctrl.HandleEvent("KEY:1")

	if ctrl.State() != StateCALL_RETURN {
		t.Fatalf("expected CALL_RETURN (no number to dial), got %s", ctrl.State())
	}
}

func TestController_Star69RingIgnored(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")

	ctrl.HandleSignal("ring", "")
	if ctrl.State() != StateCALL_RETURN {
		t.Fatalf("expected CALL_RETURN, got %s", ctrl.State())
	}
	rings := cb.Rings()
	if len(rings) != 0 {
		t.Fatalf("expected no rings, got %v", rings)
	}
}

func TestController_Star69ResetToDialtone(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")

	ctrl.ResetToDialtone()
	if ctrl.State() != StateDIALTONE {
		t.Fatalf("expected DIAL_TONE, got %s", ctrl.State())
	}
}

func TestController_Star69FlashIgnored(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")

	ctrl.HandleHookFlash("")
	if ctrl.State() != StateCALL_RETURN {
		t.Fatalf("expected CALL_RETURN after flash, got %s", ctrl.State())
	}
}

func TestController_Star69IsNotDialPhase(t *testing.T) {
	if StateCALL_RETURN.IsDialPhase() {
		t.Fatal("CALL_RETURN should not be a dial phase")
	}
}

func TestController_CallReturnRing(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleCallReturnRing("3140002")

	if ctrl.State() != StateRINGING {
		t.Fatalf("expected RINGING, got %s", ctrl.State())
	}
	pats := cb.RingPatterns()
	if len(pats) != 1 || pats[0] != 1 {
		t.Fatalf("expected ring pattern [1], got %v", pats)
	}
}

func TestController_CallReturnRingIgnoredWhenBusy(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleCallReturnRing("3140002")

	if ctrl.State() != StateDIALTONE {
		t.Fatalf("expected DIAL_TONE (ring ignored), got %s", ctrl.State())
	}
}

func TestController_CallReturnPickupAutoDials(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleCallReturnRing("3140002")
	if ctrl.State() != StateRINGING {
		t.Fatalf("expected RINGING, got %s", ctrl.State())
	}

	ctrl.HandleEvent("HOOK:OFF")

	if ctrl.State() != StateCALLING {
		t.Fatalf("expected CALLING (auto-dial), got %s", ctrl.State())
	}

	time.Sleep(1 * time.Second)
	calls := cb.Calls()
	if len(calls) != 1 || calls[0] != "3140002" {
		t.Fatalf("expected call to 3140002, got %v", calls)
	}
}

func TestController_CallReturnRingHangup(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleCallReturnRing("3140002")
	ctrl.HandleSignal("hangup", "")

	if ctrl.State() != StateIDLE {
		t.Fatalf("expected IDLE after ring hangup, got %s", ctrl.State())
	}
}

func TestController_Star89DetectedInDialing(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:8")
	ctrl.HandleEvent("KEY:9")

	if ctrl.State() != StateCALL_RETURN {
		t.Fatalf("expected CALL_RETURN (for *89 cancel), got %s", ctrl.State())
	}

	// OnCallReturnCancel is dispatched asynchronously; give it a moment.
	deadline := time.Now().Add(500 * time.Millisecond)
	for cb.CallReturnCancels() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cb.CallReturnCancels() != 1 {
		t.Fatalf("expected 1 cancel callback, got %d", cb.CallReturnCancels())
	}
}

func TestController_Star69AbandonedFiresAbandon(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleEvent("HOOK:OFF")
	ctrl.HandleEvent("KEY:*")
	ctrl.HandleEvent("KEY:6")
	ctrl.HandleEvent("KEY:9")
	if ctrl.State() != StateCALL_RETURN {
		t.Fatalf("expected CALL_RETURN, got %s", ctrl.State())
	}

	ctrl.HandleEvent("HOOK:ON")
	if ctrl.State() != StateIDLE {
		t.Fatalf("expected IDLE after hangup, got %s", ctrl.State())
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for cb.CallReturnAbandons() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cb.CallReturnAbandons() != 1 {
		t.Fatalf("expected 1 abandon callback, got %d", cb.CallReturnAbandons())
	}
}

func TestController_OnHookFromConnectedDoesNotFireAbandon(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	ctrl.HandleSignal("ring", "3140002")
	if ctrl.State() != StateRINGING {
		t.Fatalf("expected RINGING, got %s", ctrl.State())
	}
	ctrl.HandleEvent("HOOK:OFF")
	if ctrl.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED, got %s", ctrl.State())
	}

	ctrl.HandleEvent("HOOK:ON")

	time.Sleep(50 * time.Millisecond)
	if cb.CallReturnAbandons() != 0 {
		t.Fatalf("abandon callback should not fire from CONNECTED, got %d", cb.CallReturnAbandons())
	}
}

// TestController_ResetClearsCallbackRingState is the regression guard for the
// WebSocket-reconnect teardown path. The reconnect code in cmd/digitsd/main.go
// calls ctrl.Reset() to force the FSM back to IDLE; before this fix Reset()
// only cleared state and digits, so the *69 callback-ring fields
// (callReturnRinging / callReturnTarget) survived the teardown. A subsequent
// unrelated incoming call would then auto-dial the stale callback target on
// pickup instead of answering the new caller.
func TestController_ResetClearsCallbackRingState(t *testing.T) {
	cb := &mockCallbacks{}
	ctrl := NewController(cb, "3140001")

	// Server detected the *69 retry target free, so the controller is ringing
	// the requester with the distinctive callback pattern.
	ctrl.HandleCallReturnRing("3140002")
	if ctrl.State() != StateRINGING {
		t.Fatalf("setup: expected RINGING, got %s", ctrl.State())
	}

	// WebSocket drops and reconnects mid-callback-ring. The reconnect code
	// path calls Reset to drop the FSM back to IDLE.
	ctrl.Reset()
	if ctrl.State() != StateIDLE {
		t.Fatalf("expected IDLE after Reset, got %s", ctrl.State())
	}

	// Now an unrelated incoming call rings. Pickup must answer it, not
	// auto-dial the stale 3140002 callback target.
	ctrl.HandleSignal("ring", "3140003")
	if ctrl.State() != StateRINGING {
		t.Fatalf("expected RINGING for unrelated incoming call, got %s", ctrl.State())
	}
	ctrl.HandleEvent("HOOK:OFF")
	if ctrl.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED on pickup, got %s", ctrl.State())
	}

	time.Sleep(50 * time.Millisecond)
	if calls := cb.Calls(); len(calls) != 0 {
		t.Fatalf("expected no auto-dial after Reset cleared callback fields, got %v", calls)
	}
}

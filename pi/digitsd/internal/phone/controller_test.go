package phone

import (
	"testing"
	"time"
)

type mockCallbacks struct {
	tones   []string
	rings   []bool
	leds    []string
	calls   []string
	hangups int
	answers int
}

func (m *mockCallbacks) SendTone(name string)       { m.tones = append(m.tones, name) }
func (m *mockCallbacks) OncePlaying() bool          { return false }
func (m *mockCallbacks) SendRing(start bool)        { m.rings = append(m.rings, start) }
func (m *mockCallbacks) SendLED(mode string)        { m.leds = append(m.leds, mode) }
func (m *mockCallbacks) InitiateCall(number string) { m.calls = append(m.calls, number) }
func (m *mockCallbacks) AnswerCall()                { m.answers++ }
func (m *mockCallbacks) HangupCall()                { m.hangups++ }

// 1. Full outgoing call flow: HOOK:OFF → keys → DIAL:5551234 → answer → HOOK:ON
// waitForCall waits up to 2s for a call to be initiated (async after dial delay).
func waitForCall(cb *mockCallbacks) {
	for i := 0; i < 20; i++ {
		if len(cb.calls) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForTone waits up to 2s for a specific tone to appear.
func waitForTone(cb *mockCallbacks, tone string) {
	for i := 0; i < 20; i++ {
		for _, t := range cb.tones {
			if t == tone {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestController_OutgoingCallFlow(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	// Pick up handset
	c.HandleEvent("HOOK:OFF")
	if c.State() != StateDIALTONE {
		t.Fatalf("expected DIAL_TONE, got %s", c.State())
	}
	if len(cb.tones) == 0 || cb.tones[len(cb.tones)-1] != "DIAL" {
		t.Error("expected SendTone(DIAL) on HOOK:OFF")
	}
	if len(cb.leds) == 0 || cb.leds[len(cb.leds)-1] != "ON" {
		t.Error("expected SendLED(ON) on HOOK:OFF")
	}

	// Press first digit — should stop dial tone and enter DIALING
	c.HandleEvent("KEY:5")
	if c.State() != StateDIALING {
		t.Fatalf("expected DIALING after first key, got %s", c.State())
	}
	tonesSoFar := len(cb.tones)
	if cb.tones[tonesSoFar-1] != "STOP" {
		t.Error("expected SendTone(STOP) on first key")
	}

	// More digits
	c.HandleEvent("KEY:5")
	c.HandleEvent("KEY:5")

	if c.State() != StateDIALING {
		t.Fatalf("expected DIALING after more keys, got %s", c.State())
	}

	// Dial event (number recognized)
	c.HandleEvent("DIAL:5551234")
	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING after DIAL, got %s", c.State())
	}
	waitForCall(cb)
	if len(cb.calls) == 0 || cb.calls[0] != "5551234" {
		t.Errorf("expected InitiateCall(5551234), got %v", cb.calls)
	}
	waitForTone(cb, "RINGBACK")
	lastTone := cb.tones[len(cb.tones)-1]
	if lastTone != "RINGBACK" {
		t.Errorf("expected SendTone(RINGBACK), got %s", lastTone)
	}

	// Remote answers
	c.HandleSignal("answer")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED after answer signal, got %s", c.State())
	}
	lastTone = cb.tones[len(cb.tones)-1]
	if lastTone != "STOP" {
		t.Errorf("expected SendTone(STOP) on answer, got %s", lastTone)
	}

	// Hang up
	c.HandleEvent("HOOK:ON")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after HOOK:ON, got %s", c.State())
	}
	if cb.hangups != 1 {
		t.Errorf("expected 1 HangupCall, got %d", cb.hangups)
	}
	lastLED := cb.leds[len(cb.leds)-1]
	if lastLED != "OFF" {
		t.Errorf("expected SendLED(OFF) on hang up, got %s", lastLED)
	}
}

// 2. Full incoming call flow: ring signal → HOOK:OFF → hangup signal
func TestController_IncomingCallFlow(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	// Incoming ring
	c.HandleSignal("ring")
	if c.State() != StateRINGING {
		t.Fatalf("expected RINGING, got %s", c.State())
	}
	if len(cb.rings) == 0 || cb.rings[0] != true {
		t.Error("expected SendRing(true)")
	}
	if len(cb.leds) == 0 || cb.leds[0] != "BLINK" {
		t.Error("expected SendLED(BLINK)")
	}

	// Pick up
	c.HandleEvent("HOOK:OFF")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED after HOOK:OFF during RINGING, got %s", c.State())
	}
	if cb.answers != 1 {
		t.Errorf("expected 1 AnswerCall, got %d", cb.answers)
	}
	// Ring should have been stopped
	lastRing := cb.rings[len(cb.rings)-1]
	if lastRing != false {
		t.Error("expected SendRing(false) when answering")
	}

	// Remote hangs up — enters REMOTE_HANGUP (off-hook warning sequence)
	c.HandleSignal("hangup")
	if c.State() != StateREMOTE_HANGUP {
		t.Fatalf("expected REMOTE_HANGUP after hangup signal, got %s", c.State())
	}
	// Verify call was torn down
	if cb.hangups != 1 {
		t.Errorf("expected 1 HangupCall, got %d", cb.hangups)
	}

	// User hangs up (on-hook) → IDLE
	c.HandleEvent("HOOK:ON")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after HOOK:ON in REMOTE_HANGUP, got %s", c.State())
	}
	lastLED := cb.leds[len(cb.leds)-1]
	if lastLED != "OFF" {
		t.Errorf("expected SendLED(OFF) after local hangup, got %s", lastLED)
	}
}

// 3. KEY in IDLE is ignored — no state change, no callbacks
func TestController_IgnoreKeyInIdle(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	c.HandleEvent("KEY:5")

	if c.State() != StateIDLE {
		t.Errorf("expected IDLE, got %s", c.State())
	}
	if len(cb.tones) != 0 || len(cb.leds) != 0 || len(cb.calls) != 0 {
		t.Error("expected no callbacks for KEY in IDLE")
	}
}

// 4. Hang up during DIALING → IDLE, no HangupCall
func TestController_HangupDuringDialing(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	c.HandleEvent("HOOK:OFF") // → DIAL_TONE
	c.HandleEvent("KEY:5")    // → DIALING
	c.HandleEvent("HOOK:ON")  // → IDLE

	if c.State() != StateIDLE {
		t.Errorf("expected IDLE, got %s", c.State())
	}
	if cb.hangups != 0 {
		t.Errorf("expected 0 HangupCall during DIALING, got %d", cb.hangups)
	}
}

// 5. Hang up during CALLING → IDLE, HangupCall IS called
func TestController_HangupDuringCalling(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	c.HandleEvent("HOOK:OFF")    // → DIAL_TONE
	c.HandleEvent("KEY:5")       // → DIALING
	c.HandleEvent("DIAL:5551234") // → CALLING
	c.HandleEvent("HOOK:ON")     // → IDLE

	if c.State() != StateIDLE {
		t.Errorf("expected IDLE, got %s", c.State())
	}
	if cb.hangups != 1 {
		t.Errorf("expected 1 HangupCall during CALLING, got %d", cb.hangups)
	}
}

// 6. Busy signal during CALLING → SendTone("STOP"), stays in CALLING or logs
func TestController_BusySignal(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234")

	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING, got %s", c.State())
	}

	tonesBefore := len(cb.tones)
	c.HandleSignal("busy")

	// SendTone("STOP") should have been called
	if len(cb.tones) <= tonesBefore {
		t.Error("expected SendTone to be called on busy")
	}
	lastTone := cb.tones[len(cb.tones)-1]
	if lastTone != "STOP" {
		t.Errorf("expected SendTone(STOP) on busy, got %s", lastTone)
	}
	// No hangup call triggered by signal
	if cb.hangups != 0 {
		t.Errorf("expected 0 HangupCall on busy signal, got %d", cb.hangups)
	}
}

// mockContactChecker implements ContactChecker for testing.
type mockContactChecker struct {
	allowed map[string]bool
}

func (m *mockContactChecker) IsContact(number string) bool {
	return m.allowed[number]
}

// Test: dialing an allowed contact proceeds normally
func TestController_DialAllowedContact(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")
	c.SetContactChecker(&mockContactChecker{allowed: map[string]bool{"5551234": true}})

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234")

	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING, got %s", c.State())
	}
	waitForCall(cb)
	if len(cb.calls) != 1 || cb.calls[0] != "5551234" {
		t.Errorf("expected InitiateCall(5551234), got %v", cb.calls)
	}
}

// Test: dialing a blocked number gets rejection tones, not a call
func TestController_DialBlockedContact(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")
	c.SetContactChecker(&mockContactChecker{allowed: map[string]bool{"5559999": true}})

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234") // not in allowed list

	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING (rejection sequence), got %s", c.State())
	}
	if len(cb.calls) != 0 {
		t.Errorf("expected NO InitiateCall for blocked number, got %v", cb.calls)
	}
	// Should have sent RINGBACK tone (rejection mimics unreachable number)
	found := false
	for _, tone := range cb.tones {
		if tone == "RINGBACK" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected RINGBACK tone for blocked number, got tones: %v", cb.tones)
	}
}

// Test: no ContactChecker set = all calls allowed (backward compat)
func TestController_NoContactChecker_AllowsAll(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")
	// No SetContactChecker call

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234")

	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING, got %s", c.State())
	}
	waitForCall(cb)
	if len(cb.calls) != 1 {
		t.Errorf("expected call to proceed with no checker, got %v", cb.calls)
	}
}

// 7. Self-dial → immediate busy tone, no call initiated
func TestController_SelfDial(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "5551234")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234") // own number

	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING, got %s", c.State())
	}
	if len(cb.calls) != 0 {
		t.Errorf("expected no InitiateCall for self-dial, got %v", cb.calls)
	}
	lastTone := cb.tones[len(cb.tones)-1]
	if lastTone != "BUSY" {
		t.Errorf("expected BUSY tone for self-dial, got %s", lastTone)
	}
}

// 8. Incoming ring signal while CONNECTED → stays CONNECTED, ignored
func TestController_IncomingWhileBusy(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	// Set up CONNECTED state via outgoing call flow
	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234")
	waitForCall(cb)
	c.HandleSignal("answer")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED, got %s", c.State())
	}

	ringsBefore := len(cb.rings)
	c.HandleSignal("ring")

	// State should still be CONNECTED
	if c.State() != StateCONNECTED {
		t.Errorf("expected CONNECTED after ring signal during active call, got %s", c.State())
	}
	// No new ring callbacks
	if len(cb.rings) != ringsBefore {
		t.Error("expected no SendRing call when ring arrives during CONNECTED")
	}
}

package phone

import (
	"sync"
	"testing"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/signal"
)

// mockCallbacks captures controller side-effects for assertions. All methods
// are safe to call from controller-spawned goroutines; tests must read state
// through the accessor methods (Tones, Calls, ...) so reads are synchronized
// against the writers.
type mockCallbacks struct {
	mu                 sync.Mutex
	tones              []string
	rings              []bool
	leds               []string
	calls              []string
	hangups            int
	answers            int
	callConnectedCalls int
	mutedPeers         map[string]bool   // phone -> current mute state
	torndownPeers      []string          // peers that had TearDownPeer called
	mergeRequests      [][2]string       // [held, active] pairs
	meshPeers          map[string]bool   // phone -> initiator flag
	allTorndown        bool              // true if TearDownAllMeshPeers was called
}

func (m *mockCallbacks) SendTone(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tones = append(m.tones, name)
}
func (m *mockCallbacks) OncePlaying() bool { return false }
func (m *mockCallbacks) SendRing(start bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rings = append(m.rings, start)
}
func (m *mockCallbacks) SendLED(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leds = append(m.leds, mode)
}
func (m *mockCallbacks) InitiateCall(number string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, number)
}
func (m *mockCallbacks) AnswerCall() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answers++
}
func (m *mockCallbacks) HangupCall() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hangups++
}
func (m *mockCallbacks) NotifyCallConnected() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callConnectedCalls++
}
func (m *mockCallbacks) MutePeer(phone string, muted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mutedPeers == nil {
		m.mutedPeers = make(map[string]bool)
	}
	m.mutedPeers[phone] = muted
}
func (m *mockCallbacks) TearDownPeer(phone string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.torndownPeers = append(m.torndownPeers, phone)
}
func (m *mockCallbacks) RequestConferenceMerge(held, active string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mergeRequests = append(m.mergeRequests, [2]string{held, active})
}
func (m *mockCallbacks) AddMeshPeer(phone string, initiator bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.meshPeers == nil {
		m.meshPeers = make(map[string]bool)
	}
	m.meshPeers[phone] = initiator
}
func (m *mockCallbacks) RemoveMeshPeer(phone string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.torndownPeers = append(m.torndownPeers, phone)
}
func (m *mockCallbacks) TearDownAllMeshPeers() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allTorndown = true
}

// Snapshot accessors — return copies under lock so test assertions are
// race-free against goroutines started by the controller.
func (m *mockCallbacks) Tones() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.tones...)
}
func (m *mockCallbacks) Rings() []bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]bool(nil), m.rings...)
}
func (m *mockCallbacks) LEDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.leds...)
}
func (m *mockCallbacks) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}
func (m *mockCallbacks) Hangups() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hangups
}
func (m *mockCallbacks) Answers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.answers
}
func (m *mockCallbacks) CallConnectedCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callConnectedCalls
}

// peerMuted returns whether the given peer is currently muted.
func (m *mockCallbacks) peerMuted(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutedPeers[phone]
}

// peerTorndown returns whether TearDownPeer was called for the given phone.
func (m *mockCallbacks) peerTorndown(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.torndownPeers {
		if p == phone {
			return true
		}
	}
	return false
}

// tonePlayed returns whether the given tone name was ever sent.
func (m *mockCallbacks) tonePlayed(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tones {
		if t == name {
			return true
		}
	}
	return false
}

// mergeRequested returns whether RequestConferenceMerge was called with the given held/active pair.
func (m *mockCallbacks) mergeRequested(held, active string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.mergeRequests {
		if r[0] == held && r[1] == active {
			return true
		}
	}
	return false
}

// peerAdded returns whether AddMeshPeer was called for the given phone.
func (m *mockCallbacks) peerAdded(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.meshPeers[phone]
	return ok
}

// peerInitiator returns whether the given phone was added as initiator.
func (m *mockCallbacks) peerInitiator(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.meshPeers[phone]
}

// allPeersTorndown returns whether TearDownAllMeshPeers was called.
func (m *mockCallbacks) allPeersTorndown() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allTorndown
}

// waitForCall waits up to 2s for a call to be initiated (async after dial delay).
func waitForCall(cb *mockCallbacks) {
	for i := 0; i < 20; i++ {
		if len(cb.Calls()) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForTone waits up to 2s for a specific tone to appear. Returns true if
// found, false on timeout.
func waitForTone(cb *mockCallbacks, tone string) bool {
	for i := 0; i < 20; i++ {
		for _, t := range cb.Tones() {
			if t == tone {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestController_OutgoingCallFlow(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	// Pick up handset
	c.HandleEvent("HOOK:OFF")
	if c.State() != StateDIALTONE {
		t.Fatalf("expected DIAL_TONE, got %s", c.State())
	}
	if len(cb.Tones()) == 0 || cb.Tones()[len(cb.Tones())-1] != "DIAL" {
		t.Error("expected SendTone(DIAL) on HOOK:OFF")
	}
	if len(cb.LEDs()) == 0 || cb.LEDs()[len(cb.LEDs())-1] != "ON" {
		t.Error("expected SendLED(ON) on HOOK:OFF")
	}

	// Press first digit — should stop dial tone and enter DIALING
	c.HandleEvent("KEY:5")
	if c.State() != StateDIALING {
		t.Fatalf("expected DIALING after first key, got %s", c.State())
	}
	tonesSoFar := len(cb.Tones())
	if cb.Tones()[tonesSoFar-1] != "STOP" {
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
	if len(cb.Calls()) == 0 || cb.Calls()[0] != "5551234" {
		t.Errorf("expected InitiateCall(5551234), got %v", cb.Calls())
	}
	waitForTone(cb, "RINGBACK")
	lastTone := cb.Tones()[len(cb.Tones())-1]
	if lastTone != "RINGBACK" {
		t.Errorf("expected SendTone(RINGBACK), got %s", lastTone)
	}

	// Remote answers
	c.HandleSignal("answer")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED after answer signal, got %s", c.State())
	}
	lastTone = cb.Tones()[len(cb.Tones())-1]
	if lastTone != "STOP" {
		t.Errorf("expected SendTone(STOP) on answer, got %s", lastTone)
	}

	// Hang up
	c.HandleEvent("HOOK:ON")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after HOOK:ON, got %s", c.State())
	}
	if cb.Hangups() != 1 {
		t.Errorf("expected 1 HangupCall, got %d", cb.Hangups())
	}
	lastLED := cb.LEDs()[len(cb.LEDs())-1]
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
	if len(cb.Rings()) == 0 || cb.Rings()[0] != true {
		t.Error("expected SendRing(true)")
	}
	if len(cb.LEDs()) == 0 || cb.LEDs()[0] != "BLINK" {
		t.Error("expected SendLED(BLINK)")
	}

	// Pick up
	c.HandleEvent("HOOK:OFF")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED after HOOK:OFF during RINGING, got %s", c.State())
	}
	if cb.Answers() != 1 {
		t.Errorf("expected 1 AnswerCall, got %d", cb.Answers())
	}
	// Ring should have been stopped
	lastRing := cb.Rings()[len(cb.Rings())-1]
	if lastRing != false {
		t.Error("expected SendRing(false) when answering")
	}

	// Remote hangs up — enters REMOTE_HANGUP (off-hook warning sequence)
	c.HandleSignal("hangup")
	if c.State() != StateREMOTE_HANGUP {
		t.Fatalf("expected REMOTE_HANGUP after hangup signal, got %s", c.State())
	}
	// Verify call was torn down
	if cb.Hangups() != 1 {
		t.Errorf("expected 1 HangupCall, got %d", cb.Hangups())
	}

	// User hangs up (on-hook) → IDLE
	c.HandleEvent("HOOK:ON")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after HOOK:ON in REMOTE_HANGUP, got %s", c.State())
	}
	lastLED := cb.LEDs()[len(cb.LEDs())-1]
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
	if len(cb.Tones()) != 0 || len(cb.LEDs()) != 0 || len(cb.Calls()) != 0 {
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
	if cb.Hangups() != 0 {
		t.Errorf("expected 0 HangupCall during DIALING, got %d", cb.Hangups())
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
	if cb.Hangups() != 1 {
		t.Errorf("expected 1 HangupCall during CALLING, got %d", cb.Hangups())
	}
}

// 6. Busy signal during CALLING → SendTone("BUSY"), stays in CALLING
func TestController_BusySignal(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234")

	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING, got %s", c.State())
	}

	tonesBefore := len(cb.Tones())
	c.HandleSignal("busy")

	// SendTone("STOP") should have been called
	if len(cb.Tones()) <= tonesBefore {
		t.Error("expected SendTone to be called on busy")
	}
	lastTone := cb.Tones()[len(cb.Tones())-1]
	if lastTone != "BUSY" {
		t.Errorf("expected SendTone(BUSY) on busy, got %s", lastTone)
	}
	// No hangup call triggered by signal
	if cb.Hangups() != 0 {
		t.Errorf("expected 0 HangupCall on busy signal, got %d", cb.Hangups())
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
	if len(cb.Calls()) != 1 || cb.Calls()[0] != "5551234" {
		t.Errorf("expected InitiateCall(5551234), got %v", cb.Calls())
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
	if len(cb.Calls()) != 0 {
		t.Errorf("expected NO InitiateCall for blocked number, got %v", cb.Calls())
	}
	// Should have sent RINGBACK tone (rejection mimics unreachable number)
	found := false
	for _, tone := range cb.Tones() {
		if tone == "RINGBACK" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected RINGBACK tone for blocked number, got tones: %v", cb.Tones())
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
	if len(cb.Calls()) != 1 {
		t.Errorf("expected call to proceed with no checker, got %v", cb.Calls())
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
	if len(cb.Calls()) != 0 {
		t.Errorf("expected no InitiateCall for self-dial, got %v", cb.Calls())
	}
	lastTone := cb.Tones()[len(cb.Tones())-1]
	if lastTone != "BUSY" {
		t.Errorf("expected BUSY tone for self-dial, got %s", lastTone)
	}
}

// 8. Caller hangs up while ringing → ring stops, return to IDLE
func TestController_CallerHangupDuringRing(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	c.HandleSignal("ring")
	if c.State() != StateRINGING {
		t.Fatalf("expected RINGING, got %s", c.State())
	}

	c.HandleSignal("hangup")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after caller hangup during ring, got %s", c.State())
	}
	// Ring must have been stopped
	lastRing := cb.Rings()[len(cb.Rings())-1]
	if lastRing != false {
		t.Error("expected SendRing(false) when caller hangs up during ring")
	}
	lastLED := cb.LEDs()[len(cb.LEDs())-1]
	if lastLED != "OFF" {
		t.Error("expected SendLED(OFF) when caller hangs up during ring")
	}
	// No HangupCall — there was no active WebRTC session
	if cb.Hangups() != 0 {
		t.Errorf("expected 0 HangupCall (no active call), got %d", cb.Hangups())
	}
}

func TestOnSignalAnswerNotifiesCallConnected(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "5551234")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5556789")

	// Wait for the 800ms async goroutine in onDial to set StateCALLING.
	deadline := time.Now().Add(2 * time.Second)
	for c.State() != StateCALLING && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if c.State() != StateCALLING {
		t.Fatalf("expected StateCALLING, got %s", c.State())
	}

	if cb.CallConnectedCalls() != 0 {
		t.Errorf("NotifyCallConnected should not be called before answer, got %d", cb.CallConnectedCalls())
	}

	c.HandleSignal("answer")

	if c.State() != StateCONNECTED {
		t.Errorf("expected StateCONNECTED after answer, got %s", c.State())
	}
	if cb.CallConnectedCalls() != 1 {
		t.Errorf("expected NotifyCallConnected to be called once, got %d", cb.CallConnectedCalls())
	}
}

func TestRingbackMustPlayUntilAnswer(t *testing.T) {
	// Ringback must keep playing from the moment the controller emits RINGBACK
	// until onSignalAnswer fires; nothing else should STOP it in between.
	cb := &mockCallbacks{}
	c := NewController(cb, "5551234")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5556789")

	waitForTone(cb, "RINGBACK")

	// The last tone recorded must still be RINGBACK: no STOP should have been
	// sent between RINGBACK and now.
	lastTone := ""
	if len(cb.Tones()) > 0 {
		lastTone = cb.Tones()[len(cb.Tones())-1]
	}
	if lastTone != "RINGBACK" {
		t.Errorf("expected last tone to be RINGBACK (ringback should still be playing), got %q; full sequence: %v", lastTone, cb.Tones())
	}
}

// 9. Incoming ring signal while CONNECTED → stays CONNECTED, ignored
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

	ringsBefore := len(cb.Rings())
	c.HandleSignal("ring")

	// State should still be CONNECTED
	if c.State() != StateCONNECTED {
		t.Errorf("expected CONNECTED after ring signal during active call, got %s", c.State())
	}
	// No new ring callbacks
	if len(cb.Rings()) != ringsBefore {
		t.Error("expected no SendRing call when ring arrives during CONNECTED")
	}
}

// 10. Dial-tone timeout (Pico fired TIMEOUT:DIAL_TONE) → POTS off-hook treatment.
// Caller leaves handset off-hook with no digits dialed: state transitions out of
// DIALTONE, reorder tone starts after the brief CO silence, keys are silently
// ignored, and going on-hook restores IDLE.
func TestController_DialToneTimeout(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	c.HandleEvent("HOOK:OFF")
	if c.State() != StateDIALTONE {
		t.Fatalf("expected DIALTONE, got %s", c.State())
	}

	c.HandleEvent("TIMEOUT:DIAL_TONE")
	if c.State() != StateOFFHOOK_TIMEOUT {
		t.Fatalf("expected OFFHOOK_TIMEOUT after TIMEOUT:DIAL_TONE, got %s", c.State())
	}

	// Dial tone must be cut and reorder tone must start (after brief silence).
	if !waitForTone(cb, "REORDER") {
		t.Errorf("expected REORDER tone after dial-tone timeout, got tones: %v", cb.Tones())
	}

	// Keypresses during off-hook treatment must be silently ignored.
	stateBefore := c.State()
	callsBefore := len(cb.Calls())
	c.HandleEvent("KEY:5")
	c.HandleEvent("KEY:5")
	c.HandleEvent("KEY:5")
	if c.State() != stateBefore {
		t.Errorf("expected state to stay %s after keys, got %s", stateBefore, c.State())
	}
	if len(cb.Calls()) != callsBefore {
		t.Errorf("expected no call attempts during off-hook treatment, got %v", cb.Calls())
	}

	// Hook-on returns to IDLE; nothing to hang up.
	hangupsBefore := cb.Hangups()
	c.HandleEvent("HOOK:ON")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after HOOK:ON, got %s", c.State())
	}
	if cb.Hangups() != hangupsBefore {
		t.Errorf("expected no HangupCall on off-hook timeout recovery, got %d new", cb.Hangups()-hangupsBefore)
	}
}

// Close() aborts a running off-hook treatment goroutine so daemon shutdown
// isn't blocked waiting for the 4-minute sequence to drain. The pre-REORDER
// silence is 1s; if Close is honored, REORDER must never fire.
func TestController_CloseAbortsOffHookTreatment(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("TIMEOUT:DIAL_TONE")
	c.Close()

	time.Sleep(1500 * time.Millisecond)
	for _, tone := range cb.Tones() {
		if tone == ToneReorder {
			t.Errorf("REORDER fired despite Close(); tones=%v", cb.Tones())
		}
	}
}

// 11. TIMEOUT:DIAL_TONE outside DIALTONE state is ignored (defensive).
func TestController_DialToneTimeout_IgnoredOutsideDialTone(t *testing.T) {
	cb := &mockCallbacks{}
	c := NewController(cb, "")

	// Idle: no off-hook → timeout makes no sense
	c.HandleEvent("TIMEOUT:DIAL_TONE")
	if c.State() != StateIDLE {
		t.Errorf("expected IDLE (timeout ignored), got %s", c.State())
	}

	// Mid-dial: user already pressed keys, don't slam them into off-hook treatment
	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	if c.State() != StateDIALING {
		t.Fatalf("expected DIALING, got %s", c.State())
	}
	c.HandleEvent("TIMEOUT:DIAL_TONE")
	if c.State() != StateDIALING {
		t.Errorf("expected DIALING (timeout ignored mid-dial), got %s", c.State())
	}
}

func TestIsCallActive(t *testing.T) {
	cases := []struct {
		name  string
		state State
		want  bool
	}{
		{"idle", StateIDLE, false},
		{"dialtone", StateDIALTONE, false},
		{"dialing", StateDIALING, false},
		{"calling", StateCALLING, true},
		{"ringing", StateRINGING, true},
		{"connected", StateCONNECTED, true},
		{"remote hangup", StateREMOTE_HANGUP, false},
		{"offhook timeout", StateOFFHOOK_TIMEOUT, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Controller{state: tc.state}
			if got := c.IsCallActive(); got != tc.want {
				t.Errorf("IsCallActive() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestController_FlashFromConnectedEntersAddDialtone(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateCONNECTED)
	c.setCurrentPeerForTest("5550002")

	c.HandleEvent("HOOK:FLASH")

	if c.State() != StateADD_DIALTONE {
		t.Fatalf("expected StateADD_DIALTONE, got %v", c.State())
	}
	if !mock.peerMuted("5550002") {
		t.Fatalf("expected A<->B muted")
	}
	if !mock.tonePlayed("DIAL") {
		t.Fatalf("expected dial tone started")
	}
}

func TestController_FlashInAddDialtoneAborts(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_DIALTONE)
	c.setHeldPeerForTest("5550002")

	c.HandleEvent("HOOK:FLASH")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED, got %v", c.State())
	}
	if mock.peerMuted("5550002") {
		t.Fatalf("expected A<->B unmuted on abort")
	}
}

func TestController_FlashInAddCallingAbortsAndTearsDown(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_CALLING)
	c.setHeldPeerForTest("5550002")
	c.setAddingPeerForTest("5550003")

	c.HandleEvent("HOOK:FLASH")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED, got %v", c.State())
	}
	if !mock.peerTorndown("5550003") {
		t.Fatalf("expected A<->C torn down")
	}
}

func TestController_FlashInAddPrivateMerges(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_PRIVATE)
	c.setHeldPeerForTest("5550002")
	c.setAddingPeerForTest("5550003")

	c.HandleEvent("HOOK:FLASH")

	if c.State() != StateCONFERENCE_MERGED {
		t.Fatalf("expected StateCONFERENCE_MERGED, got %v", c.State())
	}
	if !mock.mergeRequested("5550002", "5550003") {
		t.Fatalf("expected ConferenceMerge requested with B=5550002, C=5550003")
	}
}

func TestController_NonHostFlashIsNoop(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550002")
	c.setStateForTest(StateCONNECTED)
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: "host"},
		{Phone: "5550002", Role: "added"},
	})

	c.HandleEvent("HOOK:FLASH")

	if c.State() != StateCONNECTED {
		t.Fatalf("non-host flash should be no-op; state changed to %v", c.State())
	}
}

func TestController_FlashInConferenceMergedIsNoop(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateCONFERENCE_MERGED)
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: "host"},
		{Phone: "5550002", Role: "added"},
	})

	c.HandleEvent("HOOK:FLASH")

	if c.State() != StateCONFERENCE_MERGED {
		t.Fatalf("flash during CONFERENCE_MERGED should be no-op; state changed to %v", c.State())
	}
}

func TestController_FlashInAddInterceptAborts(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_INTERCEPT)
	c.setHeldPeerForTest("5550002")

	c.HandleEvent("HOOK:FLASH")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected return to StateCONNECTED from ADD_INTERCEPT, got %v", c.State())
	}
}

// calledNumber returns true if InitiateCall was invoked with the given number.
func (m *mockCallbacks) calledNumber(number string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c == number {
			return true
		}
	}
	return false
}

func TestController_DialInAddDialtoneEntersAddDialing(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_DIALTONE)
	c.setHeldPeerForTest("5550002")

	c.HandleEvent("KEY:5")

	if c.State() != StateADD_DIALING {
		t.Fatalf("expected StateADD_DIALING after first digit, got %v", c.State())
	}
	// Second dial tone should stop on first digit.
	if !mock.tonePlayed(ToneStop) {
		t.Fatalf("expected dial tone stopped on first digit")
	}
}

func TestController_DialInAddDialingReachesAddCallingOnDialEvent(t *testing.T) {
	// The firmware collects digits and sends DIAL:<number> when done —
	// matching the existing StateDIALING → StateCALLING pattern exactly.
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_DIALTONE)
	c.setHeldPeerForTest("5550002")

	// First digit: transitions ADD_DIALTONE → ADD_DIALING.
	c.HandleEvent("KEY:5")
	if c.State() != StateADD_DIALING {
		t.Fatalf("expected StateADD_DIALING after first digit, got %v", c.State())
	}

	// Firmware sends DIAL event with complete number.
	c.HandleEvent("DIAL:5550003")

	if c.State() != StateADD_CALLING {
		t.Fatalf("expected StateADD_CALLING after DIAL event, got %v", c.State())
	}
	// InitiateCall fires after brief async delay.
	waitForCall(mock)
	if !mock.calledNumber("5550003") {
		t.Fatalf("expected call placed to 5550003, got calls: %v", mock.Calls())
	}
}

func TestController_ThirdAnswersEntersAddPrivate(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_CALLING)
	c.setAddingPeerForTest("5550003")

	c.HandleSignal("answer", "5550003")

	if c.State() != StateADD_PRIVATE {
		t.Fatalf("expected StateADD_PRIVATE, got %v", c.State())
	}
}

func TestController_ThirdBusyGoesToAddIntercept(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_CALLING)
	c.setAddingPeerForTest("5550003")

	c.HandleSignal("busy", "5550003")

	if c.State() != StateADD_INTERCEPT {
		t.Fatalf("expected StateADD_INTERCEPT, got %v", c.State())
	}
	if !mock.tonePlayed(ToneIntercept) {
		t.Fatalf("expected INTERCEPT tone on busy")
	}
	if !mock.peerTorndown("5550003") {
		t.Fatalf("expected C torn down on busy")
	}
}

func TestController_ThirdHangupDuringPrivateGoesToAddIntercept(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateADD_PRIVATE)
	c.setAddingPeerForTest("5550003")

	c.HandleSignal("hangup", "5550003")

	if c.State() != StateADD_INTERCEPT {
		t.Fatalf("expected StateADD_INTERCEPT, got %v", c.State())
	}
	if !mock.peerTorndown("5550003") {
		t.Fatalf("expected C torn down on hangup")
	}
}

func TestController_ThirdRingTimeoutGoesToAddIntercept(t *testing.T) {
	// Ring timeout not modeled locally; handled by server responding with TypeBusy.
	t.Skip("ring timeout not modeled locally; handled by server responding with TypeBusy")
}

func TestController_ConferenceMemberMarksHostRole(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: "host"},
		{Phone: "5550002", Role: "added"},
		{Phone: "5550003", Role: "added"},
	})

	if c.ConferenceID() != "conf-abc" {
		t.Fatalf("expected conf id conf-abc, got %s", c.ConferenceID())
	}
	if !c.IsConferenceHost() {
		t.Fatalf("5550001 should be host")
	}
}

func TestController_ConferenceMemberNonHost(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550002")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: "host"},
		{Phone: "5550002", Role: "added"},
		{Phone: "5550003", Role: "added"},
	})
	if c.IsConferenceHost() {
		t.Fatalf("5550002 should not be host")
	}
}

func TestController_ConferenceConnectOpensPeer(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550002")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: "host"}, {Phone: "5550002", Role: "added"}, {Phone: "5550003", Role: "added"},
	})

	c.HandleConferenceConnect("conf-abc", "5550003", true)

	if !mock.peerAdded("5550003") {
		t.Fatalf("expected AddMeshPeer(5550003)")
	}
	if !mock.peerInitiator("5550003") {
		t.Fatalf("expected 5550003 to be added as initiator")
	}
}

func TestController_ConferenceConnectWrongConfIDIgnored(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550002")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: "host"}, {Phone: "5550002", Role: "added"}, {Phone: "5550003", Role: "added"},
	})

	c.HandleConferenceConnect("conf-wrong", "5550003", true)

	if mock.peerAdded("5550003") {
		t.Fatalf("AddMeshPeer should not be called for wrong conf id")
	}
}

func TestController_ConferenceLeaveRemovesPeer(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: "host"}, {Phone: "5550002", Role: "added"}, {Phone: "5550003", Role: "added"},
	})

	c.HandleConferenceLeave("conf-abc", "5550002", "hangup")

	if !mock.peerTorndown("5550002") {
		t.Fatalf("expected RemoveMeshPeer(5550002) / TearDownPeer(5550002)")
	}
}

func TestController_ConferenceEndTearsDownAllAndReturnsIdle(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateCONFERENCE_MERGED)
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: "host"}, {Phone: "5550002", Role: "added"}, {Phone: "5550003", Role: "added"},
	})

	c.HandleConferenceEnd("conf-abc", "host_hangup")

	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after conference end, got %v", c.State())
	}
	if !mock.allPeersTorndown() {
		t.Fatalf("expected TearDownAllMeshPeers")
	}
	if c.ConferenceID() != "" {
		t.Fatalf("expected conf state cleared, got %q", c.ConferenceID())
	}
}

func TestController_ConferenceRejectedReturnsToConnected(t *testing.T) {
	mock := &mockCallbacks{}
	c := NewController(mock, "5550001")
	c.setStateForTest(StateCONFERENCE_MERGED)
	c.setHeldPeerForTest("5550002")
	c.setAddingPeerForTest("5550003")

	c.HandleConferenceRejected("", "merge_failed")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED on rejection, got %v", c.State())
	}
	if !mock.tonePlayed(ToneIntercept) {
		t.Fatalf("expected intercept tone on rejection")
	}
	if !mock.peerTorndown("5550003") {
		t.Fatalf("expected A->C torn down")
	}
}

package phone

import (
	"fmt"
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
	picoResets         int
	callReturns        int
	ringPatterns       []int
	callReturnCancels  int
	callReturnAbandons int
	flashDetections    int               // EnableFlashDetection call count
	mutedPeers         map[string]bool   // phone -> current mute state
	torndownPeers      []string          // peers that had TearDownPeer called
	removedMeshPeers   []string          // peers that had RemoveMeshPeer called
	mergeRequests      [][2]string       // [held, active] pairs
	meshPeers          map[string]bool   // phone -> initiator flag
	allTorndown        bool              // true if TearDownAllMeshPeers was called
	migratedToMesh     map[string]bool   // phone -> true if MigrateToMesh was called
	initiateCallErr    error             // injected error for InitiateCall
	voicemailAutoAnswers     int
	voicemailPickups         int
	voicemailRecordEnded     int
	voicemailEnabled         func() (bool, time.Duration) // nil = (false, 0)
	voicemailRecordGreetings int
	voicemailRecordKeys      []string
	voicemailPlayGreetings   int
	greetingPlaybackExits    int
	voicemailDeleteGreetings int
	playbackEnters           int
	playbackExits            int
	playbackKeys             []string
}

func (m *mockCallbacks) SendTone(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tones = append(m.tones, name)
}
func (m *mockCallbacks) OncePlaying() bool { return false }
func (m *mockCallbacks) StartRing() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rings = append(m.rings, true)
}
func (m *mockCallbacks) StopRing() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rings = append(m.rings, false)
}
func (m *mockCallbacks) SendLED(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leds = append(m.leds, mode)
}
func (m *mockCallbacks) EnableFlashDetection() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flashDetections++
}
func (m *mockCallbacks) InitiateCall(number string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, number)
	return m.initiateCallErr
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
func (m *mockCallbacks) NotifyPicoReset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.picoResets++
}
func (m *mockCallbacks) MutePeer(phone string) { m.setMuted(phone, true) }

func (m *mockCallbacks) UnmutePeer(phone string) { m.setMuted(phone, false) }

func (m *mockCallbacks) setMuted(phone string, muted bool) {
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
	m.removedMeshPeers = append(m.removedMeshPeers, phone)
}
func (m *mockCallbacks) TearDownAllMeshPeers() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allTorndown = true
}
func (m *mockCallbacks) MigrateToMesh(phone string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.migratedToMesh == nil {
		m.migratedToMesh = make(map[string]bool)
	}
	m.migratedToMesh[phone] = true
}
func (m *mockCallbacks) OnCallReturn() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callReturns++
}
func (m *mockCallbacks) SendRingPattern(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ringPatterns = append(m.ringPatterns, id)
}
func (m *mockCallbacks) OnCallReturnCancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callReturnCancels++
}
func (m *mockCallbacks) OnCallReturnAbandon() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callReturnAbandons++
}
func (m *mockCallbacks) VoicemailAutoAnswer() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voicemailAutoAnswers++
}
func (m *mockCallbacks) VoicemailPickup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voicemailPickups++
}
func (m *mockCallbacks) VoicemailRecordEnded() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voicemailRecordEnded++
}
func (m *mockCallbacks) VoicemailEnabled() (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.voicemailEnabled != nil {
		return m.voicemailEnabled()
	}
	return false, 0
}
func (m *mockCallbacks) VoicemailRecordGreeting() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voicemailRecordGreetings++
}
func (m *mockCallbacks) VoicemailRecordGreetingKey(digit string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voicemailRecordKeys = append(m.voicemailRecordKeys, digit)
}
func (m *mockCallbacks) VoicemailPlayGreeting() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voicemailPlayGreetings++
}
func (m *mockCallbacks) VoicemailExitGreetingPlayback() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.greetingPlaybackExits++
}
func (m *mockCallbacks) VoicemailDeleteGreeting() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voicemailDeleteGreetings++
}
func (m *mockCallbacks) VoicemailEnterPlayback() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.playbackEnters++
}
func (m *mockCallbacks) VoicemailExitPlayback() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.playbackExits++
}
func (m *mockCallbacks) VoicemailKey(digit string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.playbackKeys = append(m.playbackKeys, digit)
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
func (m *mockCallbacks) CallReturns() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callReturns
}
func (m *mockCallbacks) RingPatterns() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.ringPatterns...)
}
func (m *mockCallbacks) CallReturnCancels() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callReturnCancels
}
func (m *mockCallbacks) CallReturnAbandons() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callReturnAbandons
}
func (m *mockCallbacks) CallConnectedCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callConnectedCalls
}
func (m *mockCallbacks) PicoResets() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.picoResets
}
func (m *mockCallbacks) VoicemailAutoAnswers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.voicemailAutoAnswers
}
func (m *mockCallbacks) VoicemailPickups() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.voicemailPickups
}
func (m *mockCallbacks) VoicemailRecordGreetings() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.voicemailRecordGreetings
}
func (m *mockCallbacks) VoicemailDeleteGreetings() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.voicemailDeleteGreetings
}
func (m *mockCallbacks) VoicemailPlayGreetings() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.voicemailPlayGreetings
}
func (m *mockCallbacks) GreetingPlaybackExits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.greetingPlaybackExits
}
func (m *mockCallbacks) VoicemailRecordKeys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.voicemailRecordKeys...)
}
func (m *mockCallbacks) PlaybackEnters() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.playbackEnters
}
func (m *mockCallbacks) PlaybackExits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.playbackExits
}
func (m *mockCallbacks) PlaybackKeys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.playbackKeys...)
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

// meshPeerRemoved returns whether RemoveMeshPeer was called for the given phone.
func (m *mockCallbacks) meshPeerRemoved(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.removedMeshPeers {
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

// wasMigrated returns whether MigrateToMesh was called for the given phone.
func (m *mockCallbacks) wasMigrated(phone string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.migratedToMesh[phone]
}

func waitForCall(cb *mockCallbacks) {
	for i := 0; i < 100; i++ {
		if len(cb.Calls()) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForTone(cb *mockCallbacks, tone string) bool {
	for i := 0; i < 100; i++ {
		for _, t := range cb.Tones() {
			if t == tone {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestController_OutgoingCallFlow(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")

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
	c.HandleSignal("answer", "")
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
	c := newTestController(cb, "")

	// Incoming ring
	c.HandleSignal("ring", "")
	if c.State() != StateRINGING {
		t.Fatalf("expected RINGING, got %s", c.State())
	}
	if len(cb.Rings()) == 0 || cb.Rings()[0] != true {
		t.Error("expected StartRing")
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
		t.Error("expected StopRing when answering")
	}

	// Remote hangs up — enters REMOTE_HANGUP (off-hook warning sequence)
	c.HandleSignal("hangup", "")
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
	c := newTestController(cb, "")

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
	c := newTestController(cb, "")

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
	c := newTestController(cb, "")

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
	c := newTestController(cb, "")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234")

	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING, got %s", c.State())
	}

	tonesBefore := len(cb.Tones())
	c.HandleSignal("busy", "")

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

// allowContacts builds a ContactChecker that admits only the given numbers.
func allowContacts(numbers ...string) ContactChecker {
	allowed := make(map[string]bool, len(numbers))
	for _, n := range numbers {
		allowed[n] = true
	}
	return func(number string) bool { return allowed[number] }
}

// Test: dialing an allowed contact proceeds normally
func TestController_DialAllowedContact(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")
	c.SetContactChecker(allowContacts("5551234"))

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
	c := newTestController(cb, "")
	c.SetContactChecker(allowContacts("5559999"))

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

func TestController_DialServerUnreachable(t *testing.T) {
	cb := &mockCallbacks{initiateCallErr: fmt.Errorf("signal: not connected")}
	c := newTestController(cb, "")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234")

	if c.State() != StateCALLING {
		t.Fatalf("expected CALLING, got %s", c.State())
	}

	// InitiateCall was attempted (after async dial delay)
	waitForCall(cb)
	if !cb.calledNumber("5551234") {
		t.Fatalf("expected InitiateCall(5551234), got %v", cb.Calls())
	}

	if !waitForTone(cb, ToneIntercept) {
		t.Errorf("expected INTERCEPT tone after server-unreachable call, got tones: %v", cb.Tones())
	}
}

// Test: no ContactChecker set = all calls allowed (backward compat)
func TestController_NoContactChecker_AllowsAll(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")
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
	c := newTestController(cb, "5551234")

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
	c := newTestController(cb, "")

	c.HandleSignal("ring", "")
	if c.State() != StateRINGING {
		t.Fatalf("expected RINGING, got %s", c.State())
	}

	c.HandleSignal("hangup", "")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after caller hangup during ring, got %s", c.State())
	}
	// Ring must have been stopped
	lastRing := cb.Rings()[len(cb.Rings())-1]
	if lastRing != false {
		t.Error("expected StopRing when caller hangs up during ring")
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
	c := newTestController(cb, "5551234")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5556789")

	deadline := time.Now().Add(500 * time.Millisecond)
	for c.State() != StateCALLING && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.State() != StateCALLING {
		t.Fatalf("expected StateCALLING, got %s", c.State())
	}

	if cb.CallConnectedCalls() != 0 {
		t.Errorf("NotifyCallConnected should not be called before answer, got %d", cb.CallConnectedCalls())
	}

	c.HandleSignal("answer", "")

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
	c := newTestController(cb, "5551234")

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
	c := newTestController(cb, "")

	// Set up CONNECTED state via outgoing call flow
	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	c.HandleEvent("DIAL:5551234")
	waitForCall(cb)
	c.HandleSignal("answer", "")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED, got %s", c.State())
	}

	ringsBefore := len(cb.Rings())
	c.HandleSignal("ring", "")

	// State should still be CONNECTED
	if c.State() != StateCONNECTED {
		t.Errorf("expected CONNECTED after ring signal during active call, got %s", c.State())
	}
	// No new ring callbacks
	if len(cb.Rings()) != ringsBefore {
		t.Error("expected no ring call when ring arrives during CONNECTED")
	}
}

// 10. Dial-tone timeout (Pico fired TIMEOUT:DIAL_TONE) → POTS off-hook treatment.
// Caller leaves handset off-hook with no digits dialed: state transitions out of
// DIALTONE, reorder tone starts after the brief CO silence, keys are silently
// ignored, and going on-hook restores IDLE.
func TestController_DialToneTimeout(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")

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
	c := newTestController(cb, "")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("TIMEOUT:DIAL_TONE")
	c.Close()

	time.Sleep(50 * time.Millisecond)
	for _, tone := range cb.Tones() {
		if tone == ToneReorder {
			t.Errorf("REORDER fired despite Close(); tones=%v", cb.Tones())
		}
	}
}

// 11. TIMEOUT:DIAL_TONE outside DIALTONE state is ignored (defensive).
func TestController_DialToneTimeout_IgnoredOutsideDialTone(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")

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
		{"voicemail greeting", StateVOICEMAIL_GREETING, true},
		{"voicemail recording", StateVOICEMAIL_RECORDING, true},
		{"voicemail record greeting", StateVOICEMAIL_RECORD_GREETING, true},
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
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateCONNECTED)

	c.HandleHookFlash("5550002")

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
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateADD_DIALTONE)
	c.setHeldPeerForTest("5550002")

	c.HandleHookFlash("")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED, got %v", c.State())
	}
	if mock.peerMuted("5550002") {
		t.Fatalf("expected A<->B unmuted on abort")
	}
}

func TestController_FlashInAddCallingAbortsAndTearsDown(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateADD_CALLING)
	c.setHeldPeerForTest("5550002")
	c.setAddingPeerForTest("5550003")

	c.HandleHookFlash("")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED, got %v", c.State())
	}
	if !mock.peerTorndown("5550003") {
		t.Fatalf("expected A<->C torn down")
	}
}

func TestController_FlashInAddPrivateMerges(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateADD_PRIVATE)
	c.setHeldPeerForTest("5550002")
	c.setAddingPeerForTest("5550003")

	c.HandleHookFlash("")

	if c.State() != StateCONFERENCE_MERGED {
		t.Fatalf("expected StateCONFERENCE_MERGED, got %v", c.State())
	}
	if !mock.mergeRequested("5550002", "5550003") {
		t.Fatalf("expected ConferenceMerge requested with B=5550002, C=5550003")
	}
}

func TestController_NonHostFlashIsNoop(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550002")
	c.setStateForTest(StateCONNECTED)
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost},
		{Phone: "5550002", Role: signal.RoleAdded},
	})

	c.HandleHookFlash("")

	if c.State() != StateCONNECTED {
		t.Fatalf("non-host flash should be no-op; state changed to %v", c.State())
	}
}

func TestController_FlashInConferenceMergedIsNoop(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateCONFERENCE_MERGED)
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost},
		{Phone: "5550002", Role: signal.RoleAdded},
	})

	c.HandleHookFlash("")

	if c.State() != StateCONFERENCE_MERGED {
		t.Fatalf("flash during CONFERENCE_MERGED should be no-op; state changed to %v", c.State())
	}
}

func TestController_FlashInAddInterceptAborts(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateADD_INTERCEPT)
	c.setHeldPeerForTest("5550002")

	c.HandleHookFlash("")

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
	c := newTestController(mock, "5550001")
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
	c := newTestController(mock, "5550001")
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
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateADD_CALLING)
	c.setAddingPeerForTest("5550003")

	c.HandleSignal("answer", "5550003")

	if c.State() != StateADD_PRIVATE {
		t.Fatalf("expected StateADD_PRIVATE, got %v", c.State())
	}
}

func TestController_ThirdBusyPlaysBusyTone(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateADD_CALLING)
	c.setAddingPeerForTest("5550003")

	c.HandleSignal("busy", "5550003")

	if c.State() != StateADD_INTERCEPT {
		t.Fatalf("expected StateADD_INTERCEPT, got %v", c.State())
	}
	if !mock.tonePlayed(ToneBusy) {
		t.Fatalf("expected BUSY tone on subscriber-busy (matches 2-party), got tones: %v", mock.Tones())
	}
	if mock.tonePlayed(ToneIntercept) {
		t.Fatalf("SIT INTERCEPT is reserved for 'cannot be completed as dialed'; subscriber-busy should play BUSY, got tones: %v", mock.Tones())
	}
	if !mock.peerTorndown("5550003") {
		t.Fatalf("expected C torn down on busy")
	}
}

func TestController_ThirdHangupDuringPrivateGoesToAddIntercept(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
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
	c := newTestController(mock, "5550001")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost},
		{Phone: "5550002", Role: signal.RoleAdded},
		{Phone: "5550003", Role: signal.RoleAdded},
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
	c := newTestController(mock, "5550002")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost},
		{Phone: "5550002", Role: signal.RoleAdded},
		{Phone: "5550003", Role: signal.RoleAdded},
	})
	if c.IsConferenceHost() {
		t.Fatalf("5550002 should not be host")
	}
}

func TestController_ConferenceConnectOpensPeer(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550002")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost}, {Phone: "5550002", Role: signal.RoleAdded}, {Phone: "5550003", Role: signal.RoleAdded},
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
	c := newTestController(mock, "5550002")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost}, {Phone: "5550002", Role: signal.RoleAdded}, {Phone: "5550003", Role: signal.RoleAdded},
	})

	c.HandleConferenceConnect("conf-wrong", "5550003", true)

	if mock.peerAdded("5550003") {
		t.Fatalf("AddMeshPeer should not be called for wrong conf id")
	}
}

func TestController_ConferenceLeaveRemovesPeer(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost}, {Phone: "5550002", Role: signal.RoleAdded}, {Phone: "5550003", Role: signal.RoleAdded},
	})

	c.HandleConferenceLeave("conf-abc", "5550002", "hangup")

	if !mock.meshPeerRemoved("5550002") {
		t.Fatalf("expected RemoveMeshPeer(5550002)")
	}
	if mock.peerTorndown("5550002") {
		t.Fatalf("RemoveMeshPeer should not push to torndownPeers")
	}
}

func TestController_ConferenceEndTearsDownAllAndReturnsIdle(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	defer c.Close()
	c.setStateForTest(StateCONFERENCE_MERGED)
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost}, {Phone: "5550002", Role: signal.RoleAdded}, {Phone: "5550003", Role: signal.RoleAdded},
	})

	c.HandleConferenceEnd("conf-abc", "host_hangup")

	// When the user is still off-hook (CONFERENCE_MERGED) when the conference
	// ends, the controller transitions to REMOTE_HANGUP and plays the permanent-
	// signal treatment, matching 2-party remote-hangup semantics.
	if c.State() != StateREMOTE_HANGUP {
		t.Fatalf("expected REMOTE_HANGUP after conference end (user still off-hook), got %v", c.State())
	}
	if !mock.allPeersTorndown() {
		t.Fatalf("expected TearDownAllMeshPeers")
	}
	if c.ConferenceID() != "" {
		t.Fatalf("expected conf state cleared, got %q", c.ConferenceID())
	}
}

func TestController_ConferenceEndFromIdleReturnsIdle(t *testing.T) {
	// Verify that HandleConferenceEnd from a non-conference state (e.g. IDLE
	// after the user already hung up) goes to IDLE, not REMOTE_HANGUP.
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	defer c.Close()
	// Simulate: user hung up, then ConferenceEnd arrives late.
	c.setStateForTest(StateIDLE)
	// Bypass confID guard by setting it manually.
	c.mu.Lock()
	c.confID = "conf-abc"
	c.mu.Unlock()

	c.HandleConferenceEnd("conf-abc", "host_hangup")

	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE when conference ends while already idle, got %v", c.State())
	}
}

// TestController_ConferenceLeaveAfterLocalTeardownIsBenign verifies that a
// ConferenceLeave arriving after the host already cleared confID locally
// returns silently without emitting Warn or invoking RemoveMeshPeer.
func TestController_ConferenceLeaveAfterLocalTeardownIsBenign(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	defer c.Close()
	// confID is "" by default, simulating the post-teardown state.

	c.HandleConferenceLeave("conf-abc", "5550002", "hangup")

	if mock.meshPeerRemoved("5550002") {
		t.Fatalf("RemoveMeshPeer must not fire when local conference state is already cleared")
	}
}

// TestController_ConferenceEndAfterLocalTeardownIsBenign verifies that a
// ConferenceEnd arriving after the host already cleared confID locally
// returns silently without invoking TearDownAllMeshPeers or HangupCall.
func TestController_ConferenceEndAfterLocalTeardownIsBenign(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	defer c.Close()
	// confID is "" by default, simulating the post-teardown state.

	c.HandleConferenceEnd("conf-abc", "host_hangup")

	if mock.allPeersTorndown() {
		t.Fatalf("TearDownAllMeshPeers must not fire when local conference state is already cleared")
	}
	if mock.Hangups() != 0 {
		t.Fatalf("HangupCall must not fire when local conference state is already cleared, got %d", mock.Hangups())
	}
}

func TestController_ConferenceRejectedReturnsToConnected(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	// Establish conference ID via HandleConferenceMember so the confID guard is satisfied.
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost},
		{Phone: "5550002", Role: signal.RoleAdded},
		{Phone: "5550003", Role: signal.RoleAdded},
	})
	c.setStateForTest(StateCONFERENCE_MERGED)
	c.setHeldPeerForTest("5550002")
	c.setAddingPeerForTest("5550003")

	c.HandleConferenceRejected("conf-abc", "merge_failed")

	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED on rejection, got %v", c.State())
	}
	if !mock.tonePlayed(ToneIntercept) {
		t.Fatalf("expected intercept tone on rejection")
	}
	if !mock.peerTorndown("5550003") {
		t.Fatalf("expected A->C torn down")
	}
	if c.ConferenceID() != "" {
		t.Fatalf("expected conference state cleared, got %q", c.ConferenceID())
	}
}

func TestController_MuteLiftsOnMerge(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateCONNECTED)

	// Flash from CONNECTED: controller calls MigrateToMesh(B), MutePeer(B) and enters ADD_DIALTONE.
	c.HandleHookFlash("5550002")
	if c.State() != StateADD_DIALTONE {
		t.Fatalf("expected StateADD_DIALTONE after flash, got %v", c.State())
	}
	if !mock.wasMigrated("5550002") {
		t.Fatalf("expected MigrateToMesh(5550002) on flash to ADD_DIALTONE")
	}
	if !mock.peerMuted("5550002") {
		t.Fatalf("expected B muted after flash")
	}

	// First digit transitions ADD_DIALTONE -> ADD_DIALING.
	c.HandleEvent("KEY:5")
	if c.State() != StateADD_DIALING {
		t.Fatalf("expected StateADD_DIALING after first digit, got %v", c.State())
	}

	// DIAL event transitions ADD_DIALING -> ADD_CALLING.
	c.HandleEvent("DIAL:5550003")
	if c.State() != StateADD_CALLING {
		t.Fatalf("expected StateADD_CALLING after DIAL, got %v", c.State())
	}
	// B should still be muted while dialing C.
	if !mock.peerMuted("5550002") {
		t.Fatalf("expected B to remain muted in ADD_CALLING")
	}

	// C answers: ADD_CALLING -> ADD_PRIVATE.
	c.HandleSignal("answer", "5550003")
	if c.State() != StateADD_PRIVATE {
		t.Fatalf("expected StateADD_PRIVATE after C answers, got %v", c.State())
	}
	// B should STILL be muted while A is in ADD_PRIVATE.
	if !mock.peerMuted("5550002") {
		t.Fatalf("expected B to remain muted in ADD_PRIVATE")
	}

	// Flash to merge: B should be unmuted and C migrated into mesh before merge request.
	c.HandleHookFlash("")
	if c.State() != StateCONFERENCE_MERGED {
		t.Fatalf("expected StateCONFERENCE_MERGED after flash, got %v", c.State())
	}
	if mock.peerMuted("5550002") {
		t.Fatalf("expected B unmuted after merge")
	}
	if !mock.wasMigrated("5550003") {
		t.Fatalf("expected MigrateToMesh(5550003) on merge")
	}
	if !mock.mergeRequested("5550002", "5550003") {
		t.Fatalf("expected ConferenceMerge requested with B=5550002, C=5550003")
	}
}

// TestController_OnHookOnFromConferenceStates verifies that hanging up from any
// conference-related state tears down all mesh peers, calls HangupCall, stops
// tones, turns off the LED, and resets all conference fields.
func TestController_OnHookOnFromConferenceStates(t *testing.T) {
	cases := []struct {
		name  string
		state State
	}{
		{"ADD_DIALTONE", StateADD_DIALTONE},
		{"ADD_DIALING", StateADD_DIALING},
		{"ADD_CALLING", StateADD_CALLING},
		{"ADD_PRIVATE", StateADD_PRIVATE},
		{"ADD_INTERCEPT", StateADD_INTERCEPT},
		{"CONFERENCE_MERGED", StateCONFERENCE_MERGED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockCallbacks{}
			c := newTestController(mock, "5550001")
			c.setStateForTest(tc.state)
			c.setHeldPeerForTest("5550002")
			c.setAddingPeerForTest("5550003")
			// Simulate being part of a conference so the guard fires.
			c.mu.Lock()
			c.confID = "conf-abc"
			c.mu.Unlock()

			c.HandleEvent("HOOK:ON")

			if c.State() != StateIDLE {
				t.Errorf("expected StateIDLE, got %v", c.State())
			}
			if !mock.allPeersTorndown() {
				t.Errorf("expected TearDownAllMeshPeers to be called")
			}
			if mock.Hangups() == 0 {
				t.Errorf("expected HangupCall to be called")
			}
			if !mock.tonePlayed(ToneStop) {
				t.Errorf("expected SendTone(STOP)")
			}
			leds := mock.LEDs()
			if len(leds) == 0 || leds[len(leds)-1] != "OFF" {
				t.Errorf("expected SendLED(OFF), got %v", leds)
			}
			if c.ConferenceID() != "" {
				t.Errorf("expected confID cleared, got %q", c.ConferenceID())
			}
			// Verify heldPeer and addingPeer cleared (via setters using lock — read directly under lock).
			c.mu.Lock()
			held, adding := c.heldPeer, c.addingPeer
			c.mu.Unlock()
			if held != "" {
				t.Errorf("expected heldPeer cleared, got %q", held)
			}
			if adding != "" {
				t.Errorf("expected addingPeer cleared, got %q", adding)
			}
		})
	}
}

// TestController_HandleConferenceEndFromAddStates verifies that HandleConferenceEnd
// transitions to REMOTE_HANGUP (not IDLE) and tears down all mesh peers when
// called from any of the ADD_* intermediate states.
func TestController_HandleConferenceEndFromAddStates(t *testing.T) {
	cases := []struct {
		name  string
		state State
	}{
		{"ADD_DIALTONE", StateADD_DIALTONE},
		{"ADD_DIALING", StateADD_DIALING},
		{"ADD_CALLING", StateADD_CALLING},
		{"ADD_PRIVATE", StateADD_PRIVATE},
		{"ADD_INTERCEPT", StateADD_INTERCEPT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockCallbacks{}
			c := newTestController(mock, "5550001")
			defer c.Close()
			// Set the conference state so the confID guard passes.
			c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
				{Phone: "5550001", Role: signal.RoleHost},
				{Phone: "5550002", Role: signal.RoleAdded},
			})
			c.setStateForTest(tc.state)

			c.HandleConferenceEnd("conf-abc", "host_hangup")

			if c.State() != StateREMOTE_HANGUP {
				t.Errorf("expected StateREMOTE_HANGUP from %v, got %v", tc.state, c.State())
			}
			if !mock.allPeersTorndown() {
				t.Errorf("expected TearDownAllMeshPeers to be called")
			}
			if c.ConferenceID() != "" {
				t.Errorf("expected confID cleared, got %q", c.ConferenceID())
			}
		})
	}
}

// TestController_DialThirdPartyRejectsSelfDial verifies that dialThirdParty
// rejects a self-call (ADD_DIALING -> ADD_INTERCEPT) without calling InitiateCall.
func TestController_DialThirdPartyRejectsSelfDial(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateADD_DIALTONE)
	c.setHeldPeerForTest("5550002")

	// First digit: transitions ADD_DIALTONE -> ADD_DIALING.
	c.HandleEvent("KEY:5")
	if c.State() != StateADD_DIALING {
		t.Fatalf("expected StateADD_DIALING, got %v", c.State())
	}

	// Dial own number as third party.
	c.HandleEvent("DIAL:5550001")

	if c.State() != StateADD_INTERCEPT {
		t.Fatalf("expected StateADD_INTERCEPT on self-dial, got %v", c.State())
	}
	if len(mock.Calls()) != 0 {
		t.Errorf("expected no InitiateCall on self-dial, got %v", mock.Calls())
	}
	if !mock.tonePlayed(ToneIntercept) {
		t.Errorf("expected ToneIntercept on self-dial, got tones: %v", mock.Tones())
	}
}

// TestController_DialThirdPartyRejectsBlockedContact verifies that dialThirdParty
// rejects a number not in the contact list (ADD_DIALING -> ADD_INTERCEPT).
func TestController_DialThirdPartyRejectsBlockedContact(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	// Only 5550002 is allowed; 5550004 is blocked.
	c.SetContactChecker(allowContacts("5550002"))
	c.setStateForTest(StateADD_DIALTONE)
	c.setHeldPeerForTest("5550002")

	// First digit: transitions ADD_DIALTONE -> ADD_DIALING.
	c.HandleEvent("KEY:5")
	if c.State() != StateADD_DIALING {
		t.Fatalf("expected StateADD_DIALING, got %v", c.State())
	}

	// Dial a blocked number as third party.
	c.HandleEvent("DIAL:5550004")

	if c.State() != StateADD_INTERCEPT {
		t.Fatalf("expected StateADD_INTERCEPT on blocked contact, got %v", c.State())
	}
	if len(mock.Calls()) != 0 {
		t.Errorf("expected no InitiateCall for blocked contact, got %v", mock.Calls())
	}
	if !mock.tonePlayed(ToneIntercept) {
		t.Errorf("expected ToneIntercept on blocked contact, got tones: %v", mock.Tones())
	}
}

func TestController_ConferenceRejectedIgnoredOnWrongConfID(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateCONFERENCE_MERGED)
	c.setHeldPeerForTest("5550002")
	c.setAddingPeerForTest("5550003")
	c.HandleConferenceMember("conf-abc", []signal.ConferenceMemberInfo{
		{Phone: "5550001", Role: signal.RoleHost}, {Phone: "5550002", Role: signal.RoleAdded}, {Phone: "5550003", Role: signal.RoleAdded},
	})

	// Rejection for a different conf -- should be ignored.
	c.HandleConferenceRejected("conf-xyz", "merge_failed")

	if c.State() != StateCONFERENCE_MERGED {
		t.Fatalf("state should remain StateCONFERENCE_MERGED on mismatched confID, got %v", c.State())
	}
	if c.ConferenceID() != "conf-abc" {
		t.Fatalf("confID should remain conf-abc, got %q", c.ConferenceID())
	}
	if mock.tonePlayed(ToneIntercept) {
		t.Fatalf("intercept tone should not play for mismatched rejection")
	}
}

// TestController_DoubleFlashFromConnectedIsStable exercises the enter-ADD /
// abort-ADD cycle twice in succession to confirm the FSM returns to a clean
// CONNECTED state each time. Regression guard for the abort-then-retry flow
// that surfaced mesh-leak and stale-callPeer bugs during manual testing.
func TestController_DoubleFlashFromConnectedIsStable(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateCONNECTED)

	// Flash 1: CONNECTED -> ADD_DIALTONE.
	c.HandleHookFlash("5550002")
	if c.State() != StateADD_DIALTONE {
		t.Fatalf("flash 1: expected StateADD_DIALTONE, got %v", c.State())
	}
	if !mock.peerMuted("5550002") {
		t.Fatalf("flash 1: expected B muted")
	}

	// Flash 2: ADD_DIALTONE -> CONNECTED (abort).
	c.HandleHookFlash("")
	if c.State() != StateCONNECTED {
		t.Fatalf("flash 2: expected StateCONNECTED, got %v", c.State())
	}
	if mock.peerMuted("5550002") {
		t.Fatalf("flash 2: expected B unmuted on abort")
	}
	// heldPeer/addingPeer should be cleared after abort.
	held, adding := c.heldPeerForTest(), c.addingPeerForTest()
	if held != "" || adding != "" {
		t.Fatalf("flash 2: expected heldPeer and addingPeer cleared, got held=%q adding=%q", held, adding)
	}

	// Flash 3: CONNECTED -> ADD_DIALTONE again.
	c.HandleHookFlash("5550002")
	if c.State() != StateADD_DIALTONE {
		t.Fatalf("flash 3: expected StateADD_DIALTONE, got %v", c.State())
	}
	if !mock.peerMuted("5550002") {
		t.Fatalf("flash 3: expected B muted again")
	}

	// Flash 4: ADD_DIALTONE -> CONNECTED (abort again).
	c.HandleHookFlash("")
	if c.State() != StateCONNECTED {
		t.Fatalf("flash 4: expected StateCONNECTED, got %v", c.State())
	}
	if mock.peerMuted("5550002") {
		t.Fatalf("flash 4: expected B unmuted on second abort")
	}
	held, adding = c.heldPeerForTest(), c.addingPeerForTest()
	if held != "" || adding != "" {
		t.Fatalf("flash 4: expected heldPeer and addingPeer cleared, got held=%q adding=%q", held, adding)
	}
}

// TestController_FlashRaceWithAnswerFlashFirst simulates the user flashing
// out of ADD_CALLING at the exact moment the added party's answer arrives.
// The flash wins: state -> CONNECTED (via abortAdd) and C is torn
// down. A subsequent answer signal from the now-defunct C must be ignored;
// the controller must not fall into ADD_PRIVATE or CONFERENCE_MERGED on
// stale input.
func TestController_FlashRaceWithAnswerFlashFirst(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	c.setStateForTest(StateADD_CALLING)
	c.setHeldPeerForTest("5550002")
	c.setAddingPeerForTest("5550003")

	// Flash first -- aborts the add attempt.
	c.HandleHookFlash("")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED after flash-abort, got %v", c.State())
	}
	if !mock.peerTorndown("5550003") {
		t.Fatalf("expected C torn down on flash-abort")
	}
	if mock.peerMuted("5550002") {
		t.Fatalf("expected B unmuted on flash-abort")
	}

	// Now a stale answer from the torn-down C arrives. Must not transition.
	c.HandleSignal("answer", "5550003")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED to persist on stale answer, got %v", c.State())
	}
}

// TestController_AbortFromInterceptAfterSelfDial covers the path where
// dialThirdParty rejected the input locally (self-dial or blocked contact)
// and landed in ADD_INTERCEPT without ever calling InitiateCall, so no add
// peer was created. Flashing out of ADD_INTERCEPT must recover cleanly
// without crashing on a TearDownPeer for an empty addingPeer.
func TestController_AbortFromInterceptAfterSelfDial(t *testing.T) {
	mock := &mockCallbacks{}
	c := newTestController(mock, "5550001")
	// Simulate the post-self-dial state: dialThirdParty set intercept but
	// did not run InitiateCall, so addingPeer stays empty.
	c.setStateForTest(StateADD_INTERCEPT)
	c.setHeldPeerForTest("5550002")
	// c.addingPeer is intentionally empty here.

	// Flash to return. Must not crash, must restore CONNECTED, must unmute B.
	c.HandleHookFlash("")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected StateCONNECTED, got %v", c.State())
	}
	if mock.peerMuted("5550002") {
		t.Fatalf("expected B unmuted")
	}
	// No TearDownPeer should have been called for an empty phone -- the
	// guard in abortAdd skips the call when addingPeer is "".
	for _, p := range mock.torndownPeers {
		if p == "" {
			t.Fatalf("TearDownPeer called with empty phone; guard missing")
		}
	}
}

// Silent mode suppresses the bell but keeps LED + state transition.
func TestController_IncomingRingSilentModeSuppressesBell(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")
	c.SetSilentMode(true) // seeded from config at startup

	c.HandleSignal("ring", "")

	if c.State() != StateRINGING {
		t.Fatalf("state: got %s, want RINGING", c.State())
	}
	for _, r := range cb.Rings() {
		if r == true {
			t.Error("expected no StartRing while silent")
		}
	}
	if len(cb.LEDs()) == 0 || cb.LEDs()[0] != "BLINK" {
		t.Errorf("expected SendLED(BLINK), got %v", cb.LEDs())
	}
}

// Silent off behaves exactly like today.
func TestController_IncomingRingSilentOffBehavesNormally(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")
	c.SetSilentMode(false)

	c.HandleSignal("ring", "")

	if c.State() != StateRINGING {
		t.Fatalf("state: got %s", c.State())
	}
	if len(cb.Rings()) == 0 || cb.Rings()[0] != true {
		t.Errorf("expected StartRing, got %v", cb.Rings())
	}
	if len(cb.LEDs()) == 0 || cb.LEDs()[0] != "BLINK" {
		t.Errorf("expected SendLED(BLINK), got %v", cb.LEDs())
	}
}

// Flipping silent ON mid-ring stops the bell immediately. LED stays on.
// State stays RINGING so offhook still answers normally.
func TestController_SetSilentModeOnDuringRingStopsBell(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")

	c.HandleSignal("ring", "") // state RINGING, StartRing, SendLED("BLINK")
	if c.State() != StateRINGING {
		t.Fatalf("precondition: state %s != RINGING", c.State())
	}

	c.SetSilentMode(true)

	last := cb.Rings()[len(cb.Rings())-1]
	if last != false {
		t.Errorf("expected StopRing after silent=true mid-ring, got %v", cb.Rings())
	}
	if c.State() != StateRINGING {
		t.Errorf("state changed unexpectedly: %s (should stay RINGING)", c.State())
	}
	lastLED := cb.LEDs()[len(cb.LEDs())-1]
	if lastLED == "OFF" {
		t.Errorf("LED turned OFF on silent=true mid-ring; should stay BLINK")
	}
}

// Flipping silent OFF mid-ring does NOT start the bell. The asymmetry is
// intentional: a mid-ring bell start is jarring.
func TestController_SetSilentModeOffDuringRingDoesNotStartBell(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")
	c.SetSilentMode(true)

	c.HandleSignal("ring", "") // silent: no StartRing, only LED:BLINK
	ringsBefore := len(cb.Rings())

	c.SetSilentMode(false)

	if len(cb.Rings()) != ringsBefore {
		t.Errorf("unexpected ring call on silent=false mid-ring: %v", cb.Rings())
	}
}

// Flipping silent while idle changes state with no hardware side-effect.
func TestController_SetSilentModeIdleIsSideEffectFree(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")

	c.SetSilentMode(true)
	c.SetSilentMode(false)
	c.SetSilentMode(true)

	if len(cb.Rings()) != 0 || len(cb.LEDs()) != 0 {
		t.Errorf("expected no callbacks while idle, got rings=%v leds=%v", cb.Rings(), cb.LEDs())
	}
}

// TestController_Reset verifies Reset() drops back to IDLE with no digits.
func TestController_Reset(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:5")
	if c.State() != StateDIALING {
		t.Fatalf("setup: expected DIALING, got %s", c.State())
	}

	c.Reset()
	if c.State() != StateIDLE {
		t.Errorf("expected IDLE after Reset, got %s", c.State())
	}
	if c.digits != "" {
		t.Errorf("expected empty digits after Reset, got %q", c.digits)
	}
}

// TestController_ResetToDialtone_NextKeyStopsTone is the regression test for
// the volume-code dialtone bug: after entering *#*N, the daemon callback
// restarts the dial-tone loop, and the FSM was being forced to IDLE which
// silently ignored every subsequent key press, leaving the resumed dial tone
// playing forever.
//
// Now ResetToDialtone() puts the FSM back in DIALTONE so the next key press
// transitions to DIALING and emits SendTone(STOP) just like the initial
// off-hook sequence.
func TestController_ResetToDialtone_NextKeyStopsTone(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "")

	// Simulate user picking up and typing partial code; controller is now in
	// DIALING with some buffered digits.
	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:#")
	c.HandleEvent("KEY:*")
	if c.State() != StateDIALING {
		t.Fatalf("setup: expected DIALING, got %s", c.State())
	}

	// Volume code matched in main.go: callback restarted dial tone loop, and
	// the event loop calls ResetToDialtone() instead of Reset().
	c.ResetToDialtone()
	if c.State() != StateDIALTONE {
		t.Fatalf("expected DIALTONE after ResetToDialtone, got %s", c.State())
	}
	if c.digits != "" {
		t.Errorf("expected empty digits after ResetToDialtone, got %q", c.digits)
	}

	// Snapshot tone count, then press a key. The FSM must transition back to
	// DIALING and emit STOP so the resumed dial-tone loop is silenced.
	tonesBefore := len(cb.Tones())
	c.HandleEvent("KEY:7")
	if c.State() != StateDIALING {
		t.Fatalf("expected DIALING after key press in DIALTONE, got %s", c.State())
	}
	tones := cb.Tones()
	if len(tones) <= tonesBefore {
		t.Fatal("expected SendTone(STOP) on first key after ResetToDialtone")
	}
	if last := tones[len(tones)-1]; last != ToneStop {
		t.Errorf("expected last tone to be STOP, got %q", last)
	}
	if c.digits != "7" {
		t.Errorf("expected digits=%q after key, got %q", "7", c.digits)
	}
}

// TestController_RingTimeoutFiresAutoAnswer verifies that when voicemail is
// enabled and the ring-timeout expires, the controller transitions to
// VOICEMAIL_GREETING and calls VoicemailAutoAnswer.
func TestController_RingTimeoutFiresAutoAnswer(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) {
			return true, 50 * time.Millisecond
		},
	}
	c := newTestController(cb, "")
	defer c.Close()

	c.HandleSignal("ring", "")
	if c.State() != StateRINGING {
		t.Fatalf("expected RINGING, got %s", c.State())
	}

	time.Sleep(150 * time.Millisecond)

	if c.State() != StateVOICEMAIL_GREETING {
		t.Errorf("expected VOICEMAIL_GREETING after timeout, got %s", c.State())
	}
	if cb.VoicemailAutoAnswers() != 1 {
		t.Errorf("expected 1 VoicemailAutoAnswer, got %d", cb.VoicemailAutoAnswers())
	}
}

// TestController_RingTimeoutCanceledByHookOff verifies that picking up the
// handset during ringing cancels the voicemail auto-answer goroutine.
func TestController_RingTimeoutCanceledByHookOff(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) {
			return true, 100 * time.Millisecond
		},
	}
	c := newTestController(cb, "")
	defer c.Close()

	c.HandleSignal("ring", "")
	c.HandleEvent("HOOK:OFF")

	time.Sleep(200 * time.Millisecond)

	if cb.VoicemailAutoAnswers() != 0 {
		t.Errorf("expected 0 VoicemailAutoAnswer after hookoff cancel, got %d", cb.VoicemailAutoAnswers())
	}
	if c.State() != StateCONNECTED {
		t.Errorf("expected CONNECTED after HOOK:OFF, got %s", c.State())
	}
}

// TestController_RingTimeoutCanceledByHangup verifies that a hangup signal
// during ringing cancels the voicemail auto-answer goroutine.
func TestController_RingTimeoutCanceledByHangup(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) {
			return true, 100 * time.Millisecond
		},
	}
	c := newTestController(cb, "")
	defer c.Close()

	c.HandleSignal("ring", "")
	c.HandleSignal("hangup", "")

	time.Sleep(200 * time.Millisecond)

	if cb.VoicemailAutoAnswers() != 0 {
		t.Errorf("expected 0 VoicemailAutoAnswer after hangup cancel, got %d", cb.VoicemailAutoAnswers())
	}
	if c.State() != StateIDLE {
		t.Errorf("expected IDLE after hangup, got %s", c.State())
	}
}

// TestController_VoicemailDisabledNoTimeout verifies that when voicemail is
// disabled, the ring-timeout goroutine is never spawned and the phone stays
// in RINGING after the timeout window passes.
func TestController_VoicemailDisabledNoTimeout(t *testing.T) {
	cb := &mockCallbacks{} // voicemailEnabled is nil -> returns false, 0
	c := newTestController(cb, "")
	defer c.Close()

	c.HandleSignal("ring", "")

	time.Sleep(150 * time.Millisecond)

	if c.State() != StateRINGING {
		t.Errorf("expected still RINGING with voicemail disabled, got %s", c.State())
	}
	if cb.VoicemailAutoAnswers() != 0 {
		t.Errorf("expected 0 VoicemailAutoAnswer with voicemail disabled, got %d", cb.VoicemailAutoAnswers())
	}
}

// TestController_VoicemailPickupDuringGreeting verifies that picking up the
// handset during VOICEMAIL_GREETING transitions to CONNECTED and calls VoicemailPickup
// without calling AnswerCall (voicemail already answered the call).
func TestController_VoicemailPickupDuringGreeting(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 50 * time.Millisecond },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleSignal("ring", "")
	time.Sleep(100 * time.Millisecond)
	if c.State() != StateVOICEMAIL_GREETING {
		t.Fatalf("expected VOICEMAIL_GREETING, got %s", c.State())
	}

	c.HandleEvent("HOOK:OFF")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED after pickup, got %s", c.State())
	}
	if cb.VoicemailPickups() != 1 {
		t.Errorf("expected 1 VoicemailPickup, got %d", cb.VoicemailPickups())
	}
	if cb.Answers() != 0 {
		t.Errorf("expected 0 AnswerCall (voicemail already answered), got %d", cb.Answers())
	}
}

// TestController_VoicemailPickupDuringRecording verifies that picking up the
// handset during VOICEMAIL_RECORDING also transitions to CONNECTED and calls VoicemailPickup.
func TestController_VoicemailPickupDuringRecording(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 50 * time.Millisecond },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleSignal("ring", "")
	time.Sleep(100 * time.Millisecond)

	c.SetVoicemailRecording()
	if c.State() != StateVOICEMAIL_RECORDING {
		t.Fatalf("expected VOICEMAIL_RECORDING, got %s", c.State())
	}

	c.HandleEvent("HOOK:OFF")
	if c.State() != StateCONNECTED {
		t.Fatalf("expected CONNECTED after pickup during recording, got %s", c.State())
	}
	if cb.VoicemailPickups() != 1 {
		t.Errorf("expected 1 VoicemailPickup, got %d", cb.VoicemailPickups())
	}
}

// TestController_VoicemailCallerHangupDuringRecording verifies that a hangup
// signal during VOICEMAIL_RECORDING transitions to IDLE and calls HangupCall.
func TestController_VoicemailCallerHangupDuringRecording(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 50 * time.Millisecond },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleSignal("ring", "")
	time.Sleep(100 * time.Millisecond)
	c.SetVoicemailRecording()

	c.HandleSignal("hangup", "")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after caller hangup, got %s", c.State())
	}
	if cb.Hangups() != 1 {
		t.Errorf("expected 1 HangupCall, got %d", cb.Hangups())
	}
}

// TestController_SecondRingNewTimeout verifies that after a hangup cancels the
// first ring-timeout goroutine, a second incoming ring spawns a fresh one that
// fires exactly once.
func TestController_SecondRingNewTimeout(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) {
			return true, 80 * time.Millisecond
		},
	}
	c := newTestController(cb, "")
	defer c.Close()

	// First ring: let it start then cancel via hangup.
	c.HandleSignal("ring", "")
	c.HandleSignal("hangup", "")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after first hangup, got %s", c.State())
	}

	// Second ring: should spawn a fresh goroutine.
	c.HandleSignal("ring", "")
	if c.State() != StateRINGING {
		t.Fatalf("expected RINGING after second ring signal, got %s", c.State())
	}

	time.Sleep(200 * time.Millisecond)

	if cb.VoicemailAutoAnswers() != 1 {
		t.Errorf("expected exactly 1 VoicemailAutoAnswer, got %d", cb.VoicemailAutoAnswers())
	}
	if c.State() != StateVOICEMAIL_GREETING {
		t.Errorf("expected VOICEMAIL_GREETING, got %s", c.State())
	}
}

// TestController_Star97EntersRecordGreeting verifies that dialing *97 from
// DIALTONE enters StateVOICEMAIL_RECORD_GREETING and fires the
// VoicemailRecordGreeting callback.
func TestController_Star97EntersRecordGreeting(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:7")

	if c.State() != StateVOICEMAIL_RECORD_GREETING {
		t.Fatalf("expected VOICEMAIL_RECORD_GREETING, got %s", c.State())
	}
	// Callback fires from a goroutine; give it a moment.
	for i := 0; i < 20; i++ {
		if cb.VoicemailRecordGreetings() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cb.VoicemailRecordGreetings() != 1 {
		t.Errorf("expected 1 VoicemailRecordGreeting call, got %d", cb.VoicemailRecordGreetings())
	}
}

// TestController_Star99DeletesGreeting verifies that dialing *99 from
// DIALTONE returns to DIALTONE (with dial tone restored) and fires the
// VoicemailDeleteGreeting callback. Delete uses *99 (not *970) because *97
// would prefix-conflict with the record code, which fires immediately on the
// third digit.
func TestController_Star99DeletesGreeting(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:9")

	if c.State() != StateDIALTONE {
		t.Fatalf("expected DIALTONE after *99, got %s", c.State())
	}
	for i := 0; i < 20; i++ {
		if cb.VoicemailDeleteGreetings() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cb.VoicemailDeleteGreetings() != 1 {
		t.Errorf("expected 1 VoicemailDeleteGreeting call, got %d", cb.VoicemailDeleteGreetings())
	}
}

// TestController_HashFinishesGreetingRecord verifies that DTMF keys pressed
// while in VOICEMAIL_RECORD_GREETING are forwarded to VoicemailRecordGreetingKey.
// The "#" key in particular is the convention to end the recording, but the
// FSM just forwards all digits and lets the daemon decide which one terminates.
func TestController_HashFinishesGreetingRecord(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:7")

	if c.State() != StateVOICEMAIL_RECORD_GREETING {
		t.Fatalf("expected VOICEMAIL_RECORD_GREETING, got %s", c.State())
	}

	c.HandleEvent("KEY:#")
	for i := 0; i < 20; i++ {
		if len(cb.VoicemailRecordKeys()) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	keys := cb.VoicemailRecordKeys()
	if len(keys) != 1 || keys[0] != "#" {
		t.Errorf("expected VoicemailRecordGreetingKey(#), got %v", keys)
	}
}

// TestController_HookOnDuringGreetingRecord verifies that hanging up while
// recording the greeting returns the FSM to IDLE and calls HangupCall, which
// the daemon side uses to finalize the partial greeting recording and stop
// the audio pipeline.
func TestController_HookOnDuringGreetingRecord(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:7")

	c.HandleEvent("HOOK:ON")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after hook-on, got %s", c.State())
	}
	if cb.Hangups() != 1 {
		t.Errorf("expected 1 HangupCall (so daemon can finalize partial greeting), got %d", cb.Hangups())
	}
}

// TestController_Star96AuditionsGreeting verifies that dialing *96 from
// DIALTONE enters StateVOICEMAIL_PLAY_GREETING and fires the
// VoicemailPlayGreeting callback, without initiating an outbound call.
func TestController_Star96AuditionsGreeting(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:6")

	if c.State() != StateVOICEMAIL_PLAY_GREETING {
		t.Fatalf("expected VOICEMAIL_PLAY_GREETING, got %s", c.State())
	}
	// Callback fires from a goroutine; give it a moment.
	for i := 0; i < 20; i++ {
		if cb.VoicemailPlayGreetings() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cb.VoicemailPlayGreetings() != 1 {
		t.Errorf("expected 1 VoicemailPlayGreeting call, got %d", cb.VoicemailPlayGreetings())
	}
	if len(cb.Calls()) != 0 {
		t.Errorf("expected no InitiateCall, got %v", cb.Calls())
	}
}

// TestController_Star96IgnoredWhenDisabled verifies that with voicemail
// disabled, dialing *96 does not audition; the controller keeps accumulating
// digits as a normal dialed prefix.
func TestController_Star96IgnoredWhenDisabled(t *testing.T) {
	cb := &mockCallbacks{} // VoicemailEnabled returns false by default
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:6")

	if got := cb.VoicemailPlayGreetings(); got != 0 {
		t.Errorf("expected 0 VoicemailPlayGreeting when voicemail disabled, got %d", got)
	}
	if c.State() != StateDIALING {
		t.Errorf("expected DIALING (still accumulating), got %s", c.State())
	}
	if got := c.digitsForTest(); got != "*96" {
		t.Errorf("expected digits to accumulate as %q, got %q", "*96", got)
	}
}

// TestController_HookOnDuringGreetingAudition verifies that hanging up while
// auditioning the greeting returns the FSM to IDLE and fires
// VoicemailExitGreetingPlayback (so the daemon stops the one-shot playback)
// without routing through HangupCall, since *96 has no WebRTC peer or recorder.
func TestController_HookOnDuringGreetingAudition(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:6")

	if c.State() != StateVOICEMAIL_PLAY_GREETING {
		t.Fatalf("expected VOICEMAIL_PLAY_GREETING, got %s", c.State())
	}

	c.HandleEvent("HOOK:ON")
	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after hook-on, got %s", c.State())
	}
	if got := cb.GreetingPlaybackExits(); got != 1 {
		t.Errorf("expected 1 VoicemailExitGreetingPlayback, got %d", got)
	}
	if cb.Hangups() != 0 {
		t.Errorf("expected no HangupCall for *96 audition, got %d", cb.Hangups())
	}
}

// TestController_FinishGreetingAudition verifies the atomic completion helper:
// from StateVOICEMAIL_PLAY_GREETING it transitions to DIALTONE, re-arms the
// dial tone, and reports true; from any other state it reports false and
// changes nothing, so a hook-on that already left the audition wins the race.
func TestController_FinishGreetingAudition(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:6")
	if c.State() != StateVOICEMAIL_PLAY_GREETING {
		t.Fatalf("expected VOICEMAIL_PLAY_GREETING, got %s", c.State())
	}

	if !c.FinishGreetingAudition() {
		t.Fatal("expected FinishGreetingAudition to return true from audition state")
	}
	if c.State() != StateDIALTONE {
		t.Errorf("expected DIALTONE after FinishGreetingAudition, got %s", c.State())
	}
	tones := cb.Tones()
	if len(tones) == 0 || tones[len(tones)-1] != ToneDial {
		t.Errorf("expected dial tone re-armed, tones=%v", tones)
	}

	// A second call (state is now DIALTONE) must be a no-op returning false:
	// this models a hook-on having already moved the FSM out of the audition.
	if c.FinishGreetingAudition() {
		t.Error("expected FinishGreetingAudition to return false outside audition state")
	}
	if c.State() != StateDIALTONE {
		t.Errorf("expected state unchanged at DIALTONE, got %s", c.State())
	}
}

// TestRetrievalCodeIntercept verifies that dialing the fixed retrieval code
// (*98) from off-hook DIALING enters VOICEMAIL_PLAYBACK and fires
// VoicemailEnterPlayback exactly once, without initiating an outbound call.
func TestRetrievalCodeIntercept(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:8")

	if got := cb.PlaybackEnters(); got != 1 {
		t.Errorf("expected 1 VoicemailEnterPlayback, got %d", got)
	}
	if c.State() != StateVOICEMAIL_PLAYBACK {
		t.Errorf("expected VOICEMAIL_PLAYBACK after *98, got %s", c.State())
	}
	if len(cb.Calls()) != 0 {
		t.Errorf("expected no InitiateCall, got %v", cb.Calls())
	}
}

// TestVoicemailPlaybackPicoHoldAndRelease verifies the Pico key-forwarding
// hold around a peerless *98 session: entering playback moves the Pico to
// CONNECTED (NotifyCallConnected) so it keeps forwarding DTMF past its 15s
// partial-dial timeout, and the exit-to-dialtone path releases that hold
// (NotifyPicoReset) so the next call can be dialed without a hook cycle.
func TestVoicemailPlaybackPicoHoldAndRelease(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:8")

	if got := cb.CallConnectedCalls(); got != 1 {
		t.Fatalf("expected 1 NotifyCallConnected (Pico hold) on *98 entry, got %d", got)
	}
	if got := cb.PicoResets(); got != 0 {
		t.Fatalf("expected no NotifyPicoReset before exit, got %d", got)
	}

	c.ResetToDialtone()

	if got := cb.PicoResets(); got != 1 {
		t.Fatalf("expected 1 NotifyPicoReset on exit from playback, got %d", got)
	}
	if c.State() != StateDIALTONE {
		t.Errorf("expected DIALTONE after reset, got %s", c.State())
	}
}

// TestRecordGreetingPicoHold verifies that *97 greeting record takes the same
// Pico hold (so "#" still terminates after 15s) and releases it on exit.
func TestRecordGreetingPicoHold(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:7")

	if c.State() != StateVOICEMAIL_RECORD_GREETING {
		t.Fatalf("expected VOICEMAIL_RECORD_GREETING after *97, got %s", c.State())
	}
	if got := cb.CallConnectedCalls(); got != 1 {
		t.Fatalf("expected 1 NotifyCallConnected (Pico hold) on *97 entry, got %d", got)
	}

	c.ResetToDialtone()

	if got := cb.PicoResets(); got != 1 {
		t.Fatalf("expected 1 NotifyPicoReset on exit from record greeting, got %d", got)
	}
}

// TestResetToDialtoneNoPicoResetOutsideVoicemail guards the release: a plain
// ResetToDialtone from a non-voicemail state (e.g. mid-dial) must NOT poke the
// Pico, since no CALL:CONNECTED hold was taken for those flows.
func TestResetToDialtoneNoPicoResetOutsideVoicemail(t *testing.T) {
	cb := &mockCallbacks{}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.setStateForTest(StateDIALING)
	c.ResetToDialtone()

	if got := cb.PicoResets(); got != 0 {
		t.Errorf("expected no NotifyPicoReset from non-voicemail ResetToDialtone, got %d", got)
	}
}

// TestRetrievalCodeIgnoredWhenDisabled verifies that with voicemail disabled,
// dialing *98 does NOT intercept; the controller keeps accumulating digits.
// The legacy onKey path treats *98 as a normal dialed prefix; no playback
// enter, no state change to VOICEMAIL_PLAYBACK.
func TestRetrievalCodeIgnoredWhenDisabled(t *testing.T) {
	cb := &mockCallbacks{} // VoicemailEnabled returns false by default
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:8")

	if got := cb.PlaybackEnters(); got != 0 {
		t.Errorf("expected 0 VoicemailEnterPlayback when voicemail disabled, got %d", got)
	}
	if c.State() != StateDIALING {
		t.Errorf("expected DIALING (still accumulating), got %s", c.State())
	}
	if got := c.digitsForTest(); got != "*98" {
		t.Errorf("expected digits to accumulate as %q, got %q", "*98", got)
	}
}

// TestPlaybackKeysDispatched verifies that DTMF keys received while in
// VOICEMAIL_PLAYBACK are forwarded to VoicemailKey in order, with no state
// change.
func TestPlaybackKeysDispatched(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.setStateForTest(StateVOICEMAIL_PLAYBACK)

	// VoicemailKey runs in a goroutine to escape c.mu (so the daemon's impl
	// can safely call ctrl.ResetToDialtone). Wait for each key to arrive
	// before sending the next so the asserted slice is in send-order.
	want := []string{"7", "9", "#", "*"}
	for i, d := range want {
		c.HandleEvent("KEY:" + d)
		expected := i + 1
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(cb.PlaybackKeys()) >= expected {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if got := len(cb.PlaybackKeys()); got != expected {
			t.Fatalf("after sending %q expected %d playback keys, got %d: %v", d, expected, got, cb.PlaybackKeys())
		}
	}
	got := cb.PlaybackKeys()
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("playback key %d: expected %q, got %q (full: %v)", i, want[i], got[i], got)
		}
	}
	if c.State() != StateVOICEMAIL_PLAYBACK {
		t.Errorf("expected state to remain VOICEMAIL_PLAYBACK, got %s", c.State())
	}
}

// TestPlaybackHookOnExits verifies that hanging up during playback fires
// VoicemailExitPlayback, returns to IDLE, and does NOT trigger HangupCall
// (no WebRTC peer involved).
func TestPlaybackHookOnExits(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.setStateForTest(StateVOICEMAIL_PLAYBACK)
	c.HandleEvent("HOOK:ON")

	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after HOOK:ON, got %s", c.State())
	}
	if got := cb.PlaybackExits(); got != 1 {
		t.Errorf("expected 1 VoicemailExitPlayback, got %d", got)
	}
	if cb.Hangups() != 0 {
		t.Errorf("expected 0 HangupCall (no WebRTC peer in playback), got %d", cb.Hangups())
	}
}

// TestPlaybackEntryClearsDigits verifies that the dial buffer is cleared when
// the retrieval-code intercept fires. Without this, a subsequent re-entry into
// DIALING would inherit stale digits and mis-route.
func TestPlaybackEntryClearsDigits(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) { return true, 20 * time.Second },
	}
	c := newTestController(cb, "5551000")
	defer c.Close()

	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:*")
	c.HandleEvent("KEY:9")
	c.HandleEvent("KEY:8")

	if got := c.digitsForTest(); got != "" {
		t.Errorf("expected digits to be cleared after playback entry, got %q", got)
	}
}

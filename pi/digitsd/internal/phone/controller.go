package phone

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/signal"
)

type State string

const (
	StateIDLE            State = "IDLE"
	StateDIALTONE        State = "DIAL_TONE"
	StateDIALING         State = "DIALING"
	StateCALLING         State = "CALLING"
	StateRINGING         State = "RINGING"
	StateCONNECTED       State = "CONNECTED"
	StateREMOTE_HANGUP   State = "REMOTE_HANGUP"   // Far end hung up; handset still off-hook
	StateOFFHOOK_TIMEOUT State = "OFFHOOK_TIMEOUT" // Off-hook with no dialing; CO permanent-signal treatment
	StateADD_DIALTONE    State = "ADD_DIAL_TONE"   // Flash from CONNECTED: B on hold, dialing C
	StateADD_DIALING     State = "ADD_DIALING"     // Collecting digits for C
	StateADD_CALLING     State = "ADD_CALLING"     // Ringing C; B still on hold
	StateADD_PRIVATE     State = "ADD_PRIVATE"     // A↔C connected; B on hold
	StateADD_INTERCEPT   State = "ADD_INTERCEPT"   // Add-leg failed (busy, timeout, refused); B on hold, flash to recover
	StateCONFERENCE_MERGED State = "CONFERENCE_MERGED" // Three-way call active
)

// Tone names passed to Callbacks.SendTone. Mixer/daemon dispatch on these.
const (
	ToneDial         = "DIAL"
	ToneRingback     = "RINGBACK"
	ToneBusy         = "BUSY"
	ToneReorder      = "REORDER"
	ToneHowler       = "HOWLER"
	ToneIntercept    = "INTERCEPT"    // generic SIT + "please try again" (service unavailable)
	ToneDisconnected = "DISCONNECTED" // SIT + "number you have dialed is not in service" (misdial)
	ToneStop         = "STOP"
	ToneStopAll      = "STOPALL"
)

// addDialDigitsRequired is the number of digits the add-party dial accumulates
// before firing dialThirdParty. Must match the Pico firmware's
// DIAL_DIGITS_REQUIRED, since the 2-party flow still relies on the Pico to
// emit DIAL:<number> at that length. In the add flow the Pico stays in its
// CONNECTED state (to keep KEY events flowing for DTMF) and never emits DIAL,
// so the controller self-fires here once it has the full number.
const addDialDigitsRequired = 7

// Callbacks is the interface the controller uses to drive hardware and network.
type Callbacks interface {
	SendTone(name string)       // Play a tone (use one of the Tone* constants)
	OncePlaying() bool          // Reports whether a one-shot tone (e.g. intercept) is still playing
	SendRing(start bool)        // Send RING:START or RING:STOP
	SendLED(mode string)        // Send LED:<mode>
	SetFlashEnabled(enabled bool) // Enable/disable Pico hook-flash detection (off = instant hangup)
	InitiateCall(number string) error // Start outgoing WebRTC call
	AnswerCall()                // Accept incoming WebRTC call
	HangupCall()                // Tear down WebRTC call
	NotifyCallConnected()       // Notify the Pico that the WebRTC peer answered
	MutePeer(phone string, muted bool)                    // Mute or unmute the A↔B audio path for a peer
	TearDownPeer(phone string)                            // Hang up and tear down the connection to a peer
	MigrateToMesh(phone string)                           // Move the 2-party peer into the mesh under this key
	RequestConferenceMerge(held, active string)           // Request server-side three-way merge
	AddMeshPeer(phone string, initiator bool)             // Open a WebRTC peer connection to a conference member
	RemoveMeshPeer(phone string)                          // Tear down the WebRTC connection to a conference member
	TearDownAllMeshPeers()                                // Tear down all conference WebRTC connections
}

// ContactChecker determines whether a number is in the local contact list.
// When nil, all numbers are allowed (backward compatibility).
type ContactChecker interface {
	IsContact(number string) bool
}

type Controller struct {
	mu             sync.Mutex
	state          State
	cb             Callbacks
	digits         string
	ownNumber      string
	contactChecker ContactChecker
	silentMode     bool // when true, onSignalRing suppresses SendRing(true)
	// treatmentGen is incremented each time runPermanentSignalTreatment starts.
	// The spawned goroutine captures the value and aborts on mismatch, so a
	// hook-flap that re-enters off-hook treatment within ~4 min won't let the
	// previous goroutine step the new session forward.
	treatmentGen uint64
	// done is closed by Close(); long-lived goroutines (off-hook treatment) abort
	// their sleeps when it fires so daemon shutdown isn't blocked.
	done      chan struct{}
	closeOnce sync.Once

	// Conference / call-waiting state.
	confID     string // non-empty when part of a conference (Member message received)
	isConfHost bool   // true if this controller is the host of the conference
	heldPeer   string // phone number of the held party (B) during ADD_*
	addingPeer string // phone number of the third party (C) during ADD_CALLING/PRIVATE
}

// NewController creates a new Controller starting in StateIDLE.
// ownNumber is this phone's number; dialing it will produce a busy tone.
func NewController(cb Callbacks, ownNumber string) *Controller {
	return &Controller{
		state:     StateIDLE,
		cb:        cb,
		ownNumber: ownNumber,
		done:      make(chan struct{}),
	}
}

// Close signals long-lived background goroutines (off-hook treatment) to exit
// their sleep loops promptly. Safe to call multiple times.
func (c *Controller) Close() {
	c.closeOnce.Do(func() { close(c.done) })
}

// sleepOrDone blocks for d, returning false if Close was called during the
// wait (so the caller can short-circuit its sequence).
func (c *Controller) sleepOrDone(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-c.done:
		return false
	}
}

// SetContactChecker sets the contact checker for dial filtering.
// If nil, all numbers are allowed.
func (c *Controller) SetContactChecker(cc ContactChecker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contactChecker = cc
}

// SetSilentMode updates the silent-mode flag. When newly true during an
// active ring, the current bell is stopped (LED keeps blinking). When newly
// false during an active ring, the bell does NOT start - the user would
// find a mid-ring bell start jarring, and they can answer if they want
// audio. Thread-safe.
func (c *Controller) SetSilentMode(silent bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.silentMode == silent {
		return
	}
	c.silentMode = silent
	if silent && c.state == StateRINGING {
		c.cb.SendRing(false)
	}
}

// State returns the current FSM state (thread-safe).
func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// IsCallActive reports whether the phone is currently in a call or actively ringing.
func (c *Controller) IsCallActive() bool {
	switch c.State() {
	case StateCALLING, StateRINGING, StateCONNECTED:
		return true
	default:
		return false
	}
}

// Reset forces the controller back to IDLE with no pending digits.
// Used after terminal service codes (shutdown, reboot, etc.) where the
// daemon is going down and the FSM state no longer matters.
func (c *Controller) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = StateIDLE
	c.digits = ""
}

// ResetToDialtone forces the controller back to DIALTONE with no pending
// digits. Only updates FSM state; the caller owns the tone.
func (c *Controller) ResetToDialtone() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = StateDIALTONE
	c.digits = ""
}

// HandleEvent processes UART events from the Pico (e.g. "HOOK:OFF", "KEY:5", "DIAL:5551234").
func (c *Controller) HandleEvent(event string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Skip events without a colon separator (e.g. "PONG", bare acks)
	parts := strings.SplitN(event, ":", 2)
	if len(parts) != 2 {
		return // not an actionable event
	}
	evType, evVal := parts[0], parts[1]

	switch {
	case evType == "HOOK" && evVal == "OFF":
		c.onHookOff()
	case evType == "HOOK" && evVal == "ON":
		c.onHookOn()
	case evType == "KEY":
		c.onKey(evVal)
	case evType == "DIAL":
		c.onDial(evVal)
	case evType == "TIMEOUT" && evVal == "DIAL_TONE":
		c.onTimeoutDialTone()
	case evType == "RING" && (evVal == "ACK" || evVal == "DONE"):
		// Informational — ring ack/done from Pico, no action needed
	case event == "PONG":
		// Keepalive response, ignore
	default:
		slog.Warn("phone: unhandled event", "event", event)
	}
}

// HandleSignal processes signaling messages (e.g. "ring", "answer", "hangup", "busy").
// sender identifies which peer sent the signal; pass "" when the sender is unknown
// or irrelevant. It is used to route signals correctly during ADD_* states where
// two peers may be active.
func (c *Controller) HandleSignal(msgType, sender string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch msgType {
	case "ring":
		c.onSignalRing()
	case "answer":
		c.onSignalAnswer(sender)
	case "hangup":
		c.onSignalHangup(sender)
	case "busy":
		c.onSignalBusy(sender)
	case "error":
		c.onSignalError(sender)
	default:
		slog.Warn("phone: unhandled signal", "type", msgType)
	}
}

// --- internal event handlers (called with lock held) ---

func (c *Controller) onHookOff() {
	switch c.state {
	case StateIDLE:
		// Outgoing: pick up → dial tone
		c.state = StateDIALTONE
		c.digits = ""
		c.cb.SendTone(ToneDial)
		c.cb.SendLED("ON")
	case StateRINGING:
		// Incoming: answer the call; activePeer was set when the ring arrived.
		c.state = StateCONNECTED
		c.cb.SendRing(false)
		c.cb.SendTone(ToneStop)
		c.cb.SendLED("ON")
		c.cb.SetFlashEnabled(true)
		c.cb.AnswerCall()
	default:
		slog.Info("phone: HOOK:OFF ignored", "state", c.state)
	}
}

func (c *Controller) onHookOn() {
	if c.state == StateIDLE {
		return
	}
	wasConnectedOrCalling := c.state == StateCONNECTED || c.state == StateCALLING
	inConferenceFlow := c.confID != "" ||
		c.state == StateADD_DIALTONE ||
		c.state == StateADD_DIALING ||
		c.state == StateADD_CALLING ||
		c.state == StateADD_PRIVATE ||
		c.state == StateADD_INTERCEPT ||
		c.state == StateCONFERENCE_MERGED

	// Tear down mesh peers for any conference-related state before calling
	// HangupCall. This ensures B (migrated into mesh) and any added C are
	// closed immediately on hang-up rather than leaking until a ConferenceEnd
	// arrives from the server.
	if inConferenceFlow {
		c.cb.TearDownAllMeshPeers()
	}

	c.state = StateIDLE
	c.digits = ""
	c.heldPeer = ""
	c.addingPeer = ""
	c.confID = ""
	c.isConfHost = false
	c.cb.SendTone(ToneStop)
	c.cb.SendRing(false)
	c.cb.SendLED("OFF")
	if wasConnectedOrCalling || inConferenceFlow {
		c.cb.HangupCall()
	}
	// REMOTE_HANGUP / OFFHOOK_TIMEOUT: nothing to tear down, tones/LED cleaned up above.
}

func (c *Controller) onKey(digit string) {
	switch c.state {
	case StateIDLE:
		// Ignore key presses while idle
		return
	case StateDIALTONE:
		// First key: stop dial tone, start collecting digits
		c.state = StateDIALING
		c.digits = digit
		c.cb.SendTone(ToneStop)
	case StateDIALING:
		c.digits += digit
		// Service codes (*#*N) are handled by ServiceCodeHandler.
		// Reset dial buffer when we see 4+ chars starting with *#* so the FSM
		// doesn't try to dial the service code as a phone number.
		if len(c.digits) >= 4 && len(c.digits) <= 5 && strings.HasPrefix(c.digits, "*#*") {
			slog.Debug("phone: service code handled by bridge, resetting", "code", c.digits)
			c.digits = ""
			c.state = StateDIALTONE
			// Service code detected — reset to dial tone without re-sending tone
		}
	case StateADD_DIALTONE:
		// First key during add-dial: stop the second dial tone, start collecting.
		slog.Info("phone: ADD_DIALTONE -> ADD_DIALING", "digit", digit)
		c.state = StateADD_DIALING
		c.digits = digit
		c.cb.SendTone(ToneStop)
		if len(c.digits) >= addDialDigitsRequired {
			number := c.digits
			c.digits = ""
			c.dialThirdParty(number)
		}
	case StateADD_DIALING:
		c.digits += digit
		if len(c.digits) >= addDialDigitsRequired {
			number := c.digits
			c.digits = ""
			c.dialThirdParty(number)
		}
	default:
		slog.Debug("phone: key ignored", "digit", digit, "state", c.state)
	}
}

func (c *Controller) onDial(number string) {
	if c.state == StateADD_DIALING {
		c.dialThirdParty(number)
		return
	}
	if c.state != StateDIALING {
		slog.Info("phone: DIAL event ignored", "state", c.state)
		return
	}

	// Self-call: dialing your own number gets an immediate busy tone,
	// just like a real POTS line.
	if c.ownNumber != "" && number == c.ownNumber {
		slog.Info("phone: self-call detected, busy tone")
		c.state = StateCALLING
		c.cb.SendTone(ToneBusy)
		return
	}

	// Contact filter: if checker is set and number is not a contact,
	// play rejection sequence (mimics unreachable number) instead of calling.
	if c.contactChecker != nil && !c.contactChecker.IsContact(number) {
		slog.Info("phone: number not in contacts, rejecting", "number", number)
		c.state = StateCALLING
		c.cb.SendTone(ToneRingback)
		go c.playRejectSequence(StateCALLING)
		return
	}
	c.state = StateCALLING
	// Brief silence before ringback — simulates PSTN call setup delay.
	// Old rotary phones had a variable pause between last digit and first ring.
	go func() {
		time.Sleep(800 * time.Millisecond)
		c.mu.Lock()
		if c.state != StateCALLING {
			c.mu.Unlock()
			return
		}
		c.cb.SendTone(ToneRingback)
		err := c.cb.InitiateCall(number)
		c.mu.Unlock()

		if err != nil {
			slog.Info("phone: call failed, server unreachable", "number", number, "error", err)
			c.playRejectSequence(StateCALLING)
		}
	}()
}

// playRejectSequence plays the POTS intercept sequence: ringback is already
// playing from the caller, so wait 3s (simulates connection attempt), then
// SIT tones + busy. Runs without holding the controller lock and checks
// expectedState before each step so a hang-up aborts cleanly.
func (c *Controller) playRejectSequence(expectedState State) {
	checkState := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.state == expectedState
	}

	time.Sleep(3 * time.Second)
	if !checkState() {
		return
	}

	c.cb.SendTone(ToneStop)
	c.cb.SendTone(ToneIntercept)
	deadline := time.Now().Add(15 * time.Second)
	for c.cb.OncePlaying() {
		time.Sleep(200 * time.Millisecond)
		if !checkState() {
			return
		}
		if time.Now().After(deadline) {
			slog.Error("phone: intercept tone timeout, aborting rejection")
			return
		}
	}

	time.Sleep(500 * time.Millisecond)
	if !checkState() {
		return
	}
	c.cb.SendTone(ToneBusy)
}

// dialThirdParty initiates an outgoing call to C from ADD_DIALING state.
// Mirrors the final steps of onDial for the 2-party flow, including the same
// self-call and contact-filter guards.
func (c *Controller) dialThirdParty(number string) {
	// Self-call guard: dialing own number is immediately rejected.
	if c.ownNumber != "" && number == c.ownNumber {
		slog.Info("phone: dialThirdParty self-call detected, intercept")
		c.state = StateADD_INTERCEPT
		c.cb.SendTone(ToneIntercept)
		return
	}

	// Contact filter: reject if number is not in the allowed contact list.
	if c.contactChecker != nil && !c.contactChecker.IsContact(number) {
		slog.Info("phone: dialThirdParty number not in contacts, rejecting", "number", number)
		c.state = StateADD_INTERCEPT
		c.cb.SendTone(ToneIntercept)
		return
	}

	slog.Info("phone: ADD_DIALING -> ADD_CALLING", "adding", number)
	c.addingPeer = number
	c.digits = ""
	c.state = StateADD_CALLING
	// Brief silence before ringback — mirrors the 2-party outgoing call setup delay.
	go func() {
		time.Sleep(800 * time.Millisecond)
		c.mu.Lock()
		if c.state != StateADD_CALLING {
			c.mu.Unlock()
			return
		}
		c.cb.SendTone(ToneRingback)
		err := c.cb.InitiateCall(number)
		c.mu.Unlock()

		if err != nil {
			slog.Info("phone: add-call failed, server unreachable", "number", number, "error", err)
			c.mu.Lock()
			if c.state != StateADD_CALLING {
				c.mu.Unlock()
				return
			}
			c.state = StateADD_INTERCEPT
			c.mu.Unlock()
			c.cb.SendTone(ToneStop)
			c.cb.SendTone(ToneIntercept)
		}
	}()
}

func (c *Controller) onSignalRing() {
	if c.state != StateIDLE {
		slog.Info("phone: ring signal ignored (not IDLE)", "state", c.state)
		return
	}
	c.state = StateRINGING
	if !c.silentMode {
		c.cb.SendRing(true)
	}
	c.cb.SendLED("BLINK")
}

func (c *Controller) onSignalAnswer(sender string) {
	switch c.state {
	case StateCALLING:
		c.state = StateCONNECTED
		c.cb.SendTone(ToneStop)
		c.cb.SetFlashEnabled(true)
		c.cb.NotifyCallConnected()
	case StateADD_CALLING:
		if sender != "" && sender != c.addingPeer {
			slog.Info("phone: answer from unexpected peer in ADD_CALLING", "from", sender, "adding", c.addingPeer)
			return
		}
		slog.Info("phone: ADD_CALLING -> ADD_PRIVATE", "adding", c.addingPeer)
		c.state = StateADD_PRIVATE
		c.cb.SendTone(ToneStop)
	default:
		slog.Info("phone: answer signal ignored", "state", c.state)
	}
}

func (c *Controller) onSignalHangup(sender string) {
	switch c.state {
	case StateRINGING:
		// Caller hung up before we answered - stop ringing and return to idle.
		slog.Info("phone: caller hung up during ring - stopping ring")
		c.state = StateIDLE
		c.cb.SendRing(false)
		c.cb.SendLED("OFF")
	case StateCONNECTED:
		c.state = StateREMOTE_HANGUP
		c.cb.HangupCall()
		c.cb.SendTone(ToneStopAll)
		c.runPermanentSignalTreatment(StateREMOTE_HANGUP, "remote hangup")
	case StateADD_CALLING, StateADD_PRIVATE:
		if sender != "" && sender != c.addingPeer {
			slog.Info("phone: hangup from unexpected peer", "state", c.state, "from", sender, "adding", c.addingPeer)
			return
		}
		c.state = StateADD_INTERCEPT
		// Stop any looping tone (ringback in ADD_CALLING) before layering the
		// one-shot intercept; otherwise both play simultaneously.
		c.cb.SendTone(ToneStop)
		c.cb.SendTone(ToneIntercept)
		c.cb.TearDownPeer(c.addingPeer)
	default:
		slog.Info("phone: hangup signal ignored", "state", c.state)
	}
}

// onTimeoutDialTone handles the Pico's TIMEOUT:DIAL_TONE event: the user picked
// up the handset but never dialed. Same CO treatment as a remote hangup with
// the handset still off-hook.
func (c *Controller) onTimeoutDialTone() {
	if c.state != StateDIALTONE {
		slog.Info("phone: TIMEOUT:DIAL_TONE ignored", "state", c.state)
		return
	}
	c.state = StateOFFHOOK_TIMEOUT
	c.cb.SendTone(ToneStopAll)
	c.runPermanentSignalTreatment(StateOFFHOOK_TIMEOUT, "dial-tone timeout")
}

// runPermanentSignalTreatment plays the 90s POTS off-hook sequence
// (Bellcore GR-506-CORE permanent signal treatment) in a goroutine:
//   1. Brief silence (~1s CO processing delay)
//   2. Reorder tone (fast busy, 480+620 Hz) for ~45s
//   3. Howler/ROH tone (loud multi-freq) for ~3 min
//   4. Line lockout (silence until user hangs up)
//
// The sequence aborts at any step if the controller leaves treatmentState or
// if a newer treatment run has started (tracked via treatmentGen). Caller must
// hold c.mu and have already set c.state = treatmentState.
func (c *Controller) runPermanentSignalTreatment(treatmentState State, reason string) {
	c.treatmentGen++
	gen := c.treatmentGen
	slog.Info("phone: starting POTS off-hook sequence", "reason", reason)
	go func() {
		stillInTreatment := func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.state == treatmentState && c.treatmentGen == gen
		}

		if !c.sleepOrDone(1 * time.Second) || !stillInTreatment() {
			return
		}

		slog.Info("phone: off-hook sequence -- reorder tone", "reason", reason)
		c.cb.SendTone(ToneReorder)
		if !c.sleepOrDone(45 * time.Second) || !stillInTreatment() {
			return
		}

		slog.Info("phone: off-hook sequence -- howler tone", "reason", reason)
		c.cb.SendTone(ToneHowler)
		if !c.sleepOrDone(3 * time.Minute) || !stillInTreatment() {
			return
		}

		slog.Info("phone: off-hook sequence -- line lockout", "reason", reason)
		c.cb.SendTone(ToneStop)
	}()
}

// onSignalError handles server error replies to a call attempt. The server
// emits TypeError when the target isn't connected, isn't in the caller's
// contacts, or similar "cannot be completed" conditions. Plays the
// number-not-in-service announcement (SIT + recorded voice), matching the
// 2-party misdial treatment in main.go's TypeError handler.
func (c *Controller) onSignalError(sender string) {
	switch c.state {
	case StateCALLING:
		slog.Info("phone: error signal received -- call cannot be completed")
		// Stop ringback before layering the one-shot disconnected announcement.
		c.cb.SendTone(ToneStop)
		c.cb.SendTone(ToneDisconnected)
		// Stay in CALLING -- caller should hang up.
	case StateADD_CALLING:
		if sender != "" && sender != c.addingPeer {
			slog.Info("phone: error from unexpected peer in ADD_CALLING", "from", sender, "adding", c.addingPeer)
			return
		}
		c.state = StateADD_INTERCEPT
		c.cb.SendTone(ToneStop)
		c.cb.SendTone(ToneDisconnected)
		c.cb.TearDownPeer(c.addingPeer)
	default:
		slog.Info("phone: error signal ignored", "state", c.state)
	}
}

func (c *Controller) onSignalBusy(sender string) {
	switch c.state {
	case StateCALLING:
		slog.Info("phone: busy signal received -- call rejected")
		c.cb.SendTone(ToneBusy)
		// Stay in CALLING -- caller should hang up
	case StateADD_CALLING:
		if sender != "" && sender != c.addingPeer {
			slog.Info("phone: busy from unexpected peer in ADD_CALLING", "from", sender, "adding", c.addingPeer)
			return
		}
		c.state = StateADD_INTERCEPT
		// Subscriber busy -- play standard busy tone, matching 2-party CALLING+busy.
		// PlayLoop replaces the ringback loop atomically, so no explicit stop needed.
		c.cb.SendTone(ToneBusy)
		c.cb.TearDownPeer(c.addingPeer)
	default:
		slog.Info("phone: busy signal ignored", "state", c.state)
	}
}

// HandleHookFlash dispatches a HOOK:FLASH event per 90s residential TWC semantics.
// activePeer is the phone number of the currently connected 2-party peer; it is
// captured from the daemon's callPeer field at the moment of the flash. Pass ""
// if no 2-party peer is active.
func (c *Controller) HandleHookFlash(activePeer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onHookFlash(activePeer)
}

// onHookFlash dispatches the HOOK:FLASH event per 90s residential TWC semantics.
// Called with c.mu already held.
func (c *Controller) onHookFlash(activePeer string) {
	slog.Info("phone: HOOK:FLASH received", "state", c.state, "conf_id", c.confID, "is_host", c.isConfHost, "active_peer", activePeer)
	// Non-host in an active conference: flash is a no-op (historical accuracy).
	if c.confID != "" && !c.isConfHost {
		slog.Info("phone: HOOK:FLASH no-op (non-host in conference)", "conf_id", c.confID)
		return
	}
	switch c.state {
	case StateCONNECTED:
		c.enterAddDialtone(activePeer)
	case StateADD_DIALTONE, StateADD_DIALING:
		c.abortAdd()
	case StateADD_CALLING:
		c.abortAddCalling()
	case StateADD_PRIVATE:
		c.requestMerge()
	case StateADD_INTERCEPT:
		c.abortAdd()
	case StateCONFERENCE_MERGED:
		// No-op in v1 (no second add, no drop-last-party).
		slog.Info("phone: HOOK:FLASH no-op (CONFERENCE_MERGED, v1)")
	default:
		slog.Info("phone: HOOK:FLASH no-op (unhandled state)", "state", c.state)
	}
}

func (c *Controller) enterAddDialtone(activePeer string) {
	slog.Info("phone: CONNECTED -> ADD_DIALTONE", "held", activePeer)
	c.heldPeer = activePeer
	c.state = StateADD_DIALTONE
	if c.heldPeer != "" {
		c.cb.MigrateToMesh(c.heldPeer) // move B's PC from peerMgr into mesh
		c.cb.MutePeer(c.heldPeer, true) // silent hold
	}
	c.cb.SendTone(ToneDial)
}

func (c *Controller) abortAdd() {
	slog.Info("phone: abortAdd -> CONNECTED", "from_state", c.state, "held", c.heldPeer, "adding", c.addingPeer)
	c.cb.SendTone(ToneStop)
	// Tear down the added peer if one exists. Most paths into ADD_INTERCEPT
	// already did this (busy/error/hangup handlers all call TearDownPeer
	// before transitioning), but the self-dial and blocked-contact paths in
	// dialThirdParty skip InitiateCall and so have no peer to tear down --
	// TearDownPeer is idempotent on unknown phones, so calling it
	// unconditionally here keeps abortAdd state-independent.
	if c.addingPeer != "" {
		c.cb.TearDownPeer(c.addingPeer)
	}
	if c.heldPeer != "" {
		c.cb.MutePeer(c.heldPeer, false)
	}
	c.addingPeer = ""
	c.heldPeer = ""
	c.state = StateCONNECTED
}

func (c *Controller) abortAddCalling() {
	slog.Info("phone: abortAddCalling -> CONNECTED", "held", c.heldPeer, "adding", c.addingPeer)
	c.cb.SendTone(ToneStop)
	if c.addingPeer != "" {
		c.cb.TearDownPeer(c.addingPeer)
	}
	if c.heldPeer != "" {
		c.cb.MutePeer(c.heldPeer, false)
	}
	c.addingPeer = ""
	c.heldPeer = ""
	c.state = StateCONNECTED
}

func (c *Controller) requestMerge() {
	slog.Info("phone: ADD_PRIVATE -> CONFERENCE_MERGED, requesting merge", "held", c.heldPeer, "adding", c.addingPeer)
	if c.heldPeer != "" {
		c.cb.MutePeer(c.heldPeer, false)
	}
	if c.addingPeer != "" {
		c.cb.MigrateToMesh(c.addingPeer) // move C's PC from peerMgr into mesh
	}
	c.state = StateCONFERENCE_MERGED
	c.cb.RequestConferenceMerge(c.heldPeer, c.addingPeer)
}

// HandleConferenceMember is invoked when a ConferenceMember message arrives
// from the server. It updates the local conference-membership state (confID,
// isConfHost) for use by onHookFlash and other conference-aware paths.
func (c *Controller) HandleConferenceMember(confID string, members []signal.ConferenceMemberInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.confID = confID
	c.isConfHost = false
	for _, m := range members {
		if m.Phone == c.ownNumber && m.Role == signal.RoleHost {
			c.isConfHost = true
			break
		}
	}
	slog.Info("conference: member snapshot applied", "conf_id", confID, "is_host", c.isConfHost, "members", len(members))
}

// HandleConferenceConnect is invoked when the server instructs us to open a
// PC to another conference member. Delegates to the AddMeshPeer callback.
func (c *Controller) HandleConferenceConnect(confID, peer string, initiator bool) {
	c.mu.Lock()
	if c.confID != confID {
		slog.Warn("conference: connect ignored, confID mismatch", "msg_conf_id", confID, "local_conf_id", c.confID, "peer", peer)
		c.mu.Unlock()
		return
	}
	slog.Info("conference: connect instruction", "conf_id", confID, "peer", peer, "initiator", initiator)
	c.mu.Unlock()
	// Invoke the callback without holding c.mu: AddMeshPeer reaches back into
	// ConferenceID() for the signalling conf_id tag, which also takes c.mu --
	// holding the lock across the callback would deadlock the message goroutine.
	c.cb.AddMeshPeer(peer, initiator)
}

// HandleConferenceLeave is invoked when another member leaves the conference.
// Tears down the PC to that member via RemoveMeshPeer callback.
func (c *Controller) HandleConferenceLeave(confID, peer, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.confID == "" {
		// Local conference state already cleared (host hangup tears down before
		// the server's broadcast lands). Benign race; not an error.
		slog.Info("conference: leave ignored, already torn down locally", "msg_conf_id", confID, "peer", peer, "reason", reason)
		return
	}
	if c.confID != confID {
		slog.Warn("conference: leave ignored, confID mismatch", "msg_conf_id", confID, "local_conf_id", c.confID, "peer", peer)
		return
	}
	slog.Info("conference: member left", "conf_id", confID, "peer", peer, "reason", reason)
	c.cb.RemoveMeshPeer(peer)
}

// HandleConferenceEnd is invoked when the conference ends for any reason.
// Tears down all mesh peers and transitions to REMOTE_HANGUP so the
// permanent-signal treatment plays (reorder → howler → lockout) if the
// handset is still off-hook. If the phone is already on-hook, the state
// goes to IDLE directly (onHookOn will have already cleaned up).
func (c *Controller) HandleConferenceEnd(confID, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.confID == "" {
		// Local conference state already cleared. Common host-hangup race:
		// onHookOn tore down and cleared confID before the server's broadcast
		// landed. Treat as benign; everything is already cleaned up locally.
		slog.Info("conference: end ignored, already torn down locally", "msg_conf_id", confID, "reason", reason)
		return
	}
	if c.confID != confID {
		slog.Warn("conference: end ignored, confID mismatch", "msg_conf_id", confID, "local_conf_id", c.confID, "reason", reason)
		return
	}
	slog.Info("conference: end received", "conf_id", confID, "reason", reason, "state", c.state, "is_host", c.isConfHost)
	c.cb.TearDownAllMeshPeers()
	c.cb.HangupCall()

	c.confID = ""
	c.isConfHost = false
	c.heldPeer = ""
	c.addingPeer = ""

	// If the user is still off-hook in any conference-related state, play the
	// permanent-signal treatment (reorder → howler → lockout) just like a
	// remote hangup on a 2-party call. If they are already on-hook (StateIDLE)
	// or in a non-conference state, skip treatment and go straight to IDLE.
	switch c.state {
	case StateCONFERENCE_MERGED, StateADD_PRIVATE, StateADD_CALLING,
		StateADD_DIALING, StateADD_DIALTONE, StateADD_INTERCEPT:
		c.state = StateREMOTE_HANGUP
		c.cb.SendTone(ToneStop)
		c.runPermanentSignalTreatment(StateREMOTE_HANGUP, "conference_end_"+reason)
	default:
		c.state = StateIDLE
		c.cb.SendTone(ToneStop)
		c.cb.SendRing(false)
		c.cb.SendLED("OFF")
	}
}

// HandleConferenceRejected is invoked when the server rejects our merge request.
// Plays a SIT intercept tone locally and returns the host to StateCONNECTED
// with the held peer.
func (c *Controller) HandleConferenceRejected(confID, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// requestMerge() sets c.state = StateCONFERENCE_MERGED synchronously before
	// emitting ConferenceMerge to the server, so any rejection reply arrives
	// while we're in CONFERENCE_MERGED. c.confID may still be empty if the
	// server's ConferenceMember message is in flight behind the rejection; in
	// that case we can't cross-check IDs, which is why the mismatch guard
	// only fires when both IDs are known and differ.
	if c.confID != "" && confID != "" && c.confID != confID {
		slog.Warn("conference: rejection ignored, confID mismatch", "msg_conf_id", confID, "local_conf_id", c.confID, "reason", reason)
		return
	}
	if c.state != StateCONFERENCE_MERGED {
		slog.Warn("conference: rejection ignored, not in CONFERENCE_MERGED", "state", c.state, "reason", reason)
		return // probably stale; ignore
	}
	// ToneIntercept is the SIT triple-beep tone used for merge-failed intercept,
	// matching 90s residential TWC semantics (e.g. AT&T / Time Warner Cable).
	slog.Warn("conference: merge rejected", "conf_id", confID, "reason", reason)
	c.cb.SendTone(ToneIntercept)
	// Drop the A→C peer; B is still in A's active 2-party state.
	if c.addingPeer != "" {
		c.cb.TearDownPeer(c.addingPeer)
		c.addingPeer = ""
	}
	// Restore state: held peer unmute, return to CONNECTED.
	if c.heldPeer != "" {
		c.cb.MutePeer(c.heldPeer, false)
	}
	c.heldPeer = ""
	c.state = StateCONNECTED
	// Clear conference state after state assignment, consistent with HandleConferenceEnd.
	c.confID = ""
	c.isConfHost = false
}

// ConferenceID returns the current conference ID, or empty string if not in a conference.
func (c *Controller) ConferenceID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.confID
}

// IsConferenceHost reports whether this controller is the host of the current conference.
func (c *Controller) IsConferenceHost() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isConfHost
}

// --- test-only setters (internal: test only) ---

// setStateForTest directly sets the FSM state for unit test setup.
func (c *Controller) setStateForTest(s State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = s
}

// setHeldPeerForTest sets the held peer for unit test setup.
func (c *Controller) setHeldPeerForTest(peer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.heldPeer = peer
}

// setAddingPeerForTest sets the adding peer for unit test setup.
func (c *Controller) setAddingPeerForTest(peer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addingPeer = peer
}

// heldPeerForTest returns the current held-peer phone number for unit test
// assertions.
func (c *Controller) heldPeerForTest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.heldPeer
}

// addingPeerForTest returns the current adding-peer phone number for unit
// test assertions.
func (c *Controller) addingPeerForTest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addingPeer
}

package phone

import (
	"log/slog"
	"strings"
	"sync"
	"time"
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
	StateADD_INTERCEPT   State = "ADD_INTERCEPT"   // C unreachable; B on hold
	StateCONFERENCE_MERGED State = "CONFERENCE_MERGED" // Three-way call active
)

// Tone names passed to Callbacks.SendTone. Mixer/daemon dispatch on these.
const (
	ToneDial      = "DIAL"
	ToneRingback  = "RINGBACK"
	ToneBusy      = "BUSY"
	ToneReorder   = "REORDER"
	ToneHowler    = "HOWLER"
	ToneIntercept = "INTERCEPT"
	ToneStop      = "STOP"
	ToneStopAll   = "STOPALL"
)

// Callbacks is the interface the controller uses to drive hardware and network.
type Callbacks interface {
	SendTone(name string)       // Play a tone (use one of the Tone* constants)
	OncePlaying() bool          // Reports whether a one-shot tone (e.g. intercept) is still playing
	SendRing(start bool)        // Send RING:START or RING:STOP
	SendLED(mode string)        // Send LED:<mode>
	InitiateCall(number string) // Start outgoing WebRTC call
	AnswerCall()                // Accept incoming WebRTC call
	HangupCall()                // Tear down WebRTC call
	NotifyCallConnected()       // Notify the Pico that the WebRTC peer answered
	MutePeer(phone string, muted bool)                    // Mute or unmute the A↔B audio path for a peer
	TearDownPeer(phone string)                            // Hang up and tear down the connection to a peer
	RequestConferenceMerge(held, active string)           // Request server-side three-way merge
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
	// treatmentGen is incremented each time runPermanentSignalTreatment starts.
	// The spawned goroutine captures the value and aborts on mismatch, so a
	// hook-flap that re-enters off-hook treatment within ~4 min won't let the
	// previous goroutine step the new session forward.
	treatmentGen uint64
	// done is closed by Close(); long-lived goroutines (off-hook treatment) abort
	// their sleeps when it fires so daemon shutdown isn't blocked.
	done      chan struct{}
	closeOnce sync.Once

	// activePeer is the phone number of the current 2-party remote peer, if any.
	// Set on outgoing calls in onDial, and on incoming calls via SetActivePeer
	// (called from main.go before HandleSignal("ring")). Cleared in onHookOn.
	activePeer string

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
// Used after service codes to return the phone to a clean state.
func (c *Controller) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = StateIDLE
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
	case evType == "HOOK" && evVal == "FLASH":
		c.onHookFlash()
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
func (c *Controller) HandleSignal(msgType string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch msgType {
	case "ring":
		c.onSignalRing()
	case "answer":
		c.onSignalAnswer()
	case "hangup":
		c.onSignalHangup()
	case "busy":
		c.onSignalBusy()
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
	c.state = StateIDLE
	c.digits = ""
	c.activePeer = ""
	c.heldPeer = ""
	c.addingPeer = ""
	c.confID = ""
	c.isConfHost = false
	c.cb.SendTone(ToneStop)
	c.cb.SendRing(false)
	c.cb.SendLED("OFF")
	if wasConnectedOrCalling {
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
	default:
		slog.Debug("phone: key ignored", "digit", digit, "state", c.state)
	}
}

func (c *Controller) onDial(number string) {
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
		// Rejection sequence runs async (same as server-side "not connected" error)
		go func() {
			checkState := func() bool {
				c.mu.Lock()
				defer c.mu.Unlock()
				return c.state == StateCALLING
			}

			time.Sleep(3 * time.Second)
			if !checkState() {
				return
			}

			// SIT + disconnected announcement
			c.cb.SendTone(ToneStop)
			c.cb.SendTone(ToneIntercept)
			deadline := time.Now().Add(15 * time.Second)
			for c.cb.OncePlaying() {
				time.Sleep(200 * time.Millisecond)
				if !checkState() {
					return
				}
				if time.Now().After(deadline) {
					slog.Error("phone: intercept tone timeout — aborting rejection flow")
					return
				}
			}

			time.Sleep(500 * time.Millisecond)
			if !checkState() {
				return
			}
			c.cb.SendTone(ToneBusy)
		}()
		return
	}

	c.state = StateCALLING
	c.activePeer = number
	// Brief silence before ringback — simulates PSTN call setup delay.
	// Old rotary phones had a variable pause between last digit and first ring.
	go func() {
		time.Sleep(800 * time.Millisecond)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.state != StateCALLING {
			return
		}
		c.cb.SendTone(ToneRingback)
		c.cb.InitiateCall(number)
	}()
}

// SetActivePeer records the phone number of the current call peer. Call this
// before HandleSignal("ring") for incoming calls so the controller knows who
// is calling when the handset is picked up.
func (c *Controller) SetActivePeer(number string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activePeer = number
}

func (c *Controller) onSignalRing() {
	if c.state != StateIDLE {
		slog.Info("phone: ring signal ignored (not IDLE)", "state", c.state)
		return
	}
	c.state = StateRINGING
	c.cb.SendRing(true)
	c.cb.SendLED("BLINK")
}

func (c *Controller) onSignalAnswer() {
	if c.state != StateCALLING {
		slog.Info("phone: answer signal ignored (not CALLING)", "state", c.state)
		return
	}
	c.state = StateCONNECTED
	c.cb.SendTone(ToneStop)
	c.cb.NotifyCallConnected()
}

func (c *Controller) onSignalHangup() {
	if c.state == StateRINGING {
		// Caller hung up before we answered - stop ringing and return to idle.
		slog.Info("phone: caller hung up during ring - stopping ring")
		c.state = StateIDLE
		c.cb.SendRing(false)
		c.cb.SendLED("OFF")
		return
	}
	if c.state != StateCONNECTED {
		slog.Info("phone: hangup signal ignored (not CONNECTED)", "state", c.state)
		return
	}
	c.state = StateREMOTE_HANGUP
	c.cb.HangupCall()
	c.cb.SendTone(ToneStopAll)
	c.runPermanentSignalTreatment(StateREMOTE_HANGUP, "remote hangup")
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

func (c *Controller) onSignalBusy() {
	if c.state != StateCALLING {
		slog.Info("phone: busy signal ignored (not CALLING)", "state", c.state)
		return
	}
	slog.Info("phone: busy signal received -- call rejected")
	c.cb.SendTone(ToneBusy)
	// Stay in CALLING -- caller should hang up
}

// onHookFlash dispatches the HOOK:FLASH event per 90s residential TWC semantics.
// Called with c.mu already held by HandleEvent.
func (c *Controller) onHookFlash() {
	// Non-host in an active conference: flash is a no-op (historical accuracy).
	if c.confID != "" && !c.isConfHost {
		return
	}
	switch c.state {
	case StateCONNECTED:
		c.enterAddDialtone()
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
	default:
		// Any other state: no-op.
	}
}

func (c *Controller) enterAddDialtone() {
	c.heldPeer = c.activePeer
	c.state = StateADD_DIALTONE
	c.cb.MutePeer(c.heldPeer, true)
	c.cb.SendTone(ToneDial)
}

func (c *Controller) abortAdd() {
	c.cb.SendTone(ToneStop)
	if c.heldPeer != "" {
		c.cb.MutePeer(c.heldPeer, false)
	}
	c.addingPeer = ""
	c.heldPeer = ""
	c.state = StateCONNECTED
}

func (c *Controller) abortAddCalling() {
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
	c.state = StateCONFERENCE_MERGED
	c.cb.RequestConferenceMerge(c.heldPeer, c.addingPeer)
}

// MarkAsConferenceMember is invoked from the signal handler when a
// ConferenceMember message arrives for this phone. Safe to call from
// outside the controller's lock.
func (c *Controller) MarkAsConferenceMember(confID string, isHost bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.confID = confID
	c.isConfHost = isHost
}

// --- test-only setters (internal: test only) ---

// setStateForTest directly sets the FSM state for unit test setup.
func (c *Controller) setStateForTest(s State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = s
}

// setCurrentPeerForTest sets the active peer for unit test setup.
func (c *Controller) setCurrentPeerForTest(peer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activePeer = peer
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

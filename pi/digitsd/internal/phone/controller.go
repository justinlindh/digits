package phone

import (
	"log"
	"strings"
	"sync"
	"time"
)

type State string

const (
	StateIDLE           State = "IDLE"
	StateDIALTONE       State = "DIAL_TONE"
	StateDIALING        State = "DIALING"
	StateCALLING        State = "CALLING"
	StateRINGING        State = "RINGING"
	StateCONNECTED      State = "CONNECTED"
	StateREMOTE_HANGUP  State = "REMOTE_HANGUP" // Far end hung up; handset still off-hook
)

// Callbacks is the interface the controller uses to drive hardware and network.
type Callbacks interface {
	SendTone(name string)       // Play a tone: DIAL, RINGBACK, BUSY, REORDER, HOWLER, INTERCEPT, STOP, STOPALL
	OncePlaying() bool          // Reports whether a one-shot tone (e.g. intercept) is still playing
	SendRing(start bool)        // Send RING:START or RING:STOP
	SendLED(mode string)        // Send LED:<mode>
	InitiateCall(number string) // Start outgoing WebRTC call
	AnswerCall()                // Accept incoming WebRTC call
	HangupCall()                // Tear down WebRTC call
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
	contactChecker ContactChecker
}

// NewController creates a new Controller starting in StateIDLE.
func NewController(cb Callbacks) *Controller {
	return &Controller{
		state: StateIDLE,
		cb:    cb,
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
	case evType == "KEY":
		c.onKey(evVal)
	case evType == "DIAL":
		c.onDial(evVal)
	case evType == "RING" && (evVal == "ACK" || evVal == "DONE"):
		// Informational — ring ack/done from Pico, no action needed
	case event == "PONG":
		// Keepalive response, ignore
	default:
		log.Printf("phone: unhandled event: %q", event)
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
		log.Printf("phone: unhandled signal: %q", msgType)
	}
}

// --- internal event handlers (called with lock held) ---

func (c *Controller) onHookOff() {
	switch c.state {
	case StateIDLE:
		// Outgoing: pick up → dial tone
		c.state = StateDIALTONE
		c.digits = ""
		c.cb.SendTone("DIAL")
		c.cb.SendLED("ON")
	case StateRINGING:
		// Incoming: answer the call
		c.state = StateCONNECTED
		c.cb.SendRing(false)
		c.cb.SendTone("STOP")
		c.cb.SendLED("ON")
		c.cb.AnswerCall()
	default:
		log.Printf("phone: HOOK:OFF ignored in state %s", c.state)
	}
}

func (c *Controller) onHookOn() {
	if c.state == StateIDLE {
		return
	}
	wasConnectedOrCalling := c.state == StateCONNECTED || c.state == StateCALLING
	c.state = StateIDLE
	c.digits = ""
	c.cb.SendTone("STOP")
	c.cb.SendRing(false)
	c.cb.SendLED("OFF")
	if wasConnectedOrCalling {
		c.cb.HangupCall()
	}
	// REMOTE_HANGUP: call already torn down, just clean up tones/LED (done above)
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
		c.cb.SendTone("STOP")
	case StateDIALING:
		c.digits += digit
		// Service codes (*#*N) are handled by ServiceCodeHandler.
		// Reset dial buffer when we see 4+ chars starting with *#* so the FSM
		// doesn't try to dial the service code as a phone number.
		if len(c.digits) >= 4 && len(c.digits) <= 5 && strings.HasPrefix(c.digits, "*#*") {
			log.Printf("phone: service code %q handled by bridge, resetting", c.digits)
			c.digits = ""
			c.state = StateDIALTONE
			// Service code detected — reset to dial tone without re-sending tone
		}
	default:
		log.Printf("phone: KEY:%s ignored in state %s", digit, c.state)
	}
}

func (c *Controller) onDial(number string) {
	if c.state != StateDIALING {
		log.Printf("phone: DIAL event ignored in state %s", c.state)
		return
	}

	// Contact filter: if checker is set and number is not a contact,
	// play rejection sequence (mimics unreachable number) instead of calling.
	if c.contactChecker != nil && !c.contactChecker.IsContact(number) {
		log.Printf("phone: number %s not in contacts — rejecting", number)
		c.state = StateCALLING
		c.cb.SendTone("RINGBACK")
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
			c.cb.SendTone("STOP")
			c.cb.SendTone("INTERCEPT")
			deadline := time.Now().Add(15 * time.Second)
			for c.cb.OncePlaying() {
				time.Sleep(200 * time.Millisecond)
				if !checkState() {
					return
				}
				if time.Now().After(deadline) {
					log.Println("phone: intercept tone timeout — aborting rejection flow")
					return
				}
			}

			time.Sleep(500 * time.Millisecond)
			if !checkState() {
				return
			}
			c.cb.SendTone("BUSY")
		}()
		return
	}

	c.state = StateCALLING
	// Brief silence before ringback — simulates PSTN call setup delay.
	// Old rotary phones had a variable pause between last digit and first ring.
	go func() {
		time.Sleep(800 * time.Millisecond)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.state != StateCALLING {
			return
		}
		c.cb.SendTone("RINGBACK")
		c.cb.InitiateCall(number)
	}()
}

func (c *Controller) onSignalRing() {
	if c.state != StateIDLE {
		log.Printf("phone: ring signal ignored in state %s (not IDLE)", c.state)
		return
	}
	c.state = StateRINGING
	c.cb.SendRing(true)
	c.cb.SendLED("BLINK")
}

func (c *Controller) onSignalAnswer() {
	if c.state != StateCALLING {
		log.Printf("phone: answer signal ignored in state %s (not CALLING)", c.state)
		return
	}
	c.state = StateCONNECTED
	c.cb.SendTone("STOP")
}

func (c *Controller) onSignalHangup() {
	if c.state != StateCONNECTED {
		log.Printf("phone: hangup signal ignored in state %s (not CONNECTED)", c.state)
		return
	}
	// Full POTS remote-hangup sequence:
	//   1. Tear down call, brief silence (~1s, CO processing delay)
	//   2. Reorder tone (fast busy, 480+620 Hz) for ~10s
	//   3. Intercept voice message ("If you'd like to make a call...")
	//   4. Howler tone (loud multi-freq) until user hangs up
	c.state = StateREMOTE_HANGUP
	c.cb.HangupCall()
	c.cb.SendTone("STOPALL")
	log.Println("phone: remote hangup — starting POTS off-hook sequence")
	go func() {
		// Helper to check we're still in REMOTE_HANGUP state
		stillOffHook := func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.state == StateREMOTE_HANGUP
		}

		// 1. Brief silence (~1s CO processing delay)
		time.Sleep(1 * time.Second)
		if !stillOffHook() {
			return
		}

		// 2. Reorder tone for ~10 seconds
		log.Println("phone: remote hangup — reorder tone")
		c.cb.SendTone("REORDER")
		time.Sleep(10 * time.Second)
		if !stillOffHook() {
			return
		}

		// 3. Intercept voice message
		log.Println("phone: remote hangup — intercept message")
		c.cb.SendTone("STOP")
		c.cb.SendTone("INTERCEPT")
		for c.cb.OncePlaying() {
			time.Sleep(200 * time.Millisecond)
			if !stillOffHook() {
				return
			}
		}
		if !stillOffHook() {
			return
		}

		// 4. Howler tone until hang-up
		log.Println("phone: remote hangup — howler tone")
		c.cb.SendTone("HOWLER")
	}()
}

func (c *Controller) onSignalBusy() {
	if c.state != StateCALLING {
		log.Printf("phone: busy signal ignored in state %s (not CALLING)", c.state)
		return
	}
	log.Printf("phone: busy signal received — call rejected")
	c.cb.SendTone("STOP")
	// Stay in CALLING — caller should hang up
}

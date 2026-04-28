package phone

import (
	"sync"
	"time"
)

// Confirmer arms a single action that fires when the dispatcher invokes
// Fire (typically on a "press *" event after a sensitive service code),
// or auto-runs onTimeout if the user does nothing for the timeout window.
// Cancel clears the pending state without running any callback so the
// caller can choose how to react to explicit cancellation (hang-up,
// other-key press, etc.).
//
// Confirmer is single-armed: a second Arm call while one is pending
// returns false without disturbing the existing pending state.
type Confirmer struct {
	mu        sync.Mutex
	action    func()
	onTimeout func()
	timer     *time.Timer
}

// NewConfirmer returns a fresh Confirmer with no pending action.
func NewConfirmer() *Confirmer {
	return &Confirmer{}
}

// Arm registers an action to fire on Fire and an onTimeout to run if the
// timeout elapses without Fire or Cancel. Returns false if a confirmation
// is already pending, in which case the existing pending state is left
// untouched.
func (c *Confirmer) Arm(action, onTimeout func(), timeout time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.action != nil {
		return false
	}
	c.action = action
	c.onTimeout = onTimeout
	c.timer = time.AfterFunc(timeout, c.timeoutFire)
	return true
}

func (c *Confirmer) timeoutFire() {
	c.mu.Lock()
	if c.action == nil {
		// Fire or Cancel already cleared the state ahead of the timer.
		c.mu.Unlock()
		return
	}
	onTimeout := c.onTimeout
	c.action = nil
	c.onTimeout = nil
	c.timer = nil
	c.mu.Unlock()
	if onTimeout != nil {
		onTimeout()
	}
}

// Active reports whether a confirmation is pending.
func (c *Confirmer) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.action != nil
}

// Fire runs the pending action in a goroutine and clears state.
// No-op if nothing is pending.
func (c *Confirmer) Fire() {
	c.mu.Lock()
	action := c.action
	c.action = nil
	c.onTimeout = nil
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	if action != nil {
		go action()
	}
}

// Cancel clears state without invoking any callback. Caller is
// responsible for any cleanup (e.g., restoring dial tone) since only
// it knows the cancellation context.
func (c *Confirmer) Cancel() {
	c.mu.Lock()
	c.action = nil
	c.onTimeout = nil
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
}

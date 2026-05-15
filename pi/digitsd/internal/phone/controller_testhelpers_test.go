package phone

import "time"

// Test-only accessors and mutators for Controller's unexported state. These
// live in a _test.go file so the production binary does not carry the API
// surface; the same-package _test.go gives unit tests access without any
// build tags.

var testTiming = Timing{
	AutoDialDelay:            time.Millisecond,
	RejectWait:               time.Millisecond,
	RejectPollInterval:       time.Millisecond,
	PostInterceptDelay:       time.Millisecond,
	TreatmentInitialDelay:    10 * time.Millisecond,
	TreatmentReorderDuration: time.Millisecond,
	TreatmentHowlerDuration:  time.Millisecond,
}

func newTestController(cb Callbacks, ownNumber string) *Controller {
	c := NewController(cb, ownNumber)
	c.timing = testTiming
	return c
}

func (c *Controller) setStateForTest(s State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = s
}

func (c *Controller) setHeldPeerForTest(peer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.heldPeer = peer
}

func (c *Controller) setAddingPeerForTest(peer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addingPeer = peer
}

func (c *Controller) heldPeerForTest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.heldPeer
}

func (c *Controller) addingPeerForTest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addingPeer
}

func (c *Controller) digitsForTest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.digits
}

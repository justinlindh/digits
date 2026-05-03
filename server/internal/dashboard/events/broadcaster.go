// Package events provides a tiny in-process pub/sub broadcaster used to
// signal "dashboard state may have changed" without carrying any payload.
//
// Sources (calls.Tracker, signaling.Hub) call Notify after mutating
// state. Each subscriber's channel has buffer size 1, and Notify performs
// a non-blocking send: bursts of notifications coalesce into a single
// pending wake, so subscribers see at most one signal per outstanding
// state change. Subscribers re-snapshot their own view on wake.
package events

import "sync"

// Broadcaster fans out content-free wake signals to subscribers.
//
// Zero value is not usable; use New.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// New returns an empty Broadcaster ready for use.
func New() *Broadcaster {
	return &Broadcaster{subs: make(map[chan struct{}]struct{})}
}

// Subscribe registers a new subscriber and returns its receive channel
// plus an unsubscribe function. The channel has buffer size 1; bursts of
// Notify calls collapse into a single pending wake.
func (b *Broadcaster) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// Notify wakes every subscriber. Best-effort, non-blocking: if a
// subscriber's buffer is full (a previous wake is already pending), the
// send is dropped, and the subscriber will pick up the latest state on
// its next loop iteration.
func (b *Broadcaster) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

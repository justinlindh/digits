package signaling

// Tests that *Hub.Register and *Hub.Unregister call into a registered
// dashboard-events Notifier. The dashboard SSE handler depends on these
// wakes to re-render the LINES ONLINE counter without polling the hub.

import (
	"sync/atomic"
	"testing"
)

type fakeNotifier struct {
	count atomic.Int32
}

func (f *fakeNotifier) Notify() {
	f.count.Add(1)
}

func (f *fakeNotifier) Count() int32 { return f.count.Load() }

func TestHub_DashEvents_NotifiesOnRegisterAndUnregister(t *testing.T) {
	h := NewHub()
	n := &fakeNotifier{}
	h.SetDashboardEvents(n)

	conn := &Conn{Send: make(chan []byte, 1)}
	h.Register("+15550001", conn)
	if got := n.Count(); got != 1 {
		t.Fatalf("Register: got %d notifications want 1", got)
	}

	h.Unregister("+15550001", conn)
	if got := n.Count(); got != 2 {
		t.Fatalf("Unregister: got %d notifications want 2", got)
	}

	// Stale Unregister (different conn) must not wake: the line is already gone.
	h.Unregister("+15550001", &Conn{Send: make(chan []byte, 1)})
	if got := n.Count(); got != 2 {
		t.Fatalf("stale Unregister must not Notify: got %d want 2", got)
	}
}

func TestHub_DashEvents_NilSafe(t *testing.T) {
	h := NewHub()
	// No SetDashboardEvents call: Register and Unregister must not panic.
	conn := &Conn{Send: make(chan []byte, 1)}
	h.Register("+15550002", conn)
	h.Unregister("+15550002", conn)
}

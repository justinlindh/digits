package signaling

import (
	"context"
	"testing"
)

func expectNoMessage(t *testing.T, conn *Conn) {
	t.Helper()
	select {
	case data := <-conn.Send:
		t.Fatalf("expected no message, got %q", string(data))
	default:
	}
}

// TestQuietHoursSchedulerPushesOnTransition seeds a line as not-silent, then
// flips the effective state and verifies the second evaluation pushes a
// TypeLineSettings with the new SilentMode, while a steady state produces no
// push.
func TestQuietHoursSchedulerPushesOnTransition(t *testing.T) {
	hub := NewHub()
	store := newFakeLineStore()
	store.set("3140001", &LineSettings{VoiceStyle: "copper", SilentMode: false})
	sched := NewQuietHoursScheduler(hub, store)

	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	// First evaluation seeds state without pushing (OnRegistered already
	// delivered the baseline on connect).
	sched.evaluate(context.Background())
	expectNoMessage(t, conn)

	// Steady state: no transition, no push.
	sched.evaluate(context.Background())
	expectNoMessage(t, conn)

	// Window opens: effective SilentMode flips to true.
	store.set("3140001", &LineSettings{VoiceStyle: "copper", SilentMode: true})
	sched.evaluate(context.Background())
	msg := drainOne(t, conn.Send)
	if msg.Type != TypeLineSettings {
		t.Errorf("Type: got %q, want %q", msg.Type, TypeLineSettings)
	}
	if msg.LineSettings == nil || !msg.LineSettings.SilentMode {
		t.Fatalf("expected SilentMode true in push, got %+v", msg.LineSettings)
	}

	// Still silent: no further push.
	sched.evaluate(context.Background())
	expectNoMessage(t, conn)

	// Window closes: flips back to false, expect a push.
	store.set("3140001", &LineSettings{VoiceStyle: "copper", SilentMode: false})
	sched.evaluate(context.Background())
	msg = drainOne(t, conn.Send)
	if msg.LineSettings == nil || msg.LineSettings.SilentMode {
		t.Fatalf("expected SilentMode false in push, got %+v", msg.LineSettings)
	}
}

// TestQuietHoursSchedulerSkipsOffline verifies an offline line is never
// evaluated or pushed, and that its tracking entry is pruned so a reconnect
// re-seeds cleanly.
func TestQuietHoursSchedulerSkipsOffline(t *testing.T) {
	hub := NewHub()
	store := newFakeLineStore()
	store.set("3140002", &LineSettings{VoiceStyle: "copper", SilentMode: false})
	sched := NewQuietHoursScheduler(hub, store)

	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140002", conn)
	sched.evaluate(context.Background()) // seed
	expectNoMessage(t, conn)

	hub.Unregister("3140002", conn)
	store.set("3140002", &LineSettings{VoiceStyle: "copper", SilentMode: true})
	sched.evaluate(context.Background())

	sched.mu.Lock()
	_, tracked := sched.lastSent["3140002"]
	sched.mu.Unlock()
	if tracked {
		t.Errorf("offline line should be pruned from tracking")
	}
}

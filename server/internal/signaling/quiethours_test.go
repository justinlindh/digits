package signaling

import (
	"context"
	"errors"
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

	// First evaluation seeds state and pushes the current effective state to
	// close the connect/seed gap. The daemon dedupes by value so this is a
	// no-op when already correct.
	sched.evaluate(context.Background())
	seed := drainOne(t, conn.Send)
	if seed.LineSettings == nil || seed.LineSettings.SilentMode {
		t.Fatalf("expected seed push with SilentMode false, got %+v", seed.LineSettings)
	}

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
	sched.evaluate(context.Background()) // seed pushes current state
	_ = drainOne(t, conn.Send)

	hub.Unregister("3140002", conn)
	store.set("3140002", &LineSettings{VoiceStyle: "copper", SilentMode: true})
	sched.evaluate(context.Background())

	sched.mu.Lock()
	_, tracked := sched.lastSent[LineIdentity{Number: "3140002"}]
	sched.mu.Unlock()
	if tracked {
		t.Errorf("offline line should be pruned from tracking")
	}
}

// TestQuietHoursSchedulerSeedPushesActiveWindow verifies the seed-gap fix: a
// number seen for the first time whose effective state is silent (window open
// right now) gets a push on the first tick, not a silent record. Without the
// seed push, a boundary crossing between OnRegistered and the first tick would
// be lost and the line would never go silent for that window.
func TestQuietHoursSchedulerSeedPushesActiveWindow(t *testing.T) {
	hub := NewHub()
	store := newFakeLineStore()
	// Window is active right now: effective SilentMode is true on first sight.
	store.set("3140005", &LineSettings{VoiceStyle: "copper", SilentMode: true})
	sched := NewQuietHoursScheduler(hub, store)

	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140005", conn)

	sched.evaluate(context.Background())
	msg := drainOne(t, conn.Send)
	if msg.Type != TypeLineSettings {
		t.Errorf("Type: got %q, want %q", msg.Type, TypeLineSettings)
	}
	if msg.LineSettings == nil || !msg.LineSettings.SilentMode {
		t.Fatalf("expected seed push with SilentMode true, got %+v", msg.LineSettings)
	}
}

// TestQuietHoursSchedulerScopedToLocalConns verifies the scheduler evaluates
// and pushes only lines connected to THIS hub. A number with no local
// connection (e.g. online on another replica) is never evaluated or pushed,
// even though the store has settings for it.
func TestQuietHoursSchedulerScopedToLocalConns(t *testing.T) {
	hub := NewHub()
	store := newFakeLineStore()
	store.set("3140006", &LineSettings{VoiceStyle: "copper", SilentMode: true}) // local
	store.set("3140007", &LineSettings{VoiceStyle: "copper", SilentMode: true}) // remote-only
	sched := NewQuietHoursScheduler(hub, store)

	localConn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140006", localConn)
	// 3140007 is intentionally NOT registered on this hub: it represents a
	// line connected to a different replica.

	sched.evaluate(context.Background())

	// The local line is evaluated and seed-pushed.
	msg := drainOne(t, localConn.Send)
	if msg.LineSettings == nil || !msg.LineSettings.SilentMode {
		t.Fatalf("expected local line push with SilentMode true, got %+v", msg.LineSettings)
	}

	// The remote-only line is never tracked, evaluated, or pushed.
	sched.mu.Lock()
	_, tracked := sched.lastSent[LineIdentity{Number: "3140007"}]
	sched.mu.Unlock()
	if tracked {
		t.Errorf("remote-only line should not be evaluated by this replica")
	}
}

type quietHoursReuseStore struct {
	hub       *Hub
	oldConn   *Conn
	newConn   *Conn
	fenceSeen bool
	inside    bool
	reused    bool
}

func (s *quietHoursReuseStore) EffectiveLineSettings(context.Context, string) (*LineSettings, error) {
	panic("number-only effective settings lookup used")
}

func (s *quietHoursReuseStore) EffectiveLineSettingsForLine(_ context.Context, number string, lineID int64) (*LineSettings, error) {
	if !s.inside {
		return nil, errors.New("effective settings read outside renumber fence")
	}
	if lineID == 1 && !s.reused {
		s.reused = true
		s.hub.Unregister(number, s.oldConn)
		if err := s.hub.Register(number, s.newConn); err != nil {
			return nil, err
		}
		return &LineSettings{SilentMode: true}, nil
	}
	if lineID == 2 {
		return &LineSettings{SilentMode: false}, nil
	}
	return nil, errors.New("unexpected line identity")
}

func (s *quietHoursReuseStore) LineIdentifiers(context.Context, string) (int64, string, error) {
	return 0, "", errors.New("not used")
}

func (s *quietHoursReuseStore) WithRenumberReadFence(ctx context.Context, fn func(context.Context) error) error {
	s.fenceSeen = true
	s.inside = true
	defer func() { s.inside = false }()
	return fn(ctx)
}

func TestQuietHoursSchedulerRetainsLocalLineIdentityAcrossReuse(t *testing.T) {
	const number = "3140098"
	hub := NewHub()
	hub.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
	oldConn := &Conn{LineID: 1, HardwareID: "old-owner", Send: make(chan []byte, 1)}
	newConn := &Conn{LineID: 2, HardwareID: "new-owner", Send: make(chan []byte, 1)}
	if err := hub.Register(number, oldConn); err != nil {
		t.Fatal(err)
	}
	store := &quietHoursReuseStore{hub: hub, oldConn: oldConn, newConn: newConn}
	sched := NewQuietHoursScheduler(hub, store)

	sched.evaluate(context.Background())
	if !store.fenceSeen {
		t.Fatal("quiet-hours evaluation did not use renumber read fence")
	}
	expectNoMessage(t, newConn)

	sched.evaluate(context.Background())
	msg := drainOne(t, newConn.Send)
	if msg.LineSettings == nil || msg.LineSettings.SilentMode {
		t.Fatalf("replacement owner did not receive its own settings: %+v", msg.LineSettings)
	}
}

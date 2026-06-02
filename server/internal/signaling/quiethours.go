package signaling

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// quietHoursTick is how often the scheduler re-evaluates each online line's
// effective silent state. Quiet-hours windows have minute granularity, so a
// one-minute cadence catches every open/close transition with at most a
// minute of lag. The work per tick is bounded by the number of currently
// online lines and is a cheap settings lookup plus a comparison.
const quietHoursTick = time.Minute

// QuietHoursScheduler periodically recomputes the effective per-line settings
// (which fold any active scheduled quiet-hours window into SilentMode) and
// pushes the updated settings to online devices whenever the effective silent
// state flips. It reuses the same EffectiveLineSettings + TypeLineSettings
// path that relay.OnRegistered uses on connect, so a device that is online
// across a window boundary converges to the same state it would get on a
// fresh registration, without the household having to touch anything.
//
// Pushes are emitted only on a state change, tracked per line number, so a
// line that stays inside (or outside) its window across many ticks produces
// no traffic. The tracked state is cleared lazily for numbers that go
// offline so the map cannot grow unbounded across a long uptime.
type QuietHoursScheduler struct {
	hub   *Hub
	store LineStore

	mu       sync.Mutex
	lastSent map[string]bool // number -> last pushed effective SilentMode
}

// NewQuietHoursScheduler wires a scheduler to the hub (for the online roster
// and the push path) and the line store (for effective settings).
func NewQuietHoursScheduler(hub *Hub, store LineStore) *QuietHoursScheduler {
	return &QuietHoursScheduler{
		hub:      hub,
		store:    store,
		lastSent: make(map[string]bool),
	}
}

// Run ticks until ctx is cancelled, evaluating online lines on each tick.
// Intended to be launched in its own goroutine from cmd/signald.
func (s *QuietHoursScheduler) Run(ctx context.Context) {
	if s.hub == nil || s.store == nil {
		return
	}
	ticker := time.NewTicker(quietHoursTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evaluate(ctx)
		}
	}
}

// evaluate recomputes the effective silent state for every online line and
// pushes the new settings to any line whose state changed since the last
// tick. It also prunes tracking entries for lines that have gone offline so
// a reconnect re-pushes from a clean slate (the on-connect path in
// OnRegistered already sends the current state, so a transition that lands
// while a line is offline is reconciled on its next registration).
func (s *QuietHoursScheduler) evaluate(ctx context.Context) {
	online := s.hub.OnlineNumbers()
	onlineSet := make(map[string]bool, len(online))
	for _, number := range online {
		onlineSet[number] = true
	}

	s.mu.Lock()
	for number := range s.lastSent {
		if !onlineSet[number] {
			delete(s.lastSent, number)
		}
	}
	s.mu.Unlock()

	for _, number := range online {
		settings, err := s.store.EffectiveLineSettings(ctx, number)
		if err != nil {
			slog.DebugContext(ctx, "quiet-hours eval skipped", "number", number, "err", err)
			continue
		}
		if settings == nil {
			continue
		}

		s.mu.Lock()
		prev, seen := s.lastSent[number]
		s.lastSent[number] = settings.SilentMode
		s.mu.Unlock()

		// First time we see a line, seed its state without pushing:
		// relay.OnRegistered already delivered the current settings on
		// connect, so only an actual transition while online needs a push.
		if !seen || prev == settings.SilentMode {
			continue
		}
		if err := s.hub.SendTo(number, &Message{
			Type:         TypeLineSettings,
			To:           number,
			LineSettings: settings,
		}); err != nil {
			slog.WarnContext(ctx, "quiet-hours push failed", "number", number, "err", err)
		} else {
			slog.InfoContext(ctx, "quiet-hours transition pushed", "number", number, "silent", settings.SilentMode)
		}
	}
}

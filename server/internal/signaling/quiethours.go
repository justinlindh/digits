package signaling

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// quietHoursTick is how often the scheduler re-evaluates each locally connected line's
// effective silent state. Quiet-hours windows have minute granularity, so a
// one-minute cadence catches every open/close transition with at most a
// minute of lag. The work per tick is bounded by the number of currently
// locally connected lines and is a cheap settings lookup plus a comparison.
const quietHoursTick = time.Minute

// QuietHoursScheduler periodically recomputes the effective per-line settings
// (which fold any active scheduled quiet-hours window into SilentMode) and
// pushes the updated settings to online devices whenever the effective silent
// state flips. It reuses the same EffectiveLineSettings + TypeLineSettings
// path that relay.OnRegistered uses on connect, so a device that is online
// across a window boundary converges to the same state it would get on a
// fresh registration, without the household having to touch anything.
//
// Pushes are emitted on a state change, tracked per line number, so a line
// that stays inside (or outside) its window across many ticks produces no
// traffic. On the first tick a line is seen, the scheduler pushes the current
// effective state unconditionally to close the seed gap: a window boundary can
// cross between relay.OnRegistered's connect-push and the scheduler's first
// tick (up to one tick of lag), and the daemon dedupes by value so the push is
// a no-op when the device already matches.
//
// The scheduler iterates only the lines connected to THIS hub instance
// (Hub.LocalNumbers), not the global online roster. A device is connected to
// exactly one replica, so each line is evaluated and pushed by exactly one
// replica with a local send (no Redis fan-out, no duplicate pushes across
// replicas). The tracked state is cleared lazily for numbers no longer
// connected locally (offline, or moved to another replica) so the map cannot
// grow unbounded.
type QuietHoursScheduler struct {
	hub   *Hub
	store LineStore

	mu       sync.Mutex
	lastSent map[string]bool // number -> last pushed effective SilentMode
}

// NewQuietHoursScheduler wires a scheduler to the hub (for the locally
// connected lines and the push path) and the line store (for effective
// settings).
func NewQuietHoursScheduler(hub *Hub, store LineStore) *QuietHoursScheduler {
	return &QuietHoursScheduler{
		hub:      hub,
		store:    store,
		lastSent: make(map[string]bool),
	}
}

// Run ticks until ctx is cancelled, evaluating locally connected lines on each tick.
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

// evaluate recomputes the effective silent state for every line connected to
// this replica and pushes the new settings to any line whose state changed
// since the last tick (or that this replica is seeing for the first time). It
// also prunes tracking entries for lines no longer connected locally (offline,
// or migrated to another replica) so the map cannot grow unbounded and a
// reconnect re-seeds from a clean slate.
func (s *QuietHoursScheduler) evaluate(ctx context.Context) {
	local := s.hub.LocalNumbers()
	localSet := make(map[string]bool, len(local))
	for _, number := range local {
		localSet[number] = true
	}

	s.mu.Lock()
	for number := range s.lastSent {
		if !localSet[number] {
			delete(s.lastSent, number)
		}
	}
	s.mu.Unlock()

	for _, number := range local {
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

		// On a steady state (already seen and unchanged) emit nothing. On the
		// first sight of a line, push the current effective state to close the
		// seed gap: a window boundary can cross between OnRegistered's
		// connect-push and this first tick, and the daemon dedupes by value so
		// the push is a no-op when the device already matches.
		if seen && prev == settings.SilentMode {
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

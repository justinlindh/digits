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
	lastSent map[LineIdentity]bool // local line identity -> last pushed effective SilentMode
}

// NewQuietHoursScheduler wires a scheduler to the hub (for the locally
// connected lines and the push path) and the line store (for effective
// settings).
func NewQuietHoursScheduler(hub *Hub, store LineStore) *QuietHoursScheduler {
	return &QuietHoursScheduler{
		hub:      hub,
		store:    store,
		lastSent: make(map[LineIdentity]bool),
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
	local := s.hub.LocalLineIdentities()
	localSet := make(map[LineIdentity]bool, len(local))
	for _, identity := range local {
		localSet[identity] = true
	}

	s.mu.Lock()
	for identity := range s.lastSent {
		if !localSet[identity] {
			delete(s.lastSent, identity)
		}
	}
	s.mu.Unlock()

	for _, identity := range local {
		pushed := false
		err := s.store.WithRenumberReadFence(ctx, func(fencedCtx context.Context) error {
			settings, err := s.store.EffectiveLineSettingsForLine(fencedCtx, identity.Number, identity.LineID)
			if err != nil {
				return err
			}
			if settings == nil {
				return nil
			}

			s.mu.Lock()
			prev, seen := s.lastSent[identity]
			s.lastSent[identity] = settings.SilentMode
			s.mu.Unlock()

			if seen && prev == settings.SilentMode {
				return nil
			}
			err = s.hub.SendToLine(identity.Number, identity.LineID, &Message{
				Type:         TypeLineSettings,
				To:           identity.Number,
				LineSettings: settings,
			})
			pushed = err == nil
			return err
		})
		if err != nil {
			slog.DebugContext(ctx, "quiet-hours eval skipped", "number", identity.Number, "line_id", identity.LineID, "err", err)
			continue
		}
		if pushed {
			slog.InfoContext(ctx, "quiet-hours transition pushed", "number", identity.Number, "line_id", identity.LineID)
		}
	}
}

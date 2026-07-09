package household

import (
	"context"
	"log/slog"
)

// NeedsOnboarding returns true if the given user has no household memberships.
//
// This runs on every authenticated request behind the onboarding gate, so it
// leans on the Store's positive-only hasHousehold cache to avoid a COUNT query
// for established users. A cache hit means the user has a household and so does
// not need onboarding; only a miss falls through to the database. A user with a
// household is cached on the first miss and stays cached until they leave, so
// the query runs at most once per user per replica lifetime (plus once more
// after any membership removal). New users are never cached, so they keep being
// re-evaluated until they create or join a household.
func (s *Store) NeedsOnboarding(ctx context.Context, userID string) bool {
	return s.needsOnboarding(ctx, userID, s.countMemberships)
}

// needsOnboarding holds the cache logic with the membership counter injected,
// so unit tests can exercise the hit/miss/eviction paths without a database.
func (s *Store) needsOnboarding(ctx context.Context, userID string, count func(context.Context, string) (int, error)) bool {
	if _, ok := s.hasHousehold.Load(userID); ok {
		return false
	}
	n, err := count(ctx, userID)
	if err != nil {
		// On error, assume they don't need onboarding to avoid redirect loops.
		// Log so a persistent lookup failure is visible rather than silently
		// stranding the user on a household-less dashboard.
		slog.ErrorContext(ctx, "NeedsOnboarding: count memberships failed", "user_id", userID, "err", err)
		return false
	}
	if n > 0 {
		s.markHasHousehold(userID)
		return false
	}
	return true
}

// countMemberships returns how many households the user belongs to.
func (s *Store) countMemberships(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM household_members WHERE user_id = $1`,
		userID,
	).Scan(&count)
	return count, err
}

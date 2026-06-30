package household

import (
	"context"
	"log/slog"
)

// NeedsOnboarding returns true if the given user has no household memberships.
func (s *Store) NeedsOnboarding(ctx context.Context, userID string) bool {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM household_members WHERE user_id = $1`,
		userID,
	).Scan(&count)
	if err != nil {
		// On error, assume they don't need onboarding to avoid redirect loops.
		// Log so a persistent lookup failure is visible rather than silently
		// stranding the user on a household-less dashboard.
		slog.ErrorContext(ctx, "NeedsOnboarding: count memberships failed", "user_id", userID, "err", err)
		return false
	}
	return count == 0
}

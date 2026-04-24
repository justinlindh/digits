package household

import "context"

// NeedsOnboarding returns true if the given user has no household memberships.
func (s *Store) NeedsOnboarding(ctx context.Context, userID string) bool {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM household_members WHERE user_id = $1`,
		userID,
	).Scan(&count)
	if err != nil {
		// On error, assume they don't need onboarding to avoid redirect loops
		return false
	}
	return count == 0
}

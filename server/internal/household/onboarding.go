package household

import "fmt"

// NeedsOnboarding returns true if the given user has no household memberships.
func (s *Store) NeedsOnboarding(userID string) bool {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM household_members WHERE user_id = $1`,
		userID,
	).Scan(&count)
	if err != nil {
		// On error, assume they don't need onboarding to avoid redirect loops
		return false
	}
	return count == 0
}

// Onboard creates a default household named "<userName>'s Family" and adds the
// user as its admin member. It returns the new household.
func (s *Store) Onboard(userID, userName string) (*Household, error) {
	name := fmt.Sprintf("%s's Family", userName)
	if userName == "" {
		name = "My Family"
	}
	return s.Create(name, userID)
}

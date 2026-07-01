package line

import (
	"context"
	"database/sql"

	"github.com/justinlindh/digits/server/internal/db"
)

// Authorizer checks whether a call between two line numbers is permitted based
// on household membership and active household links.
type Authorizer struct {
	db *sql.DB
}

// NewAuthorizer returns an Authorizer backed by database.
func NewAuthorizer(database *db.Database) *Authorizer {
	return &Authorizer{db: database.DB}
}

// CanCall reports whether fromNumber is permitted to call toNumber, which is
// true when both lines share a household or their households have an active
// link.
func (a *Authorizer) CanCall(ctx context.Context, fromNumber, toNumber string) (bool, error) {
	var allowed bool
	err := a.db.QueryRowContext(ctx, `
        WITH caller AS (SELECT household_id FROM lines WHERE number = $1),
             callee AS (SELECT household_id FROM lines WHERE number = $2)
        SELECT EXISTS (
            SELECT 1 FROM caller, callee WHERE caller.household_id = callee.household_id
            UNION ALL
            SELECT 1 FROM caller
            JOIN callee ON true
            JOIN household_links hl ON hl.status = 'active'
              AND (
                (hl.household_a_id = caller.household_id AND hl.household_b_id = callee.household_id)
                OR (hl.household_a_id = callee.household_id AND hl.household_b_id = caller.household_id)
              )
        )
    `, fromNumber, toNumber).Scan(&allowed)
	return allowed, err
}

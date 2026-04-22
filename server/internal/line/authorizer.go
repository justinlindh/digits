package line

import (
	"context"

	"github.com/justinlindh/digits/server/internal/db"
)

type Authorizer struct {
	db *db.Database
}

func NewAuthorizer(database *db.Database) *Authorizer {
	return &Authorizer{db: database}
}

func (a *Authorizer) CanCall(ctx context.Context, fromNumber, toNumber string) (bool, error) {
	var allowed bool
	err := a.db.DB.QueryRowContext(ctx, `
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

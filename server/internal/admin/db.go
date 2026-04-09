package admin

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

type AdminDB struct {
	DB *sql.DB
}

func OpenAdmin(databaseURL string) (*AdminDB, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open admin db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping admin db: %w", err)
	}
	a := &AdminDB{DB: db}
	if err := a.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate admin db: %w", err)
	}
	return a, nil
}

func (a *AdminDB) Close() error {
	return a.DB.Close()
}

func (a *AdminDB) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS admin_users (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			username    TEXT NOT NULL UNIQUE,
			secret_hash TEXT NOT NULL,
			created_at  TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			admin_id   UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_sessions_token ON admin_sessions(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_sessions_expires ON admin_sessions(expires_at)`,
	}
	for _, m := range migrations {
		if _, err := a.DB.Exec(m); err != nil {
			return fmt.Errorf("admin migration failed: %w\nSQL: %s", err, m)
		}
	}
	return nil
}

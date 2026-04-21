package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

type Database struct {
	DB *sql.DB
}

func Open(databaseURL string) (*Database, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	d := &Database{DB: db}
	if err := d.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *Database) Close() error {
	return d.DB.Close()
}

func (d *Database) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		// v1: core phone/call tables (migrated from SQLite)
		`CREATE TABLE IF NOT EXISTS phones (
			id          SERIAL PRIMARY KEY,
			number      TEXT NOT NULL UNIQUE,
			name        TEXT NOT NULL DEFAULT '',
			device_id   TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ DEFAULT NOW(),
			updated_at  TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS calls (
			id          SERIAL PRIMARY KEY,
			caller      TEXT NOT NULL,
			callee      TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'initiated',
			started_at  TIMESTAMPTZ DEFAULT NOW(),
			answered_at TIMESTAMPTZ,
			ended_at    TIMESTAMPTZ,
			duration_s  INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		// v2: user accounts + auth
		`CREATE TABLE IF NOT EXISTS users (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email       TEXT NOT NULL UNIQUE,
			name        TEXT NOT NULL DEFAULT '',
			google_id   TEXT UNIQUE,
			created_at  TIMESTAMPTZ DEFAULT NOW(),
			last_login_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash  TEXT NOT NULL UNIQUE,
			expires_at  TIMESTAMPTZ NOT NULL,
			created_at  TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS magic_links (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email       TEXT NOT NULL,
			token_hash  TEXT NOT NULL UNIQUE,
			expires_at  TIMESTAMPTZ NOT NULL,
			used        BOOLEAN DEFAULT FALSE,
			created_at  TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_magic_links_token ON magic_links(token_hash)`,
		// v3: households + members
		`CREATE TABLE IF NOT EXISTS households (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name       TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS household_members (
			user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			household_id UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
			role         TEXT NOT NULL DEFAULT 'admin',
			created_at   TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (user_id, household_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_household_members_household ON household_members(household_id)`,
		// v3b: link phones to households
		`ALTER TABLE phones ADD COLUMN IF NOT EXISTS household_id UUID REFERENCES households(id)`,
		`CREATE INDEX IF NOT EXISTS idx_phones_household ON phones(household_id)`,
		`ALTER TABLE households ADD COLUMN IF NOT EXISTS call_history_enabled BOOLEAN NOT NULL DEFAULT false`,
		// v4: phone pairing
		`ALTER TABLE phones ADD COLUMN IF NOT EXISTS pairing_code TEXT`,
		`ALTER TABLE phones ADD COLUMN IF NOT EXISTS pairing_code_expires_at TIMESTAMPTZ`,
		`ALTER TABLE phones ADD COLUMN IF NOT EXISTS paired_at TIMESTAMPTZ`,
		`ALTER TABLE phones ADD COLUMN IF NOT EXISTS hardware_id TEXT UNIQUE`,
		`CREATE INDEX IF NOT EXISTS idx_phones_pairing_code ON phones(pairing_code) WHERE pairing_code IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_phones_hardware_id ON phones(hardware_id) WHERE hardware_id IS NOT NULL`,
		// v5: household linking
		`CREATE TABLE IF NOT EXISTS household_links (
			id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			household_a_id   UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
			household_b_id   UUID REFERENCES households(id) ON DELETE CASCADE,
			status           TEXT NOT NULL DEFAULT 'pending',
			invite_code      TEXT NOT NULL UNIQUE,
			invited_by       UUID NOT NULL REFERENCES users(id),
			accepted_by      UUID REFERENCES users(id),
			created_at       TIMESTAMPTZ DEFAULT NOW(),
			accepted_at      TIMESTAMPTZ,
			revoked_at       TIMESTAMPTZ,
			revoked_by       UUID REFERENCES users(id),
			CHECK (household_b_id IS NULL OR household_a_id < household_b_id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_household_links_invite_code_pending ON household_links(invite_code) WHERE status = 'pending'`,
		`CREATE INDEX IF NOT EXISTS idx_household_links_a ON household_links(household_a_id)`,
		`CREATE INDEX IF NOT EXISTS idx_household_links_b ON household_links(household_b_id) WHERE household_b_id IS NOT NULL`,
		// v5b: contacts + contact_invites
		`CREATE TABLE IF NOT EXISTS contacts (
			id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			phone_id          INT NOT NULL REFERENCES phones(id) ON DELETE CASCADE,
			contact_phone_id  INT NOT NULL REFERENCES phones(id) ON DELETE CASCADE,
			name              TEXT NOT NULL DEFAULT '',
			created_at        TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(phone_id, contact_phone_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_contacts_phone_id ON contacts(phone_id)`,
		`CREATE TABLE IF NOT EXISTS contact_invites (
			id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			from_phone_id         INT NOT NULL REFERENCES phones(id) ON DELETE CASCADE,
			to_phone_id           INT NOT NULL REFERENCES phones(id) ON DELETE CASCADE,
			from_name             TEXT NOT NULL DEFAULT '',
			to_name               TEXT,
			status                TEXT NOT NULL DEFAULT 'pending',
			invited_by_user_id    UUID REFERENCES users(id),
			responded_by_user_id  UUID REFERENCES users(id),
			created_at            TIMESTAMPTZ DEFAULT NOW(),
			responded_at          TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_contact_invites_to_phone_pending ON contact_invites(to_phone_id) WHERE status = 'pending'`,
		`ALTER TABLE phones ADD COLUMN IF NOT EXISTS device_token TEXT`,
		// v6: lines + devices (replaces phones, contacts, contact_invites)
		`CREATE TABLE IF NOT EXISTS lines (
			id           SERIAL PRIMARY KEY,
			number       TEXT NOT NULL UNIQUE,
			name         TEXT NOT NULL DEFAULT '',
			household_id UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
			created_at   TIMESTAMPTZ DEFAULT NOW(),
			updated_at   TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_lines_household ON lines(household_id);

		CREATE TABLE IF NOT EXISTS devices (
			id                     SERIAL PRIMARY KEY,
			line_id                INT NOT NULL REFERENCES lines(id) ON DELETE CASCADE,
			hardware_id            TEXT UNIQUE,
			device_id              TEXT NOT NULL DEFAULT '',
			device_token           TEXT,
			pairing_code           TEXT,
			pairing_code_expires_at TIMESTAMPTZ,
			paired_at              TIMESTAMPTZ,
			created_at             TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_devices_line ON devices(line_id);
		CREATE INDEX IF NOT EXISTS idx_devices_pairing_code ON devices(pairing_code) WHERE pairing_code IS NOT NULL;
		CREATE INDEX IF NOT EXISTS idx_devices_hardware_id ON devices(hardware_id) WHERE hardware_id IS NOT NULL;

		INSERT INTO lines (id, number, name, household_id, created_at, updated_at)
		SELECT id, number, name, household_id, created_at, updated_at
		FROM phones
		WHERE number IS NOT NULL AND number != '' AND household_id IS NOT NULL;

		INSERT INTO devices (line_id, hardware_id, device_id, device_token, pairing_code, pairing_code_expires_at, paired_at, created_at)
		SELECT p.id, p.hardware_id, p.device_id, p.device_token, p.pairing_code, p.pairing_code_expires_at, p.paired_at, p.created_at
		FROM phones p
		WHERE p.id IN (SELECT id FROM lines);

		SELECT setval('lines_id_seq', COALESCE((SELECT MAX(id) FROM lines), 1));
		SELECT setval('devices_id_seq', COALESCE((SELECT MAX(id) FROM devices), 1));

		DROP TABLE IF EXISTS contact_invites;
		DROP TABLE IF EXISTS contacts;
		DROP TABLE IF EXISTS phones`,
		// v7: allow devices to exist before pairing (line_id NULL until paired)
		`ALTER TABLE devices ALTER COLUMN line_id DROP NOT NULL`,
		// v8: household timezone
		`ALTER TABLE households ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'UTC'`,
		// v9: device last-seen timestamp
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ`,
		// v10: hash existing plaintext device tokens with SHA-256
		`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 10) THEN
        UPDATE devices SET device_token = encode(sha256(device_token::bytea), 'hex')
        WHERE device_token IS NOT NULL;
        INSERT INTO schema_version (version) VALUES (10);
    END IF;
END $$;`,
		// v11: per-line settings JSONB column (voice_style, etc.)
		`ALTER TABLE lines ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}'::jsonb`,
		// v12: user-selectable webapp theme ('intercom' = default, 'dialup' = 1997 online-service alternate)
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS theme TEXT NOT NULL DEFAULT 'intercom'`,
		// v13: rename earlier theme identifier 'aol' -> 'dialup' (only affects pre-release branches)
		`UPDATE users SET theme = 'dialup' WHERE theme = 'aol'`,
		// v14: rename earlier theme identifier 'c' -> 'intercom' and update the column default
		//      (only affects pre-release branches where v12 shipped with DEFAULT 'c')
		`UPDATE users SET theme = 'intercom' WHERE theme = 'c'`,
		`ALTER TABLE users ALTER COLUMN theme SET DEFAULT 'intercom'`,
		// v16: track why a call ended (e.g. 'merged_to_conference' vs a normal hangup)
		`ALTER TABLE calls ADD COLUMN IF NOT EXISTS end_reason TEXT`,
		// v15: party line (three-way calling) support
		`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 15) THEN

        CREATE TABLE conferences (
            id UUID PRIMARY KEY,
            host_phone TEXT NOT NULL,
            originating_call_id INTEGER NOT NULL REFERENCES calls(id),
            state TEXT NOT NULL CHECK (state IN ('active', 'ended')),
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            ended_at TIMESTAMPTZ,
            end_reason TEXT
        );

        CREATE INDEX conferences_host_phone_idx ON conferences(host_phone);
        CREATE INDEX conferences_state_idx ON conferences(state);

        CREATE TABLE conference_members (
            conference_id UUID NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
            phone TEXT NOT NULL,
            role TEXT NOT NULL CHECK (role IN ('host', 'added')),
            joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            left_at TIMESTAMPTZ,
            left_reason TEXT,
            PRIMARY KEY (conference_id, phone)
        );

        CREATE INDEX conference_members_phone_idx ON conference_members(phone);

        ALTER TABLE calls ADD COLUMN originating_conference_id UUID REFERENCES conferences(id);

        INSERT INTO schema_version (version) VALUES (15);
    END IF;
END $$;`,
	}
	for _, m := range migrations {
		if _, err := d.DB.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}
	return nil
}

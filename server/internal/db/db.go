package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/tracing"
)

type Database struct {
	DB *sql.DB
}

func Open(databaseURL string) (*Database, error) {
	// tracing.OpenSQLDB wraps lib/pq through otelsql so query spans flow
	// into the active HTTP request span. otelsql's DisableQuery option is
	// set there to prevent SQL text from reaching span attributes; see
	// internal/tracing/db.go for the privacy rationale.
	db, err := tracing.OpenSQLDB("postgres", databaseURL, "digits")
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
		// v15: per-user CRT bezel preference for the dialup theme.
		// 'off' / 'connecting' (default) / 'all'.
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS crt_mode TEXT NOT NULL DEFAULT 'connecting'`,
		// v16: per-call link-health telemetry samples
		`CREATE TABLE IF NOT EXISTS call_link_health (
			call_id     INTEGER     NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
			endpoint    TEXT        NOT NULL,
			ts          TIMESTAMPTZ NOT NULL,
			loss_pct    REAL,
			jitter_ms   REAL,
			rtt_ms      REAL,
			conn_type   TEXT,
			bytes_in    BIGINT,
			bytes_out   BIGINT,
			PRIMARY KEY (call_id, endpoint, ts)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_call_link_health_call_ts
			ON call_link_health (call_id, ts DESC)`,
		// v17: party line (three-way calling) support.
		// Inner statements are idempotent (IF NOT EXISTS) so the block is safe on
		// environments where the tables were created under an earlier version=15
		// label (prod before this PR's rebase; main rebase-concurrently claimed 15
		// for the CRT bezel column above).
		`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 17) THEN

        CREATE TABLE IF NOT EXISTS conferences (
            id UUID PRIMARY KEY,
            host_phone TEXT NOT NULL,
            originating_call_id INTEGER NOT NULL REFERENCES calls(id),
            state TEXT NOT NULL CHECK (state IN ('active', 'ended')),
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            ended_at TIMESTAMPTZ,
            end_reason TEXT
        );

        CREATE INDEX IF NOT EXISTS conferences_host_phone_idx ON conferences(host_phone);
        CREATE INDEX IF NOT EXISTS conferences_state_idx ON conferences(state);

        CREATE TABLE IF NOT EXISTS conference_members (
            conference_id UUID NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
            phone TEXT NOT NULL,
            role TEXT NOT NULL CHECK (role IN ('host', 'added')),
            joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            left_at TIMESTAMPTZ,
            left_reason TEXT,
            PRIMARY KEY (conference_id, phone)
        );

        CREATE INDEX IF NOT EXISTS conference_members_phone_idx ON conference_members(phone);

        ALTER TABLE calls ADD COLUMN IF NOT EXISTS originating_conference_id UUID REFERENCES conferences(id);

        INSERT INTO schema_version (version) VALUES (17);
    END IF;
END $$;`,
		// v18: track why a call ended (e.g. 'merged_to_conference' vs a normal hangup)
		`ALTER TABLE calls ADD COLUMN IF NOT EXISTS end_reason TEXT`,
		// v19: capture which user initiated a force-disconnect on a call.
		// NULL for peer-initiated hangups.
		`ALTER TABLE calls ADD COLUMN IF NOT EXISTS force_ended_by UUID REFERENCES users(id)`,
		// v20: conference-scoped link-health samples.
		// Relaxes call_id to nullable and adds conference_id + peer with an
		// XOR CHECK so exactly one session type is attributed per row. The
		// old PRIMARY KEY (call_id, endpoint, ts) is replaced by two partial
		// unique indexes (one per session kind) and a conference-scoped ts
		// index used by the Phase 3 readback path. Gated on schema_version
		// so re-running on a v20-applied DB is a no-op.
		`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 20) THEN

        ALTER TABLE call_link_health DROP CONSTRAINT IF EXISTS call_link_health_pkey;
        ALTER TABLE call_link_health ALTER COLUMN call_id DROP NOT NULL;
        ALTER TABLE call_link_health ADD COLUMN IF NOT EXISTS conference_id UUID NULL REFERENCES conferences(id) ON DELETE CASCADE;
        ALTER TABLE call_link_health ADD COLUMN IF NOT EXISTS peer TEXT NULL;
        IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'call_link_health_exactly_one_session') THEN
            ALTER TABLE call_link_health ADD CONSTRAINT call_link_health_exactly_one_session
                CHECK ((call_id IS NOT NULL) != (conference_id IS NOT NULL));
        END IF;

        CREATE UNIQUE INDEX IF NOT EXISTS call_link_health_call_ep_ts
            ON call_link_health (call_id, endpoint, ts)
            WHERE call_id IS NOT NULL;
        CREATE UNIQUE INDEX IF NOT EXISTS call_link_health_conf_ep_peer_ts
            ON call_link_health (conference_id, endpoint, peer, ts)
            WHERE conference_id IS NOT NULL;
        CREATE INDEX IF NOT EXISTS idx_call_link_health_conf_ts
            ON call_link_health (conference_id, ts DESC)
            WHERE conference_id IS NOT NULL;

        INSERT INTO schema_version (version) VALUES (20);
    END IF;
END $$;`,
		// v21: enforce that conference-scoped link-health rows always have a
		// non-NULL peer. The partial unique index on (conference_id, endpoint,
		// peer, ts) cannot dedupe NULL peer rows because NULLs are distinct in
		// unique indexes; Go-side writeSample rejects this case but any non-Go
		// write path would bypass that guard. Belt-and-suspenders at the DB
		// layer.
		`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 21) THEN
        IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'call_link_health_conf_requires_peer') THEN
            ALTER TABLE call_link_health ADD CONSTRAINT call_link_health_conf_requires_peer
                CHECK (conference_id IS NULL OR peer IS NOT NULL);
        END IF;
        INSERT INTO schema_version (version) VALUES (21);
    END IF;
END $$;`,
		// v22: conference_kicks audit table. One row per host-triggered kick.
		// Append-only; cascade delete tied to the conferences parent.
		`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 22) THEN

        CREATE TABLE IF NOT EXISTS conference_kicks (
            id                BIGSERIAL PRIMARY KEY,
            conference_id     UUID NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
            kicked_phone      TEXT NOT NULL,
            kicked_by_user_id UUID NOT NULL REFERENCES users(id),
            kicked_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
        );
        CREATE INDEX IF NOT EXISTS idx_conference_kicks_conf
            ON conference_kicks (conference_id);

        INSERT INTO schema_version (version) VALUES (22);
    END IF;
END $$;`,
		// v23: per-user intercom appearance preference.
		// 'day' (default) / 'night'. Only meaningful when theme = 'intercom'.
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS appearance TEXT NOT NULL DEFAULT 'day'`,
		// v24: household-level do-not-disturb master switch.
		`ALTER TABLE households ADD COLUMN IF NOT EXISTS do_not_disturb BOOLEAN NOT NULL DEFAULT FALSE`,
		// v25: per-user "has picked a theme" flag for the first-time-login
		// theme picker. Pre-existing users are backfilled to TRUE so the
		// welcome wizard only fires for new accounts; the schema_version
		// guard (mirroring v22) ensures the backfill runs exactly once even
		// if a QA workflow has since flipped some rows back to FALSE.
		`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 25) THEN

        ALTER TABLE users ADD COLUMN IF NOT EXISTS theme_chosen BOOLEAN NOT NULL DEFAULT FALSE;
        UPDATE users SET theme_chosen = TRUE;

        INSERT INTO schema_version (version) VALUES (25);
    END IF;
END $$;`,

		// v26: household user invites + multi-household session scoping + auth return_to
		`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 26) THEN

        CREATE TABLE IF NOT EXISTS household_invites (
            id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            household_id  UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
            email         TEXT NOT NULL,
            invited_by    UUID NOT NULL REFERENCES users(id),
            token         TEXT NOT NULL UNIQUE,
            status        TEXT NOT NULL DEFAULT 'pending',
            created_at    TIMESTAMPTZ DEFAULT NOW(),
            accepted_at   TIMESTAMPTZ,
            expires_at    TIMESTAMPTZ NOT NULL
        );
        CREATE UNIQUE INDEX IF NOT EXISTS idx_household_invites_pending
            ON household_invites (household_id, email) WHERE status = 'pending';
        CREATE INDEX IF NOT EXISTS idx_household_invites_token
            ON household_invites (token);

        ALTER TABLE sessions ADD COLUMN IF NOT EXISTS active_household_id UUID REFERENCES households(id);

        ALTER TABLE magic_links ADD COLUMN IF NOT EXISTS return_to TEXT;

        INSERT INTO schema_version (version) VALUES (26);
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

// Package db provides the database connection and schema migration for
// signald. Use Open to obtain a Database value; it connects to Postgres,
// wraps the connection with OpenTelemetry tracing, and runs all pending
// migrations before returning.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/dbutil"
	"github.com/justinlindh/digits/server/internal/tracing"
)

// Database wraps *sql.DB with OpenTelemetry-instrumented query tracing.
// Use Open to obtain one; the zero value is not valid.
type Database struct {
	DB *sql.DB
}

// Open connects to the Postgres database at databaseURL, wraps it with
// OpenTelemetry SQL tracing, and runs all pending schema migrations before
// returning. Returns an error if the connection or any migration fails.
func Open(databaseURL string) (*Database, error) {
	// tracing.OpenSQLDB wraps lib/pq through otelsql so query spans flow
	// into the active HTTP request span. otelsql's DisableQuery option is
	// set there to prevent SQL text from reaching span attributes; see
	// internal/tracing/db.go for the privacy rationale.
	db, err := tracing.OpenSQLDB(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	// Low-concurrency process: 25 open / 5 idle covers bursts without
	// exhausting typical Postgres connection limits.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	d := &Database{DB: db}
	if err := d.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *Database) Close() error {
	return d.DB.Close()
}

// migrationLockKey is the pg_advisory_xact_lock key that serializes
// migration runners across processes sharing one database.
const migrationLockKey = 0x6469676974730001

// migration is one entry in migrations.
type migration struct {
	version int
	sql     string
}

// migrate applies every migration whose version is not yet recorded in
// schema_version. Applied versions are skipped without touching any
// application table, so a steady-state start takes no table locks.
//
// Each pending migration runs in a transaction that holds a
// transaction-scoped advisory lock: concurrent starters queue behind it,
// re-check the version once they hold the lock, and find it already
// applied. A failure rolls the version back whole; the next start retries
// it.
func (d *Database) migrate(ctx context.Context) error {
	// CREATE TABLE IF NOT EXISTS is not race-safe on a fresh database, so
	// the bootstrap takes the same advisory lock the migrations do.
	if err := d.locked(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (
			version INT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)`)
		return err
	}); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	applied, err := d.appliedVersions(ctx)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := d.apply(ctx, m); err != nil {
			return fmt.Errorf("v%d: %w", m.version, err)
		}
	}
	return nil
}

func (d *Database) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := d.DB.QueryContext(ctx, `SELECT version FROM schema_version`)
	if err != nil {
		return nil, fmt.Errorf("read schema_version: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("read schema_version: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// locked runs fn inside a transaction that holds the migration advisory
// lock, committing if fn returns nil.
func (d *Database) locked(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return dbutil.WithTx(ctx, d.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
			return fmt.Errorf("advisory lock: %w", err)
		}
		return fn(tx)
	})
}

func (d *Database) apply(ctx context.Context, m migration) error {
	return d.locked(ctx, func(tx *sql.Tx) error {
		// Claim the version under the lock; a conflict means another
		// starter applied it while we waited. The row only becomes visible
		// once the script below commits with it.
		res, err := tx.ExecContext(ctx,
			`INSERT INTO schema_version (version) VALUES ($1) ON CONFLICT DO NOTHING`, m.version)
		if err != nil {
			return fmt.Errorf("record schema_version: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return nil
		}
		// No bind parameters: lib/pq then uses the simple query protocol,
		// which accepts the multi-statement scripts below.
		_, err = tx.ExecContext(ctx, m.sql)
		return err
	})
}

// migrations is the full schema history, oldest first. Append a new
// version to add a migration; never edit or renumber an existing one, since
// databases that already recorded it will not run it again.
var migrations = []migration{
	// core phone/call tables (migrated from SQLite)
	{1, `
		CREATE TABLE IF NOT EXISTS phones (
			id          SERIAL PRIMARY KEY,
			number      TEXT NOT NULL UNIQUE,
			name        TEXT NOT NULL DEFAULT '',
			device_id   TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ DEFAULT NOW(),
			updated_at  TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS calls (
			id          SERIAL PRIMARY KEY,
			caller      TEXT NOT NULL,
			callee      TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'initiated',
			started_at  TIMESTAMPTZ DEFAULT NOW(),
			answered_at TIMESTAMPTZ,
			ended_at    TIMESTAMPTZ,
			duration_s  INTEGER DEFAULT 0
		)`},
	// user accounts + auth
	{2, `
		CREATE TABLE IF NOT EXISTS users (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email       TEXT NOT NULL UNIQUE,
			name        TEXT NOT NULL DEFAULT '',
			google_id   TEXT UNIQUE,
			created_at  TIMESTAMPTZ DEFAULT NOW(),
			last_login_at TIMESTAMPTZ
		);
		CREATE TABLE IF NOT EXISTS sessions (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash  TEXT NOT NULL UNIQUE,
			expires_at  TIMESTAMPTZ NOT NULL,
			created_at  TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token_hash);
		CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
		CREATE TABLE IF NOT EXISTS magic_links (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email       TEXT NOT NULL,
			token_hash  TEXT NOT NULL UNIQUE,
			expires_at  TIMESTAMPTZ NOT NULL,
			used        BOOLEAN DEFAULT FALSE,
			created_at  TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_magic_links_token ON magic_links(token_hash)`},
	// households + members, phones linked to households
	{3, `
		CREATE TABLE IF NOT EXISTS households (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name       TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS household_members (
			user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			household_id UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
			role         TEXT NOT NULL DEFAULT 'admin',
			created_at   TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (user_id, household_id)
		);
		CREATE INDEX IF NOT EXISTS idx_household_members_household ON household_members(household_id);
		ALTER TABLE phones ADD COLUMN IF NOT EXISTS household_id UUID REFERENCES households(id);
		CREATE INDEX IF NOT EXISTS idx_phones_household ON phones(household_id);
		ALTER TABLE households ADD COLUMN IF NOT EXISTS call_history_enabled BOOLEAN NOT NULL DEFAULT false`},
	// phone pairing
	{4, `
		ALTER TABLE phones ADD COLUMN IF NOT EXISTS pairing_code TEXT;
		ALTER TABLE phones ADD COLUMN IF NOT EXISTS pairing_code_expires_at TIMESTAMPTZ;
		ALTER TABLE phones ADD COLUMN IF NOT EXISTS paired_at TIMESTAMPTZ;
		ALTER TABLE phones ADD COLUMN IF NOT EXISTS hardware_id TEXT UNIQUE;
		CREATE INDEX IF NOT EXISTS idx_phones_pairing_code ON phones(pairing_code) WHERE pairing_code IS NOT NULL;
		CREATE INDEX IF NOT EXISTS idx_phones_hardware_id ON phones(hardware_id) WHERE hardware_id IS NOT NULL`},
	// household linking, contacts + contact_invites
	{5, `
		CREATE TABLE IF NOT EXISTS household_links (
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
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_household_links_invite_code_pending ON household_links(invite_code) WHERE status = 'pending';
		CREATE INDEX IF NOT EXISTS idx_household_links_a ON household_links(household_a_id);
		CREATE INDEX IF NOT EXISTS idx_household_links_b ON household_links(household_b_id) WHERE household_b_id IS NOT NULL;
		CREATE TABLE IF NOT EXISTS contacts (
			id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			phone_id          INT NOT NULL REFERENCES phones(id) ON DELETE CASCADE,
			contact_phone_id  INT NOT NULL REFERENCES phones(id) ON DELETE CASCADE,
			name              TEXT NOT NULL DEFAULT '',
			created_at        TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(phone_id, contact_phone_id)
		);
		CREATE INDEX IF NOT EXISTS idx_contacts_phone_id ON contacts(phone_id);
		CREATE TABLE IF NOT EXISTS contact_invites (
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
		);
		CREATE INDEX IF NOT EXISTS idx_contact_invites_to_phone_pending ON contact_invites(to_phone_id) WHERE status = 'pending';
		ALTER TABLE phones ADD COLUMN IF NOT EXISTS device_token TEXT`},
	// lines + devices (replaces phones, contacts, contact_invites)
	{6, `
		CREATE TABLE IF NOT EXISTS lines (
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
		DROP TABLE IF EXISTS phones`},
	// allow devices to exist before pairing (line_id NULL until paired)
	{7, `ALTER TABLE devices ALTER COLUMN line_id DROP NOT NULL`},
	// household timezone
	{8, `ALTER TABLE households ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT 'UTC'`},
	// device last-seen timestamp
	{9, `ALTER TABLE devices ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ`},
	// hash existing plaintext device tokens with SHA-256
	{10, `
		UPDATE devices SET device_token = encode(sha256(device_token::bytea), 'hex')
		WHERE device_token IS NOT NULL`},
	// per-line settings JSONB column (voice_style, etc.)
	{11, `ALTER TABLE lines ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}'::jsonb`},
	// user-selectable webapp theme ('intercom' = default, 'dialup' = 1997 online-service alternate)
	{12, `ALTER TABLE users ADD COLUMN IF NOT EXISTS theme TEXT NOT NULL DEFAULT 'intercom'`},
	// rename earlier theme identifier 'aol' -> 'dialup'
	{13, `UPDATE users SET theme = 'dialup' WHERE theme = 'aol'`},
	// rename earlier theme identifier 'c' -> 'intercom' and update the column default
	{14, `
		UPDATE users SET theme = 'intercom' WHERE theme = 'c';
		ALTER TABLE users ALTER COLUMN theme SET DEFAULT 'intercom'`},
	// per-user CRT bezel preference for the dialup theme:
	// 'off' / 'connecting' (default) / 'all'.
	{15, `ALTER TABLE users ADD COLUMN IF NOT EXISTS crt_mode TEXT NOT NULL DEFAULT 'connecting'`},
	// per-call link-health telemetry samples
	{16, `
		CREATE TABLE IF NOT EXISTS call_link_health (
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
		);
		CREATE INDEX IF NOT EXISTS idx_call_link_health_call_ts
			ON call_link_health (call_id, ts DESC)`},
	// party line (three-way calling) support
	{17, `
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

		ALTER TABLE calls ADD COLUMN IF NOT EXISTS originating_conference_id UUID REFERENCES conferences(id)`},
	// track why a call ended (e.g. 'merged_to_conference' vs a normal hangup)
	{18, `ALTER TABLE calls ADD COLUMN IF NOT EXISTS end_reason TEXT`},
	// capture which user initiated a force-disconnect on a call;
	// NULL for peer-initiated hangups
	{19, `ALTER TABLE calls ADD COLUMN IF NOT EXISTS force_ended_by UUID REFERENCES users(id)`},
	// conference-scoped link-health samples. Relaxes call_id to nullable and
	// adds conference_id + peer with an XOR CHECK so exactly one session type
	// is attributed per row. The old PRIMARY KEY (call_id, endpoint, ts) is
	// replaced by two partial unique indexes (one per session kind) and a
	// conference-scoped ts index used by the readback path.
	{20, `
		ALTER TABLE call_link_health DROP CONSTRAINT IF EXISTS call_link_health_pkey;
		ALTER TABLE call_link_health ALTER COLUMN call_id DROP NOT NULL;
		ALTER TABLE call_link_health ADD COLUMN IF NOT EXISTS conference_id UUID NULL REFERENCES conferences(id) ON DELETE CASCADE;
		ALTER TABLE call_link_health ADD COLUMN IF NOT EXISTS peer TEXT NULL;
		ALTER TABLE call_link_health ADD CONSTRAINT call_link_health_exactly_one_session
			CHECK ((call_id IS NOT NULL) != (conference_id IS NOT NULL));

		CREATE UNIQUE INDEX IF NOT EXISTS call_link_health_call_ep_ts
			ON call_link_health (call_id, endpoint, ts)
			WHERE call_id IS NOT NULL;
		CREATE UNIQUE INDEX IF NOT EXISTS call_link_health_conf_ep_peer_ts
			ON call_link_health (conference_id, endpoint, peer, ts)
			WHERE conference_id IS NOT NULL;
		CREATE INDEX IF NOT EXISTS idx_call_link_health_conf_ts
			ON call_link_health (conference_id, ts DESC)
			WHERE conference_id IS NOT NULL`},
	// conference-scoped link-health rows always carry a peer. The partial
	// unique index on (conference_id, endpoint, peer, ts) cannot dedupe NULL
	// peer rows because NULLs are distinct in unique indexes.
	{21, `
		ALTER TABLE call_link_health ADD CONSTRAINT call_link_health_conf_requires_peer
			CHECK (conference_id IS NULL OR peer IS NOT NULL)`},
	// conference_kicks audit table: one row per host-triggered kick,
	// append-only, cascade delete tied to the conferences parent
	{22, `
		CREATE TABLE IF NOT EXISTS conference_kicks (
			id                BIGSERIAL PRIMARY KEY,
			conference_id     UUID NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
			kicked_phone      TEXT NOT NULL,
			kicked_by_user_id UUID NOT NULL REFERENCES users(id),
			kicked_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_conference_kicks_conf
			ON conference_kicks (conference_id)`},
	// per-user intercom appearance preference: 'day' (default) / 'night';
	// only meaningful when theme = 'intercom'
	{23, `ALTER TABLE users ADD COLUMN IF NOT EXISTS appearance TEXT NOT NULL DEFAULT 'day'`},
	// per-user "has picked a theme" flag for the first-time-login theme
	// picker. Pre-existing users are backfilled to TRUE so the welcome
	// wizard only fires for new accounts.
	{25, `
		ALTER TABLE users ADD COLUMN IF NOT EXISTS theme_chosen BOOLEAN NOT NULL DEFAULT FALSE;
		UPDATE users SET theme_chosen = TRUE`},
	// household user invites + multi-household session scoping + auth return_to
	{26, `
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

		ALTER TABLE magic_links ADD COLUMN IF NOT EXISTS return_to TEXT`},
	// per-device name column. Previously line.name served double duty as
	// both the line label and the handset label. With multiple handsets per
	// line, each device needs its own name. Backfill copies the line name to
	// the first paired device on each line.
	{27, `
		ALTER TABLE devices ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';

		UPDATE devices d
		SET name = l.name
		FROM lines l
		WHERE d.line_id = l.id
		  AND d.paired_at IS NOT NULL
		  AND d.name = ''`},
	// allow user deletion by relaxing FK constraints that reference users(id)
	{28, `
		-- household_links.invited_by: drop NOT NULL, swap FK to SET NULL
		ALTER TABLE household_links ALTER COLUMN invited_by DROP NOT NULL;
		ALTER TABLE household_links DROP CONSTRAINT IF EXISTS household_links_invited_by_fkey;
		ALTER TABLE household_links ADD CONSTRAINT household_links_invited_by_fkey
			FOREIGN KEY (invited_by) REFERENCES users(id) ON DELETE SET NULL;

		-- household_links.accepted_by: already nullable, swap FK
		ALTER TABLE household_links DROP CONSTRAINT IF EXISTS household_links_accepted_by_fkey;
		ALTER TABLE household_links ADD CONSTRAINT household_links_accepted_by_fkey
			FOREIGN KEY (accepted_by) REFERENCES users(id) ON DELETE SET NULL;

		-- household_links.revoked_by: already nullable, swap FK
		ALTER TABLE household_links DROP CONSTRAINT IF EXISTS household_links_revoked_by_fkey;
		ALTER TABLE household_links ADD CONSTRAINT household_links_revoked_by_fkey
			FOREIGN KEY (revoked_by) REFERENCES users(id) ON DELETE SET NULL;

		-- household_invites.invited_by: drop NOT NULL, swap FK
		ALTER TABLE household_invites ALTER COLUMN invited_by DROP NOT NULL;
		ALTER TABLE household_invites DROP CONSTRAINT IF EXISTS household_invites_invited_by_fkey;
		ALTER TABLE household_invites ADD CONSTRAINT household_invites_invited_by_fkey
			FOREIGN KEY (invited_by) REFERENCES users(id) ON DELETE SET NULL;

		-- calls.force_ended_by: already nullable, swap FK
		ALTER TABLE calls DROP CONSTRAINT IF EXISTS calls_force_ended_by_fkey;
		ALTER TABLE calls ADD CONSTRAINT calls_force_ended_by_fkey
			FOREIGN KEY (force_ended_by) REFERENCES users(id) ON DELETE SET NULL;

		-- conference_kicks.kicked_by_user_id: drop NOT NULL, swap FK
		ALTER TABLE conference_kicks ALTER COLUMN kicked_by_user_id DROP NOT NULL;
		ALTER TABLE conference_kicks DROP CONSTRAINT IF EXISTS conference_kicks_kicked_by_user_id_fkey;
		ALTER TABLE conference_kicks ADD CONSTRAINT conference_kicks_kicked_by_user_id_fkey
			FOREIGN KEY (kicked_by_user_id) REFERENCES users(id) ON DELETE SET NULL;

		-- sessions.active_household_id: swap FK to SET NULL so household
		-- deletion does not fail when a session points at the household.
		ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_active_household_id_fkey;
		ALTER TABLE sessions ADD CONSTRAINT sessions_active_household_id_fkey
			FOREIGN KEY (active_household_id) REFERENCES households(id) ON DELETE SET NULL`},
	// remove the household-level do_not_disturb flag; "Silence All" is
	// derived from per-line silent_mode settings
	{29, `ALTER TABLE households DROP COLUMN IF EXISTS do_not_disturb`},
	// drop the settings key/value table, a v1 SQLite-port artifact that no
	// code ever read or wrote
	{30, `DROP TABLE IF EXISTS settings`},
	// index the append-only calls table on caller and callee. Call history
	// and *69 lookups filter on these columns and sort by started_at DESC;
	// without indexes they degrade to full sequential scans as the table
	// grows.
	{31, `
		CREATE INDEX IF NOT EXISTS idx_calls_caller ON calls(caller, started_at DESC);
		CREATE INDEX IF NOT EXISTS idx_calls_callee ON calls(callee, started_at DESC)`},
}

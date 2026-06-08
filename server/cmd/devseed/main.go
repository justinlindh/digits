// devseed populates the local dev database with a comprehensive scenario
// for iterating on UI: a primary dialup user plus additional households
// linked to them, each with multiple phone lines, plus one outstanding
// pending invite. This means `make dev-up` lands on a rendered dashboard
// AND /links hub / Today panel / pending-invite row are all populated
// without hand-wiring fixtures per session.
//
// The seeder is idempotent: every entity is checked before being created.
// Running twice does not duplicate users, households, lines, links, or
// invites. Running against a partially-seeded DB fills in whatever is
// missing.
//
// Minimal mode (single primary user, no extras) is available via the
// -minimal flag for callers that want the old behavior.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
)

// seededUser describes one user's full state after seeding.
type seededUser struct {
	Email         string
	DisplayName   string
	HouseholdName string
	Theme         auth.Theme
	Lines         []seededLine
	LinkToPrimary bool // when true, the seeder wires an accepted link to the primary household
}

type seededLine struct {
	Number string
	Name   string
}

var (
	primary = seededUser{
		Email:         "dev@digits.local",
		DisplayName:   "Dev",
		HouseholdName: "Lindh Family",
		Theme:         auth.ThemeDialup,
		Lines: []seededLine{
			{"2480001", "Kitchen"},
			{"2480002", "Living room"},
			{"2480003", "Garage"},
		},
	}

	secondMember = seededUser{
		Email:       "other@digits.local",
		DisplayName: "Other",
		Theme:       auth.ThemeIntercom,
	}

	others = []seededUser{
		{
			Email:         "grandma@digits.local",
			DisplayName:   "Grandma",
			HouseholdName: "Grandma Lindh",
			Theme:         auth.ThemeIntercom,
			Lines: []seededLine{
				{"5550001", "Bedroom"},
			},
			LinkToPrimary: true,
		},
		{
			Email:         "coopers@digits.local",
			DisplayName:   "Cooper",
			HouseholdName: "The Coopers",
			Theme:         auth.ThemeIntercom,
			Lines: []seededLine{
				{"3120001", "Kitchen"},
				{"3120002", "Studio"},
			},
			LinkToPrimary: true,
		},
		{
			Email:         "whitfields@digits.local",
			DisplayName:   "Whitfield",
			HouseholdName: "Whitfield Cabin",
			Theme:         auth.ThemeDialup,
			Lines: []seededLine{
				{"7070001", "Cabin"},
			},
			LinkToPrimary: true,
		},
		{
			Email:         "hayashis@digits.local",
			DisplayName:   "Hayashi",
			HouseholdName: "Hayashi family",
			Theme:         auth.ThemeAnsweringMachine,
			Lines: []seededLine{
				{"4150001", "Living room"},
				{"4150002", "Kitchen"},
			},
			LinkToPrimary: true,
		},
		{
			Email:         "marquezes@digits.local",
			DisplayName:   "Marquez",
			HouseholdName: "Marquez household",
			Theme:         auth.ThemeIntercom,
			Lines: []seededLine{
				{"6190001", "Garage"},
			},
			LinkToPrimary: true,
		},
	}
)

type stores struct {
	auth  *auth.Store
	house *household.Store
	link  *household.LinkStore
	line  *line.Store
	db    *sql.DB
}

func main() {
	var (
		baseURL     = flag.String("base-url", envOr("BASE_URL", "http://localhost:8080"), "Base URL to embed in the printed sign-in link")
		databaseURL = flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection string (falls back to DATABASE_URL)")
		minimal     = flag.Bool("minimal", false, "Seed only the primary dialup user with no lines, links, or pending invites")
	)
	flag.Parse()

	if *databaseURL == "" {
		log.Fatal("DATABASE_URL (or -database-url) must be set")
	}

	ctx := context.Background()
	database, err := db.Open(*databaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = database.Close() }()

	s := &stores{
		auth:  auth.NewStore(database.DB),
		house: household.NewStore(database.DB),
		link:  household.NewLinkStore(database.DB),
		line:  line.NewStore(database),
		db:    database.DB,
	}

	primaryUser, primaryHH, err := ensureUser(ctx, s, primary, *minimal)
	if err != nil {
		log.Fatalf("seed primary: %v", err)
	}

	if *minimal {
		printSummary(*baseURL, []string{primary.Email}, primary.Email)
		return
	}

	// Seed device rows with names for the primary household's lines.
	// The Kitchen line (248-0001) gets two handsets to exercise multi-device UI.
	for _, d := range []struct{ number, name, hwID string }{
		{"2480001", "Kitchen", "dev-hw-kitchen"},
		{"2480001", "Hallway", "dev-hw-hallway"},
		{"2480002", "Living room", "dev-hw-living"},
		{"2480003", "Garage", "dev-hw-garage"},
	} {
		if err := ensureDeviceWithName(ctx, s.db, s.line, d.number, d.name, d.hwID); err != nil {
			log.Printf("seed device %s on %s: %v", d.hwID, d.number, err)
		}
	}

	// Add a second member to the primary household
	secondUser, err := upsertUser(ctx, s.auth, secondMember.Email, secondMember.DisplayName)
	if err != nil {
		log.Fatalf("seed second member: %v", err)
	}
	if err := s.auth.SetTheme(ctx, secondUser.ID, secondMember.Theme); err != nil {
		log.Fatalf("set second member theme: %v", err)
	}
	if err := s.auth.MarkThemeChosen(ctx, secondUser.ID); err != nil {
		log.Fatalf("mark second member theme chosen: %v", err)
	}
	if err := s.house.AddMember(ctx, secondUser.ID, primaryHH.ID, "admin"); err != nil {
		log.Fatalf("add second member: %v", err)
	}
	seededEmails := []string{primary.Email, secondMember.Email}

	// Seed a pending user invite on the primary household
	invStore := household.NewInviteStore(database.DB)
	pending, err := invStore.IsPendingForHouseholdEmail(ctx, primaryHH.ID, "pending@digits.local")
	if err != nil {
		log.Fatalf("check pending invite: %v", err)
	}
	if !pending {
		if _, err := invStore.CreateInvite(ctx, primaryHH.ID, "pending@digits.local", primaryUser.ID); err != nil {
			log.Fatalf("seed pending invite: %v", err)
		}
	}

	if err := s.house.SetCallHistoryEnabled(ctx, primaryHH.ID, true); err != nil {
		log.Fatalf("enable call history on primary: %v", err)
	}

	for _, o := range others {
		otherUser, otherHH, err := ensureUser(ctx, s, o, false)
		if err != nil {
			log.Fatalf("seed %s: %v", o.Email, err)
		}
		seededEmails = append(seededEmails, o.Email)
		if o.LinkToPrimary {
			if err := ensureAcceptedLink(ctx, s.link, primaryHH.ID, primaryUser.ID, otherHH.ID, otherUser.ID); err != nil {
				log.Fatalf("link %s to primary: %v", o.Email, err)
			}
		}
	}

	if err := ensurePendingInvite(ctx, s.link, primaryHH.ID, primaryUser.ID); err != nil {
		log.Fatalf("ensure pending invite on primary: %v", err)
	}

	printSummary(*baseURL, seededEmails, primary.Email)
}

// ensureUser guarantees the user, their household, their theme, and their
// lines are in the configured state. If minimal is true, only the user + an
// empty household are ensured (no lines).
func ensureUser(ctx context.Context, s *stores, spec seededUser, minimal bool) (*auth.User, *household.Household, error) {
	u, err := upsertUser(ctx, s.auth, spec.Email, spec.DisplayName)
	if err != nil {
		return nil, nil, fmt.Errorf("upsert user: %w", err)
	}
	if err := s.auth.SetTheme(ctx, u.ID, spec.Theme); err != nil {
		return nil, nil, fmt.Errorf("set theme: %w", err)
	}
	// Seeded users have a deterministic theme already, so flip the flag so
	// they don't bounce through /welcome on every dev-up. To exercise the
	// picker locally, run:
	//   psql $DATABASE_URL -c "UPDATE users SET theme_chosen = FALSE WHERE email = 'dev@digits.local'"
	if err := s.auth.MarkThemeChosen(ctx, u.ID); err != nil {
		return nil, nil, fmt.Errorf("mark theme chosen: %w", err)
	}
	hh, err := ensureHousehold(ctx, s.house, u.ID, spec.HouseholdName)
	if err != nil {
		return nil, nil, fmt.Errorf("ensure household: %w", err)
	}
	if hh.Name != spec.HouseholdName {
		if err := s.house.UpdateName(ctx, hh.ID, spec.HouseholdName); err != nil {
			return nil, nil, fmt.Errorf("rename household: %w", err)
		}
		hh.Name = spec.HouseholdName
	}
	if !minimal {
		for _, l := range spec.Lines {
			if err := ensureLine(ctx, s.line, l.Number, l.Name, hh.ID); err != nil {
				return nil, nil, fmt.Errorf("ensure line %s: %w", l.Number, err)
			}
		}
	}
	return u, hh, nil
}

// upsertUser returns the existing user for email, or creates one with the
// given display name. The display name is only used on first insert.
func upsertUser(ctx context.Context, s *auth.Store, email, displayName string) (*auth.User, error) {
	u, err := s.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		return u, nil
	case errors.Is(err, auth.ErrUserNotFound):
		return s.CreateUser(ctx, email, displayName, nil)
	default:
		return nil, fmt.Errorf("lookup user: %w", err)
	}
}

// ensureHousehold returns the user's first household, creating one with the
// desired name if the user has none.
func ensureHousehold(ctx context.Context, s *household.Store, userID, name string) (*household.Household, error) {
	if !s.NeedsOnboarding(ctx, userID) {
		hs, err := s.GetForUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		if len(hs) == 0 {
			return nil, errors.New("user reports not needing onboarding but has no households")
		}
		return hs[0], nil
	}
	return s.Create(ctx, name, userID)
}

// ensureLine creates a phone line only if none exists with that number.
// Phone numbers are globally unique, so GetByNumber is the natural key.
func ensureLine(ctx context.Context, s *line.Store, number, name, householdID string) error {
	existing, err := s.GetByNumber(ctx, number)
	if err == nil && existing != nil {
		return nil
	}
	if err != nil && !errors.Is(err, line.ErrNotFound) {
		return err
	}
	if _, err := s.Add(ctx, number, name, householdID); err != nil {
		return err
	}
	return nil
}

// ensureAcceptedLink wires an accepted link between two households if one
// does not already exist. Uses the production invite/accept flow so the
// seeded link is indistinguishable from a user-created one.
func ensureAcceptedLink(ctx context.Context, s *household.LinkStore, primaryHHID, primaryUserID, otherHHID, otherUserID string) error {
	linked, err := s.AreLinked(ctx, primaryHHID, otherHHID)
	if err != nil {
		return fmt.Errorf("check link: %w", err)
	}
	if linked {
		return nil
	}
	invite, err := s.CreateInvite(ctx, primaryHHID, primaryUserID)
	if err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	if _, err := s.AcceptInvite(ctx, invite.InviteCode, otherUserID, otherHHID); err != nil {
		return fmt.Errorf("accept invite: %w", err)
	}
	return nil
}

// ensurePendingInvite makes sure the primary household has exactly one
// pending-invite code outstanding so the Families page renders its
// "Pending invites sent" row. Does nothing if one already exists.
func ensurePendingInvite(ctx context.Context, s *household.LinkStore, householdID, userID string) error {
	pending, err := s.GetPendingForHousehold(ctx, householdID)
	if err != nil {
		return fmt.Errorf("get pending: %w", err)
	}
	if len(pending) > 0 {
		return nil
	}
	if _, err := s.CreateInvite(ctx, householdID, userID); err != nil {
		return fmt.Errorf("create pending invite: %w", err)
	}
	return nil
}

func printSummary(baseURL string, emails []string, primaryEmail string) {
	fmt.Println()
	fmt.Println("Seeded users:")
	for _, e := range emails {
		fmt.Printf("  - %s\n", e)
	}
	fmt.Println()
	fmt.Println("Sign in as the primary dialup user:")
	fmt.Printf("  %s/auth/dev-session?email=%s\n",
		strings.TrimRight(baseURL, "/"),
		url.QueryEscape(primaryEmail),
	)
	fmt.Println()
	fmt.Println("The server must be running with DEV_MODE=true for the dev-session endpoint to work.")
}

// ensureDeviceWithName creates a paired device row for the given line number
// if one with that hardware_id does not already exist. Idempotent.
func ensureDeviceWithName(ctx context.Context, db *sql.DB, lineStore *line.Store, lineNumber, deviceName, hardwareID string) error {
	ln, err := lineStore.GetByNumber(ctx, lineNumber)
	if err != nil {
		return fmt.Errorf("line %s not found: %w", lineNumber, err)
	}
	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM devices WHERE hardware_id = $1)`,
		hardwareID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("existence check for %s: %w", hardwareID, err)
	}
	if exists {
		if _, err := db.ExecContext(ctx,
			`UPDATE devices SET name = $1 WHERE hardware_id = $2`,
			deviceName, hardwareID,
		); err != nil {
			return fmt.Errorf("update name for %s: %w", hardwareID, err)
		}
		return nil
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO devices (line_id, hardware_id, name, paired_at, device_token)
		 VALUES ($1, $2, $3, NOW(), 'devseed-token-' || $2)`,
		ln.ID, hardwareID, deviceName,
	); err != nil {
		return fmt.Errorf("insert device %s on line %s: %w", hardwareID, lineNumber, err)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

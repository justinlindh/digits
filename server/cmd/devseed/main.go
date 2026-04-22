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
	}
)

type stores struct {
	auth  *auth.Store
	house *household.Store
	link  *household.LinkStore
	line  *line.Store
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

	database, err := db.Open(*databaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = database.Close() }()

	s := &stores{
		auth:  auth.NewStoreFromDB(database.DB),
		house: household.NewStore(database.DB),
		link:  household.NewLinkStore(database.DB),
		line:  line.NewStore(database),
	}

	primaryUser, primaryHH, err := ensureUser(s, primary, *minimal)
	if err != nil {
		log.Fatalf("seed primary: %v", err)
	}

	if *minimal {
		printSummary(*baseURL, []string{primary.Email}, primary.Email)
		return
	}

	if err := s.house.SetCallHistoryEnabled(primaryHH.ID, true); err != nil {
		log.Fatalf("enable call history on primary: %v", err)
	}

	seededEmails := []string{primary.Email}
	for _, o := range others {
		otherUser, otherHH, err := ensureUser(s, o, false)
		if err != nil {
			log.Fatalf("seed %s: %v", o.Email, err)
		}
		seededEmails = append(seededEmails, o.Email)
		if o.LinkToPrimary {
			if err := ensureAcceptedLink(s.link, primaryHH.ID, primaryUser.ID, otherHH.ID, otherUser.ID); err != nil {
				log.Fatalf("link %s to primary: %v", o.Email, err)
			}
		}
	}

	if err := ensurePendingInvite(s.link, primaryHH.ID, primaryUser.ID); err != nil {
		log.Fatalf("ensure pending invite on primary: %v", err)
	}

	printSummary(*baseURL, seededEmails, primary.Email)
}

// ensureUser guarantees the user, their household, their theme, and their
// lines are in the configured state. If minimal is true, only the user + an
// empty household are ensured (no lines).
func ensureUser(s *stores, spec seededUser, minimal bool) (*auth.User, *household.Household, error) {
	u, err := upsertUser(s.auth, spec.Email, spec.DisplayName)
	if err != nil {
		return nil, nil, fmt.Errorf("upsert user: %w", err)
	}
	if err := s.auth.SetTheme(u.ID, spec.Theme); err != nil {
		return nil, nil, fmt.Errorf("set theme: %w", err)
	}
	hh, err := ensureHousehold(s.house, u.ID, spec.HouseholdName)
	if err != nil {
		return nil, nil, fmt.Errorf("ensure household: %w", err)
	}
	if hh.Name != spec.HouseholdName {
		if err := s.house.UpdateName(hh.ID, spec.HouseholdName); err != nil {
			return nil, nil, fmt.Errorf("rename household: %w", err)
		}
		hh.Name = spec.HouseholdName
	}
	if !minimal {
		for _, l := range spec.Lines {
			if err := ensureLine(s.line, l.Number, l.Name, hh.ID); err != nil {
				return nil, nil, fmt.Errorf("ensure line %s: %w", l.Number, err)
			}
		}
	}
	return u, hh, nil
}

// upsertUser returns the existing user for email, or creates one with the
// given display name. The display name is only used on first insert.
func upsertUser(s *auth.Store, email, displayName string) (*auth.User, error) {
	u, err := s.GetUserByEmail(email)
	switch {
	case err == nil:
		return u, nil
	case errors.Is(err, auth.ErrUserNotFound):
		return s.CreateUser(email, displayName, nil)
	default:
		return nil, fmt.Errorf("lookup user: %w", err)
	}
}

// ensureHousehold returns the user's first household, creating one with the
// desired name if the user has none.
func ensureHousehold(s *household.Store, userID, name string) (*household.Household, error) {
	if !s.NeedsOnboarding(userID) {
		hs, err := s.GetForUser(userID)
		if err != nil {
			return nil, err
		}
		if len(hs) == 0 {
			return nil, fmt.Errorf("user reports not needing onboarding but has no households")
		}
		return hs[0], nil
	}
	return s.Create(name, userID)
}

// ensureLine creates a phone line only if none exists with that number.
// Phone numbers are globally unique, so GetByNumber is the natural key.
func ensureLine(s *line.Store, number, name, householdID string) error {
	existing, err := s.GetByNumber(number)
	if err == nil && existing != nil {
		return nil
	}
	if err != nil && !errors.Is(err, line.ErrNotFound) {
		return err
	}
	if _, err := s.Add(number, name, householdID); err != nil {
		return err
	}
	return nil
}

// ensureAcceptedLink wires an accepted link between two households if one
// does not already exist. Uses the production invite/accept flow so the
// seeded link is indistinguishable from a user-created one.
func ensureAcceptedLink(s *household.LinkStore, primaryHHID, primaryUserID, otherHHID, otherUserID string) error {
	linked, err := s.AreLinked(primaryHHID, otherHHID)
	if err != nil {
		return fmt.Errorf("check link: %w", err)
	}
	if linked {
		return nil
	}
	invite, err := s.CreateInvite(primaryHHID, primaryUserID)
	if err != nil {
		return fmt.Errorf("create invite: %w", err)
	}
	if _, err := s.AcceptInvite(invite.InviteCode, otherUserID, otherHHID); err != nil {
		return fmt.Errorf("accept invite: %w", err)
	}
	return nil
}

// ensurePendingInvite makes sure the primary household has exactly one
// pending-invite code outstanding so the Families page renders its
// "Pending invites sent" row. Does nothing if one already exists.
func ensurePendingInvite(s *household.LinkStore, householdID, userID string) error {
	pending, err := s.GetPendingForHousehold(householdID)
	if err != nil {
		return fmt.Errorf("get pending: %w", err)
	}
	if len(pending) > 0 {
		return nil
	}
	if _, err := s.CreateInvite(householdID, userID); err != nil {
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

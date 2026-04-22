// devseed creates (or updates) a single dialup-themed user + household in the
// local dev database so `make dev-up` lands the operator on a rendered
// dashboard after one click.
//
// It is idempotent: running it twice does not create duplicate users or
// households. It prints a /auth/dev-session URL at the end which the operator
// can paste into a browser to sign in.
//
// This binary is intentionally minimal. It is not meant for production
// seeding; it has no flags beyond what is needed to bootstrap one dialup user.
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
)

func main() {
	var (
		email       = flag.String("email", envOr("DEV_SEED_EMAIL", "dev@digits.local"), "Email address for the seeded dev user")
		baseURL     = flag.String("base-url", envOr("BASE_URL", "http://localhost:8080"), "Base URL to embed in the printed sign-in link")
		databaseURL = flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection string (falls back to DATABASE_URL)")
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

	authStore := auth.NewStoreFromDB(database.DB)
	houseStore := household.NewStore(database.DB)

	user, created, err := upsertUser(authStore, *email)
	if err != nil {
		log.Fatalf("upsert user: %v", err)
	}
	if err := authStore.SetTheme(user.ID, auth.ThemeDialup); err != nil {
		log.Fatalf("set theme: %v", err)
	}

	hh, err := ensureHousehold(houseStore, user.ID)
	if err != nil {
		log.Fatalf("ensure household: %v", err)
	}

	verb := "updated"
	if created {
		verb = "created"
	}
	fmt.Printf("User %s %s (id=%s, theme=dialup)\n", *email, verb, user.ID)
	fmt.Printf("Household: %s (id=%s)\n", hh.Name, hh.ID)
	fmt.Println()
	fmt.Println("Sign in by opening this URL in your browser:")
	fmt.Printf("  %s/auth/dev-session?email=%s\n", strings.TrimRight(*baseURL, "/"), url.QueryEscape(*email))
	fmt.Println()
	fmt.Println("The server must be running with DEV_MODE=true for the dev-session endpoint to work.")
}

// upsertUser returns the existing user for email, or creates a new one. The
// second return value reports whether a new row was inserted.
func upsertUser(s *auth.Store, email string) (*auth.User, bool, error) {
	u, err := s.GetUserByEmail(email)
	switch {
	case err == nil:
		return u, false, nil
	case errors.Is(err, auth.ErrUserNotFound):
		created, err := s.CreateUser(email, "Dev", nil)
		if err != nil {
			return nil, false, err
		}
		return created, true, nil
	default:
		return nil, false, fmt.Errorf("lookup user: %w", err)
	}
}

// ensureHousehold returns the user's first household, creating one if they
// have none. Running again is a no-op — NeedsOnboarding gates creation.
func ensureHousehold(s *household.Store, userID string) (*household.Household, error) {
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
	return s.Onboard(userID, "Dev")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

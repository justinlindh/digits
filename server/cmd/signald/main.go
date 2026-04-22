package main

import (
	"context"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/config"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/logging"
	"github.com/justinlindh/digits/server/internal/pairing"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/turn"
	"github.com/justinlindh/digits/server/internal/updates"
	"github.com/justinlindh/digits/server/internal/web"
)

func main() {
	logging.Setup()

	cfg := config.Load()

	// Open database
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL must be set")
	}
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = database.Close() }()

	// Create components
	lineStore := line.NewStore(database)
	deviceStore := device.NewStore(database)
	hub := signaling.NewHub()
	tracker := calls.New(database)

	// Household store
	householdStore := household.NewStore(database.DB)

	// Pairing store
	pairingStore := pairing.NewStore(database.DB)

	// Link store
	linkStore := household.NewLinkStore(database.DB)

	flushDisabled := os.Getenv("SIGNALD_LINK_HEALTH_FLUSH_DISABLED") == "1"
	healthStore := calls.NewHealthStore(database, calls.WithFlushDisabled(flushDisabled))
	if flushDisabled {
		slog.Warn("link-health flusher disabled via SIGNALD_LINK_HEALTH_FLUSH_DISABLED")
	}
	tracker.SetHealthStore(healthStore)

	healthCtx, cancelHealth := context.WithCancel(context.Background())
	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		healthStore.Run(healthCtx)
	}()
	defer func() {
		cancelHealth()
		<-healthDone // wait for final flush before DB close unwinds
	}()

	relay := signaling.NewRelay(hub, tracker, line.NewAuthorizer(database), signaling.NewLineStoreAdapter(lineStore))
	relay.HealthStore = healthStore

	// Configure TURN credential generation if enabled
	if cfg.TURNEnabled {
		if cfg.TURNSecret == "" {
			log.Fatal("SIGNALD_TURN_SECRET must be set when TURN is enabled")
		}
		relay.TURNGen = turn.NewCredentialGenerator(cfg.TURNSecret, 24*time.Hour)
		relay.TURNDomain = cfg.TURNDomain
		slog.Info("TURN credential generation enabled", "domain", cfg.TURNDomain)
	}

	// Auth setup
	authStore := auth.NewStoreFromDB(database.DB)
	authStore.CookieDomain = cfg.CookieDomain

	var emailSender email.Sender
	if cfg.SMTPHost != "" {
		emailSender = email.NewSMTPSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
		slog.Info("SMTP sender configured", "host", cfg.SMTPHost)
	} else {
		emailSender = email.NewNoopSender()
		slog.Warn("no SMTP configured, magic link emails will be logged only")
	}

	googleAuth := auth.NewGoogleAuth(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL, cfg.CookieDomain, authStore)
	if googleAuth.Enabled() {
		slog.Info("Google OAuth enabled")
	}

	loginTmpl, err := template.ParseFS(web.TemplateFS(), "templates/layout-v2.html", "templates/_partials.html", "templates/login.html")
	if err != nil {
		log.Fatalf("parse login template: %v", err)
	}

	if cfg.DevMode {
		slog.Warn("dev mode enabled — magic link URLs will be logged to stdout")
	}
	authHandlers := auth.NewHandlers(authStore, googleAuth, emailSender, cfg.BaseURL, cfg.CookieDomain, loginTmpl, cfg.DevMode)

	// Periodic cleanup: sessions, magic links, expired pairing codes
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := authStore.CleanupExpired(); err != nil {
				slog.Error("session cleanup failed", "err", err)
			}
			if n, err := pairingStore.CleanupExpired(); err != nil {
				slog.Error("pairing cleanup failed", "err", err)
			} else if n > 0 {
				slog.Info("pairing cleanup complete", "expired_codes", n)
			}
		}
	}()

	// Create web handler
	handler, err := web.NewHandler(web.Deps{
		LineStore:      lineStore,
		DeviceStore:    deviceStore,
		Hub:            hub,
		Tracker:        tracker,
		Relay:          relay,
		HealthStore:    healthStore,
		AuthStore:      authStore,
		AuthHandlers:   authHandlers,
		GoogleAuth:     googleAuth,
		HouseholdStore: householdStore,
		PairingStore:   pairingStore,
		LinkStore:      linkStore,
		EmailSender:    emailSender,
	}, web.HandlerConfig{
		Addr:        cfg.Addr,
		BaseURL:     cfg.BaseURL,
		AdminSecret: cfg.AdminSecret,
		DevMode:     cfg.DevMode,
	})
	if err != nil {
		log.Fatalf("create handler: %v", err)
	}

	// Release index: prefer a static fixture (e2e/CI) over live GitHub data.
	if os.Getenv("TEST_FAKE_UPDATES") == "1" {
		handler.Releases = updates.FakeReleaseIndex()
		slog.Info("updates: using fake release index (TEST_FAKE_UPDATES=1)")
	} else if ghRepo := os.Getenv("GITHUB_REPO"); ghRepo != "" {
		parts := strings.SplitN(ghRepo, "/", 2)
		if len(parts) == 2 {
			gh := updates.NewGitHubReleases(parts[0], parts[1], os.Getenv("GITHUB_TOKEN"), 300) // 5 min cache
			handler.Releases = gh
			slog.Info("updates: release index from GitHub", "repo", ghRepo)
		} else {
			slog.Warn("GITHUB_REPO must be in owner/repo format, ignoring", "value", ghRepo)
		}
	}

	// Start server
	slog.Info("server started", "addr", cfg.Addr)
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		slog.Info("TLS enabled", "cert", cfg.TLSCert, "key", cfg.TLSKey)
		if err := http.ListenAndServeTLS(cfg.Addr, cfg.TLSCert, cfg.TLSKey, handler.Router()); err != nil {
			log.Fatalf("listen TLS: %v", err)
		}
	} else {
		if err := http.ListenAndServe(cfg.Addr, handler.Router()); err != nil {
			log.Fatalf("listen: %v", err)
		}
	}
}

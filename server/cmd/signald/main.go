package main

import (
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
	defer database.Close()

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

	relay := signaling.NewRelay(hub, tracker, line.NewAuthorizer(database))

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

	loginTmpl, err := template.ParseFS(web.TemplateFS(), "templates/layout.html", "templates/login.html")
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
	handler, err := web.NewHandler(lineStore, deviceStore, hub, tracker, relay, web.HandlerConfig{
		Addr: cfg.Addr,
	}, authStore, authHandlers, googleAuth, householdStore, pairingStore, linkStore, emailSender, cfg.BaseURL, cfg.AdminSecret)
	if err != nil {
		log.Fatalf("create handler: %v", err)
	}

	// GitHub-backed release index
	if ghRepo := os.Getenv("GITHUB_REPO"); ghRepo != "" {
		parts := strings.SplitN(ghRepo, "/", 2)
		if len(parts) == 2 {
			gh := updates.NewGitHubReleases(parts[0], parts[1], 300) // 5 min cache
			if token := os.Getenv("GITHUB_TOKEN"); token != "" {
				gh.SetToken(token)
			}
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

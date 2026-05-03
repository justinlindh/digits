package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/config"
	"github.com/justinlindh/digits/server/internal/dashboard/events"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("signald exited with error", "err", err)
		os.Exit(1)
	}
}

// run is the real entrypoint: all wiring lives here, main stays a one-liner
// so the ListenAndServe/signal/exit path is testable and errors propagate
// through slog instead of bypassing it via log.Fatal.
func run(ctx context.Context) error {
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL must be set")
	}

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = database.Close() }()

	// Core stores
	lineStore := line.NewStore(database)
	deviceStore := device.NewStore(database)
	hub := signaling.NewHub()
	tracker := calls.New(database)
	householdStore := household.NewStore(database.DB)
	pairingStore := pairing.NewStore(database.DB)
	linkStore := household.NewLinkStore(database.DB)

	// Dashboard pub/sub: hub.Register/Unregister and tracker.OnCall* notify
	// this broadcaster so the /api/dashboard/stream SSE handler can re-render
	// counters without polling.
	dashEvents := events.New()
	hub.SetDashboardEvents(dashEvents)
	tracker.SetDashboardEvents(dashEvents)

	// Link-health store with its own lifecycle; flusher runs until ctx is cancelled.
	healthStore := calls.NewHealthStore(database, calls.WithFlushDisabled(cfg.LinkHealthFlushDisabled))
	if cfg.LinkHealthFlushDisabled {
		slog.Warn("link-health flusher disabled via SIGNALD_LINK_HEALTH_FLUSH_DISABLED")
	}
	tracker.SetHealthStore(healthStore)

	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		healthStore.Run(ctx)
	}()
	defer func() { <-healthDone }() // wait for final flush before DB close unwinds

	// Relay and TURN
	relay := signaling.NewRelay(hub, tracker, line.NewAuthorizer(database), signaling.NewLineStoreAdapter(lineStore))
	relay.HealthStore = healthStore
	if cfg.TURNEnabled {
		if cfg.TURNSecret == "" {
			return errors.New("SIGNALD_TURN_SECRET must be set when TURN is enabled")
		}
		relay.TURNGen = turn.NewCredentialGenerator(cfg.TURNSecret, 24*time.Hour)
		relay.TURNDomain = cfg.TURNDomain
		slog.Info("TURN credential generation enabled", "domain", cfg.TURNDomain)
	}

	// Auth
	authStore := auth.NewStore(database.DB)
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

	loginTmpl, err := template.New("").Funcs(web.TemplateFuncs()).ParseFS(web.TemplateFS(), "templates/layout-v2.html", "templates/_partials.html", "templates/login.html")
	if err != nil {
		return fmt.Errorf("parse login template: %w", err)
	}
	if cfg.DevMode {
		slog.Warn("dev mode enabled — magic link URLs will be logged to stdout")
	}
	authHandlers := auth.NewHandlers(authStore, googleAuth, emailSender, cfg.BaseURL, cfg.CookieDomain, loginTmpl, cfg.DevMode)

	// Periodic cleanup: sessions, magic links, expired pairing codes. Ticker
	// goroutine is bound to the same ctx as the main server so shutdown is
	// tidy (previous code leaked this goroutine on signal).
	go cleanupLoop(ctx, authStore, pairingStore)

	// Web handler
	handler, err := web.NewHandler(web.Deps{
		LineStore:      lineStore,
		DeviceStore:    deviceStore,
		Hub:            hub,
		Tracker:        tracker,
		Relay:          relay,
		HealthStore:    healthStore,
		DashEvents:     dashEvents,
		AuthStore:      authStore,
		AuthHandlers:   authHandlers,
		GoogleAuth:     googleAuth,
		HouseholdStore: householdStore,
		PairingStore:   pairingStore,
		LinkStore:      linkStore,
	}, web.HandlerConfig{
		Addr:        cfg.Addr,
		BaseURL:     cfg.BaseURL,
		AdminSecret: cfg.AdminSecret,
		DevMode:     cfg.DevMode,
	})
	if err != nil {
		return fmt.Errorf("create handler: %w", err)
	}

	// Release index: prefer the static fixture (e2e/CI) over live GitHub data.
	switch {
	case cfg.FakeUpdates:
		handler.Releases = updates.FakeReleaseIndex()
		slog.Info("updates: using fake release index (TEST_FAKE_UPDATES=1)")
	case cfg.GitHubRepo != "":
		parts := strings.SplitN(cfg.GitHubRepo, "/", 2)
		if len(parts) == 2 {
			handler.Releases = updates.NewGitHubReleases(ctx, parts[0], parts[1], cfg.GitHubToken, 300, // 5 min cache
				func(piLatest, fwLatest string) {
					slog.Info("updates: new release detected, broadcasting", "pi", piLatest, "fw", fwLatest)
					handler.Hub().Broadcast(&signaling.Message{
						Type:            signaling.TypeReleaseAvailable,
						LatestPiVersion: piLatest,
						LatestFWVersion: fwLatest,
					})
				})
			slog.Info("updates: release index from GitHub", "repo", cfg.GitHubRepo)
		} else {
			slog.Warn("GITHUB_REPO must be in owner/repo format, ignoring", "value", cfg.GitHubRepo)
		}
	}

	// Serve. http.Server gives us graceful shutdown on ctx cancellation.
	srv := &http.Server{Addr: cfg.Addr, Handler: handler.Router()}
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("server started", "addr", cfg.Addr)
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			slog.Info("TLS enabled", "cert", cfg.TLSCert, "key", cfg.TLSKey)
			serveErr <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			serveErr <- srv.ListenAndServe()
		}
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		return nil
	}
}

// cleanupLoop runs the hourly cleanup sweep until ctx is cancelled.
// Extracted from run() only for readability; it has no other callers.
func cleanupLoop(ctx context.Context, authStore *auth.Store, pairingStore *pairing.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := authStore.CleanupExpired(ctx); err != nil {
				slog.Error("session cleanup failed", "err", err)
			}
			if n, err := pairingStore.CleanupExpired(ctx); err != nil {
				slog.Error("pairing cleanup failed", "err", err)
			} else if n > 0 {
				slog.Info("pairing cleanup complete", "expired_codes", n)
			}
		}
	}
}

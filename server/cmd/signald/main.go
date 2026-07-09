// Command signald is the Digits signaling server: a WebRTC signaling relay
// that brokers SDP/ICE exchange between phones so they can establish
// peer-to-peer encrypted audio. Audio never touches the server. Alongside
// signaling it manages households, device pairing, authentication, call
// history, and serves the embedded web UI. All wiring lives here; the real
// logic is in the internal packages.
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

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/config"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/events"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/logging"
	"github.com/justinlindh/digits/server/internal/metrics"
	"github.com/justinlindh/digits/server/internal/pairing"
	"github.com/justinlindh/digits/server/internal/profiling"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/tracing"
	"github.com/justinlindh/digits/server/internal/turn"
	"github.com/justinlindh/digits/server/internal/updates"
	"github.com/justinlindh/digits/server/internal/version"
	"github.com/justinlindh/digits/server/internal/web"
)

// releaseCacheTTL is the release index cache lifetime.
const releaseCacheTTL = 5 * time.Minute

// redisClient returns the shared Redis client from the bridge, or nil when
// Redis is not configured. The rate limiters use nil to select their
// per-process in-memory backend.
func redisClient(b *signaling.RedisBridge) redis.UniversalClient {
	if b == nil {
		return nil
	}
	return b.Client()
}

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
	slog.Info("starting signald", "version", version.Version, "commit", version.Commit, "pid", os.Getpid())
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL must be set")
	}

	// OpenTelemetry tracing. Endpoint is read from
	// OTEL_EXPORTER_OTLP_ENDPOINT; empty disables the exporter while
	// leaving in-process propagation on, so a future enable does not
	// require a code change. The shutdown closure flushes buffered spans
	// on a clean SIGTERM; deferred so a panic during run() still flushes.
	traceCfg := tracing.NewConfig("signald", version.Version, version.Commit)
	traceShutdown, err := tracing.Init(ctx, traceCfg)
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			slog.Warn("tracing shutdown", "err", err)
		}
	}()
	if traceCfg.Endpoint != "" {
		slog.Info("OpenTelemetry tracing enabled", "endpoint", traceCfg.Endpoint, "protocol", traceCfg.Protocol)
	}

	// Pyroscope continuous profiling. Server address is read from
	// PYROSCOPE_SERVER_ADDRESS; empty disables the profiler. Profiling
	// labels are a closed set; see internal/profiling for the rationale.
	profCfg := profiling.NewConfig("signald")
	profStop, err := profiling.Init(profCfg, version.Version)
	if err != nil {
		return fmt.Errorf("init profiling: %w", err)
	}
	defer func() {
		if err := profStop(); err != nil {
			slog.Warn("profiling shutdown", "err", err)
		}
	}()
	if profCfg.ServerAddress != "" {
		slog.Info("Pyroscope profiling enabled", "endpoint", profCfg.ServerAddress)
	}

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = database.Close() }()

	// Core stores
	lineStore := line.NewStore(database.DB)
	deviceStore := device.NewStore(database.DB)
	hub := signaling.NewHub()

	// Redis pub/sub for multi-replica signaling. When REDIS_URL is set,
	// the hub publishes to a shared channel when a target device is not
	// connected to this pod. Other pods subscribe and deliver locally.
	var redisBridge *signaling.RedisBridge
	if cfg.RedisURL != "" {
		var err error
		redisBridge, err = signaling.NewRedisBridge(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("connect redis: %w", err)
		}
		defer func() { _ = redisBridge.Close() }()
		if err := redisBridge.Ping(ctx); err != nil {
			return fmt.Errorf("redis ping: %w", err)
		}
		hub.SetRedis(redisBridge)
		go hub.Run(ctx)
		slog.Info("redis pub/sub enabled for multi-replica signaling")
	}

	tracker := calls.New(database.DB)
	if redisBridge != nil {
		rc := redisBridge.Client()
		podID := redisBridge.PodID()

		hub.SetDeviceState(signaling.NewDeviceState(rc, podID))
		tracker.SetCallState(calls.NewCallState(rc))
		tracker.Conferences().SetConfState(calls.NewConfState(rc))
		slog.Info("redis cluster state enabled for multi-replica operation")
	}
	householdStore := household.NewStore(database.DB)
	pairingStore := pairing.NewStore(database.DB)
	linkStore := household.NewLinkStore(database.DB)

	// Prometheus metrics. The registry is wired into the web handler as
	// middleware (HTTP request count + duration) and into the signaling
	// relay (signaling_errors_total). Live-state gauges sample the hub and
	// tracker at scrape time so we never persist counts elsewhere.
	mreg := metrics.New(version.Version, version.Commit)
	mreg.RegisterDevicesGauge(func() float64 { return float64(hub.LocalConnectionCount()) })
	mreg.RegisterCallsGauge(func() float64 { return float64(len(tracker.Active(context.Background()))) })

	// Dashboard pub/sub: hub.Register/Unregister and tracker.OnCall* notify
	// this broadcaster so the /api/dashboard/stream SSE handler can re-render
	// counters without polling.
	dashEvents := events.New()
	if redisBridge != nil {
		dashEvents.SetRedis(redisBridge.Client(), redisBridge.PodID())
		go dashEvents.RunRedis(ctx)
	}
	hub.SetDashboardEvents(dashEvents)
	tracker.SetDashboardEvents(dashEvents)

	// Link-health store with its own lifecycle; flusher runs until ctx is cancelled.
	healthStore := calls.NewHealthStore(database.DB)
	if cfg.LinkHealthFlushDisabled {
		healthStore.DisableFlush()
		slog.Warn("link-health flusher disabled via SIGNALD_LINK_HEALTH_FLUSH_DISABLED")
	}
	tracker.SetHealthStore(healthStore)
	if redisBridge != nil {
		healthStore.SetRedis(redisBridge.Client(), redisBridge.PodID())
		go healthStore.RunRedis(ctx)
		slog.Info("redis fan-out enabled for cross-pod link health")
	}

	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		healthStore.Run(ctx)
	}()
	defer func() { <-healthDone }() // wait for final flush before DB close unwinds

	// Relay and TURN
	relay := signaling.NewRelay(hub, tracker, line.NewAuthorizer(database.DB), signaling.NewLineStoreAdapter(lineStore))
	relay.HealthStore = healthStore
	relay.Metrics = mreg
	tracker.SetCallEndObserver(relay)
	hub.SetReconnectHook(relay.HandleRemoteReconnect)
	hub.SetDropHook(func() { mreg.ObserveSignalingError("send_buffer_full") })
	if cfg.TURNEnabled {
		if cfg.TURNSecret == "" {
			return errors.New("SIGNALD_TURN_SECRET must be set when TURN is enabled")
		}
		relay.TURNGen = turn.NewCredentialGenerator(cfg.TURNSecret, 2*time.Hour)
		relay.TURNDomain = cfg.TURNDomain
		slog.Info("TURN credential generation enabled", "domain", cfg.TURNDomain)
	}

	// Quiet-hours scheduler: re-evaluates each online line's effective silent
	// state on a one-minute cadence and pushes updated line settings when a
	// scheduled window opens or closes, so a device that stays online across a
	// boundary reflects the new state without waiting for a reconnect.
	quietHours := signaling.NewQuietHoursScheduler(hub, signaling.NewLineStoreAdapter(lineStore))
	go quietHours.Run(ctx)

	// Auth
	authStore := auth.NewStore(database.DB)
	authStore.CookieDomain = cfg.CookieDomain

	var emailSender email.Sender
	if cfg.SMTPHost != "" {
		emailSender = email.NewSMTPSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
		slog.Info("SMTP sender configured", "host", cfg.SMTPHost)
	} else {
		emailSender = email.NewLogSender()
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
		slog.Warn("dev mode enabled: magic link URLs will be logged to stdout")
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
		InviteStore:    household.NewInviteStore(database.DB),
		Emailer:        emailSender,
		Metrics:        mreg,
		RedisClient:    redisClient(redisBridge),
	}, web.HandlerConfig{
		BaseURL:           cfg.BaseURL,
		AdminSecret:       cfg.AdminSecret,
		DevMode:           cfg.DevMode,
		WSRateLimitPerMin: cfg.WSRateLimitPerMin,
		TrustedProxies:    cfg.TrustedProxies,
	})
	if err != nil {
		return fmt.Errorf("create handler: %w", err)
	}

	// Release index: prefer the static fixture (e2e/CI) over live GitHub data.
	switch {
	case cfg.FakeUpdates:
		handler.SetReleases(updates.FakeReleaseIndex())
		slog.Info("updates: using fake release index (TEST_FAKE_UPDATES=1)")
	case cfg.GitHubRepo != "":
		parts := strings.SplitN(cfg.GitHubRepo, "/", 2)
		if len(parts) == 2 {
			handler.SetReleases(updates.NewGitHubReleases(ctx, parts[0], parts[1], cfg.GitHubToken, releaseCacheTTL,
				func(piLatest, fwLatest string) {
					slog.Info("updates: new release detected, broadcasting", "pi", piLatest, "fw", fwLatest)
					handler.Hub().Broadcast(&signaling.Message{
						Type:            signaling.TypeReleaseAvailable,
						LatestPiVersion: piLatest,
						LatestFWVersion: fwLatest,
					})
				}))
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

	// Metrics listener. Bound to a separate addr/port so /metrics is never
	// served over the public app listener. Operators are expected to keep
	// this address on a private interface (the docker prod compose binds it
	// to 127.0.0.1; cluster scrapes reach it via the docker host IP). Empty
	// SIGNALD_METRICS_ADDR disables the listener entirely.
	var metricsSrv *http.Server
	metricsErr := make(chan error, 1)
	if cfg.MetricsAddr != "" {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("GET /metrics", promhttp.HandlerFor(mreg.Reg, promhttp.HandlerOpts{Registry: mreg.Reg}))
		metricsMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})
		metricsSrv = &http.Server{Addr: cfg.MetricsAddr, Handler: metricsMux}
		go func() {
			slog.Info("metrics listener started", "addr", cfg.MetricsAddr)
			metricsErr <- metricsSrv.ListenAndServe()
		}()
	}

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("listen: %w", err)
	case err := <-metricsErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("metrics listen: %w", err)
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining connections")

		// 25s budget leaves 5s headroom before k8s SIGKILL at 30s.
		drainCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()

		// 1. Stop accepting new WebSocket upgrades (returns 503).
		hub.StartDraining()

		// 2. Shut down HTTP server (waits for non-hijacked requests).
		if err := srv.Shutdown(drainCtx); err != nil {
			slog.Warn("http shutdown", "err", err)
		}

		// 3. Close remaining WebSocket connections gracefully (close frame 1001).
		hub.DrainAndClose(drainCtx)

		// 4. Shut down metrics listener.
		if metricsSrv != nil {
			if err := metricsSrv.Shutdown(drainCtx); err != nil {
				slog.Warn("metrics shutdown", "err", err)
			}
		}

		slog.Info("shutdown complete")
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

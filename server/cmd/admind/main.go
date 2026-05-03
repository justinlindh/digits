package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/justinlindh/digits/server/internal/admin"
	"github.com/justinlindh/digits/server/internal/logging"
	"github.com/justinlindh/digits/server/internal/metrics"
	"github.com/justinlindh/digits/server/internal/profiling"
	"github.com/justinlindh/digits/server/internal/tracing"
	"github.com/justinlindh/digits/server/internal/version"
)

func main() {
	logging.Setup()

	cfg := admin.LoadConfig()

	if cfg.AdminDBURL == "" {
		log.Fatal("ADMIN_DATABASE_URL must be set")
	}
	if cfg.StatsSecret == "" {
		log.Fatal("ADMIN_STATS_SECRET must be set")
	}

	// OpenTelemetry tracing. See cmd/signald/main.go for the same wiring
	// rationale; admind's spans become children of the upstream caller's
	// span when one is present (e.g. an admin browsing /admin/users
	// while the operator's local mesh is forwarding traceparent), and
	// the outbound stats client to signald injects traceparent so the
	// cross-service trace is continuous.
	tctx, tcancel := context.WithCancel(context.Background())
	defer tcancel()
	traceCfg := tracing.NewConfig(string(metrics.ServiceAdmind), version.Version, version.Commit)
	traceShutdown, err := tracing.Init(tctx, traceCfg)
	if err != nil {
		log.Fatalf("init tracing: %v", err)
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

	// Pyroscope continuous profiling. See internal/profiling for the
	// label-set rationale.
	profCfg := profiling.NewConfig(string(metrics.ServiceAdmind))
	profStop, err := profiling.Init(profCfg, version.Version)
	if err != nil {
		log.Fatalf("init profiling: %v", err)
	}
	defer func() {
		if err := profStop(); err != nil {
			slog.Warn("profiling shutdown", "err", err)
		}
	}()
	if profCfg.ServerAddress != "" {
		slog.Info("Pyroscope profiling enabled", "endpoint", profCfg.ServerAddress)
	}

	db, err := admin.OpenAdmin(context.Background(), cfg.AdminDBURL)
	if err != nil {
		log.Fatalf("open admin db: %v", err)
	}
	defer func() { _ = db.Close() }()

	authStore := admin.NewAuthStore(db)

	// Bootstrap initial admin if configured and doesn't exist yet
	if cfg.InitialAdmin != "" && cfg.InitialSecret != "" {
		hash, err := admin.HashSecret(cfg.InitialSecret)
		if err != nil {
			log.Fatalf("hash initial secret: %v", err)
		}
		id, err := authStore.CreateAdmin(context.Background(), cfg.InitialAdmin, hash)
		if err != nil {
			slog.Warn("initial admin creation failed", "username", cfg.InitialAdmin, "err", err)
		} else if id == "" {
			slog.Info("initial admin already exists", "username", cfg.InitialAdmin)
		} else {
			slog.Info("initial admin created", "username", cfg.InitialAdmin, "id", id)
		}
	}

	srv := admin.NewServer(cfg, db, authStore)

	// Metrics listener. Bound to a separate addr/port so /metrics is never
	// served over the admin web listener (which the operator may expose
	// behind auth at admin.digits.family). Empty ADMIN_METRICS_ADDR
	// disables the listener entirely.
	if cfg.MetricsAddr != "" {
		mreg := srv.Metrics()
		go func() {
			mux := http.NewServeMux()
			mux.Handle("GET /metrics", promhttp.HandlerFor(mreg.Reg, promhttp.HandlerOpts{Registry: mreg.Reg}))
			mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			})
			slog.Info("metrics listener started", "addr", cfg.MetricsAddr)
			if err := http.ListenAndServe(cfg.MetricsAddr, mux); err != nil {
				slog.Error("metrics listener exited", "err", err)
			}
		}()
	}

	slog.Info("server started", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/justinlindh/digits/server/internal/admin"
	"github.com/justinlindh/digits/server/internal/logging"
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
			slog.Warn("initial admin creation skipped", "username", cfg.InitialAdmin, "err", err)
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

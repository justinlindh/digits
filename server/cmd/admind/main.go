package main

import (
	"log"
	"log/slog"

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

	db, err := admin.OpenAdmin(cfg.AdminDBURL)
	if err != nil {
		log.Fatalf("open admin db: %v", err)
	}
	defer db.Close()

	authStore := admin.NewAuthStore(db)

	// Bootstrap initial admin if configured and doesn't exist yet
	if cfg.InitialAdmin != "" && cfg.InitialSecret != "" {
		hash, err := admin.HashSecret(cfg.InitialSecret)
		if err != nil {
			log.Fatalf("hash initial secret: %v", err)
		}
		id, err := authStore.CreateAdmin(cfg.InitialAdmin, hash)
		if err != nil {
			slog.Warn("initial admin creation skipped", "username", cfg.InitialAdmin, "err", err)
		} else {
			slog.Info("initial admin created", "username", cfg.InitialAdmin, "id", id)
		}
	}

	srv := admin.NewServer(cfg, db, authStore)
	slog.Info("server started", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

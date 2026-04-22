package admin

import "github.com/justinlindh/digits/server/internal/config"

type Config struct {
	Addr          string // Listen address
	AdminDBURL    string // Admin database URL
	StatsURL      string // digits-server internal stats URL
	StatsSecret   string // Shared secret for stats API
	InitialAdmin  string // Username for initial admin (created on first boot)
	InitialSecret string // Password for initial admin
}

func LoadConfig() *Config {
	c := &Config{
		Addr: ":9090",
	}
	config.StringEnv("ADMIN_ADDR", &c.Addr)
	config.StringEnv("ADMIN_DATABASE_URL", &c.AdminDBURL)
	config.StringEnv("ADMIN_STATS_URL", &c.StatsURL)
	config.StringEnv("ADMIN_STATS_SECRET", &c.StatsSecret)
	config.StringEnv("ADMIN_INITIAL_USER", &c.InitialAdmin)
	config.StringEnv("ADMIN_INITIAL_SECRET", &c.InitialSecret)
	return c
}

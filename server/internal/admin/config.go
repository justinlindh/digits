package admin

import "os"

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
	if v := os.Getenv("ADMIN_ADDR"); v != "" {
		c.Addr = v
	}
	if v := os.Getenv("ADMIN_DATABASE_URL"); v != "" {
		c.AdminDBURL = v
	}
	if v := os.Getenv("ADMIN_STATS_URL"); v != "" {
		c.StatsURL = v
	}
	if v := os.Getenv("ADMIN_STATS_SECRET"); v != "" {
		c.StatsSecret = v
	}
	if v := os.Getenv("ADMIN_INITIAL_USER"); v != "" {
		c.InitialAdmin = v
	}
	if v := os.Getenv("ADMIN_INITIAL_SECRET"); v != "" {
		c.InitialSecret = v
	}
	return c
}

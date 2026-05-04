package config

import "os"

type Config struct {
	Addr        string // HTTP listen address (public app port)
	MetricsAddr string // Prometheus metrics listen address (separate listener; empty disables)
	DatabaseURL string // Postgres connection string
	TLSCert     string // TLS certificate path (empty = plain HTTP)
	TLSKey      string // TLS key path
	TURNEnabled bool   // Whether TURN credential generation is enabled
	TURNSecret  string // Shared secret for HMAC-SHA1 credential generation
	TURNDomain  string // TURN server domain (e.g. turn.example.com)
	// Auth
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	BaseURL            string // Base URL for the app (e.g. https://app.digits.family)
	CookieDomain       string // Cookie domain (e.g. .digits.family for subdomain sharing)
	// Email
	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string
	SMTPFrom string
	// Admin
	AdminSecret string
	// Dev
	DevMode bool
	// Link health flusher: when true, calls.NewHealthStore starts with the
	// background DB flush disabled. Used by integration tests that drive time.
	LinkHealthFlushDisabled bool
	// Redis pub/sub for multi-replica signaling. When set, the hub publishes
	// signaling messages to a Redis channel when the target device is not on
	// the local pod. Empty disables Redis (single-instance mode).
	RedisURL string

	// Release index source. Exactly one of FakeUpdates or GitHubRepo (owner/repo)
	// should be set. FakeUpdates wins if both are set and is intended for e2e
	// tests. An unset GitHubRepo disables the release endpoint entirely.
	FakeUpdates bool
	GitHubRepo  string // e.g. "justinlindh/digits"
	GitHubToken string
}

func Load() *Config {
	c := &Config{
		Addr:        ":8443",
		MetricsAddr: ":9091",
		SMTPPort:    "587",
		SMTPFrom:    "noreply@digits.family",
		BaseURL:     "https://app.digits.family",
	}
	StringEnv("SIGNALD_ADDR", &c.Addr)
	StringEnv("SIGNALD_METRICS_ADDR", &c.MetricsAddr)
	StringEnv("DATABASE_URL", &c.DatabaseURL)
	StringEnv("SIGNALD_TLS_CERT", &c.TLSCert)
	StringEnv("SIGNALD_TLS_KEY", &c.TLSKey)
	BoolEnv("SIGNALD_TURN_ENABLED", &c.TURNEnabled)
	StringEnv("SIGNALD_TURN_SECRET", &c.TURNSecret)
	StringEnv("SIGNALD_TURN_DOMAIN", &c.TURNDomain)
	// Auth
	StringEnv("GOOGLE_CLIENT_ID", &c.GoogleClientID)
	StringEnv("GOOGLE_CLIENT_SECRET", &c.GoogleClientSecret)
	StringEnv("GOOGLE_REDIRECT_URL", &c.GoogleRedirectURL)
	StringEnv("BASE_URL", &c.BaseURL)
	StringEnv("COOKIE_DOMAIN", &c.CookieDomain)
	// Email
	StringEnv("SMTP_HOST", &c.SMTPHost)
	StringEnv("SMTP_PORT", &c.SMTPPort)
	StringEnv("SMTP_USER", &c.SMTPUser)
	StringEnv("SMTP_PASS", &c.SMTPPass)
	StringEnv("SMTP_FROM", &c.SMTPFrom)
	// Admin
	StringEnv("ADMIN_SECRET", &c.AdminSecret)
	// Dev
	BoolEnv("DEV_MODE", &c.DevMode)
	// Link health: env is "1" (not "true") to match the daemon's convention.
	if os.Getenv("SIGNALD_LINK_HEALTH_FLUSH_DISABLED") == "1" {
		c.LinkHealthFlushDisabled = true
	}
	// Redis
	StringEnv("REDIS_URL", &c.RedisURL)
	// Release index
	if os.Getenv("TEST_FAKE_UPDATES") == "1" {
		c.FakeUpdates = true
	}
	StringEnv("GITHUB_REPO", &c.GitHubRepo)
	StringEnv("GITHUB_TOKEN", &c.GitHubToken)
	return c
}

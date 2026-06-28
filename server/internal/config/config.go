// Package config loads signald's runtime configuration from environment
// variables. All settings live in Config; call Load to populate one with
// environment values and built-in defaults.
package config

// Config holds all runtime configuration for signald, populated by Load from
// environment variables. Zero values are overridden by Load's defaults where
// appropriate.
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

	// WSRateLimitPerMin overrides the default WebSocket upgrade rate limit
	// (per IP, per minute). Default is 30. Set higher for load testing.
	WSRateLimitPerMin int

	// TrustedProxies is the number of reverse-proxy hops between signald and
	// the real client, used to resolve the client IP from X-Forwarded-For for
	// rate limiting. Default is 1 (a single Traefik in front of signald). Set
	// to 0 to ignore X-Forwarded-For entirely (direct exposure).
	TrustedProxies int

	// Release index source. Exactly one of FakeUpdates or GitHubRepo (owner/repo)
	// should be set. FakeUpdates wins if both are set and is intended for e2e
	// tests. An unset GitHubRepo disables the release endpoint entirely.
	FakeUpdates bool
	GitHubRepo  string // e.g. "justinlindh/digits"
	GitHubToken string
}

// Load reads environment variables and returns a populated Config. Fields not
// present in the environment retain their defaults (e.g. Addr ":8443").
func Load() *Config {
	c := &Config{
		Addr:              ":8443",
		MetricsAddr:       ":9091",
		SMTPPort:          "587",
		SMTPFrom:          "noreply@digits.family",
		BaseURL:           "https://app.digits.family",
		WSRateLimitPerMin: 30,
		TrustedProxies:    1,
	}
	stringEnv("SIGNALD_ADDR", &c.Addr)
	stringEnv("SIGNALD_METRICS_ADDR", &c.MetricsAddr)
	stringEnv("DATABASE_URL", &c.DatabaseURL)
	stringEnv("SIGNALD_TLS_CERT", &c.TLSCert)
	stringEnv("SIGNALD_TLS_KEY", &c.TLSKey)
	boolEnv("SIGNALD_TURN_ENABLED", &c.TURNEnabled)
	stringEnv("SIGNALD_TURN_SECRET", &c.TURNSecret)
	stringEnv("SIGNALD_TURN_DOMAIN", &c.TURNDomain)
	// Auth
	stringEnv("GOOGLE_CLIENT_ID", &c.GoogleClientID)
	stringEnv("GOOGLE_CLIENT_SECRET", &c.GoogleClientSecret)
	stringEnv("GOOGLE_REDIRECT_URL", &c.GoogleRedirectURL)
	stringEnv("BASE_URL", &c.BaseURL)
	stringEnv("COOKIE_DOMAIN", &c.CookieDomain)
	// Email
	stringEnv("SMTP_HOST", &c.SMTPHost)
	stringEnv("SMTP_PORT", &c.SMTPPort)
	stringEnv("SMTP_USER", &c.SMTPUser)
	stringEnv("SMTP_PASS", &c.SMTPPass)
	stringEnv("SMTP_FROM", &c.SMTPFrom)
	// Admin
	stringEnv("ADMIN_SECRET", &c.AdminSecret)
	// Dev
	boolEnv("DEV_MODE", &c.DevMode)
	oneEnv("SIGNALD_LINK_HEALTH_FLUSH_DISABLED", &c.LinkHealthFlushDisabled)
	// Redis
	stringEnv("REDIS_URL", &c.RedisURL)
	// Rate limits
	intEnv("SIGNALD_WS_RATE_LIMIT", &c.WSRateLimitPerMin)
	intEnv("SIGNALD_TRUSTED_PROXIES", &c.TrustedProxies)
	// Release index
	oneEnv("TEST_FAKE_UPDATES", &c.FakeUpdates)
	stringEnv("GITHUB_REPO", &c.GitHubRepo)
	stringEnv("GITHUB_TOKEN", &c.GitHubToken)
	return c
}

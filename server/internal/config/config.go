package config

import "os"

type Config struct {
	Addr        string // HTTP listen address
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
	// Release index source. Exactly one of FakeUpdates or GitHubRepo (owner/repo)
	// should be set. FakeUpdates wins if both are set and is intended for e2e
	// tests. An unset GitHubRepo disables the release endpoint entirely.
	FakeUpdates bool
	GitHubRepo  string // e.g. "justinlindh/digits"
	GitHubToken string
}

// stringEnv assigns a non-empty env var to *dst, keeping the current default
// if the variable is unset. Keeps Load() scannable instead of a wall of
// five-line if blocks.
func stringEnv(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

// boolEnv sets *dst to true iff the env var is literal "true". Matches the
// existing convention (a stricter-than-strconv.ParseBool check that rejects
// "1", "yes", etc.).
func boolEnv(key string, dst *bool) {
	if os.Getenv(key) == "true" {
		*dst = true
	}
}

func Load() *Config {
	c := &Config{
		Addr:     ":8443",
		SMTPPort: "587",
		SMTPFrom: "noreply@digits.family",
		BaseURL:  "https://app.digits.family",
	}
	stringEnv("SIGNALD_ADDR", &c.Addr)
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
	// Link health: env is "1" (not "true") to match the daemon's convention.
	if os.Getenv("SIGNALD_LINK_HEALTH_FLUSH_DISABLED") == "1" {
		c.LinkHealthFlushDisabled = true
	}
	// Release index
	if os.Getenv("TEST_FAKE_UPDATES") == "1" {
		c.FakeUpdates = true
	}
	stringEnv("GITHUB_REPO", &c.GitHubRepo)
	stringEnv("GITHUB_TOKEN", &c.GitHubToken)
	return c
}

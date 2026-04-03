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
}

func Load() *Config {
	c := &Config{
		Addr:     ":8443",
		SMTPPort: "587",
		SMTPFrom: "noreply@digits.family",
		BaseURL:  "https://app.digits.family",
	}
	if a := os.Getenv("SIGNALD_ADDR"); a != "" {
		c.Addr = a
	}
	if d := os.Getenv("DATABASE_URL"); d != "" {
		c.DatabaseURL = d
	}
	if cert := os.Getenv("SIGNALD_TLS_CERT"); cert != "" {
		c.TLSCert = cert
	}
	if key := os.Getenv("SIGNALD_TLS_KEY"); key != "" {
		c.TLSKey = key
	}
	if os.Getenv("SIGNALD_TURN_ENABLED") == "true" {
		c.TURNEnabled = true
	}
	if s := os.Getenv("SIGNALD_TURN_SECRET"); s != "" {
		c.TURNSecret = s
	}
	if d := os.Getenv("SIGNALD_TURN_DOMAIN"); d != "" {
		c.TURNDomain = d
	}
	// Auth
	if v := os.Getenv("GOOGLE_CLIENT_ID"); v != "" {
		c.GoogleClientID = v
	}
	if v := os.Getenv("GOOGLE_CLIENT_SECRET"); v != "" {
		c.GoogleClientSecret = v
	}
	if v := os.Getenv("GOOGLE_REDIRECT_URL"); v != "" {
		c.GoogleRedirectURL = v
	}
	if v := os.Getenv("BASE_URL"); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv("COOKIE_DOMAIN"); v != "" {
		c.CookieDomain = v
	}
	// Email
	if v := os.Getenv("SMTP_HOST"); v != "" {
		c.SMTPHost = v
	}
	if v := os.Getenv("SMTP_PORT"); v != "" {
		c.SMTPPort = v
	}
	if v := os.Getenv("SMTP_USER"); v != "" {
		c.SMTPUser = v
	}
	if v := os.Getenv("SMTP_PASS"); v != "" {
		c.SMTPPass = v
	}
	if v := os.Getenv("SMTP_FROM"); v != "" {
		c.SMTPFrom = v
	}
	// Admin
	if v := os.Getenv("ADMIN_SECRET"); v != "" {
		c.AdminSecret = v
	}
	// Dev
	if os.Getenv("DEV_MODE") == "true" {
		c.DevMode = true
	}
	return c
}

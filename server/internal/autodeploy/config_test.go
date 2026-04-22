package autodeploy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tok")
	if err := os.WriteFile(tokenPath, []byte("ghp_abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `
# comment
REPO=justinlindh/digits
TAG_PREFIX=server/v
COMPOSE_DIR=/opt/digits/server
COMPOSE_FILE=docker-compose.prod.yml
COMPOSE_PROJECT=digits-prod
COMPOSE_ENV_FILE=/opt/digits/server/.env.prod
SERVICES=signald admind
HEALTH_URLS=http://localhost:8090/healthz http://localhost:9094/healthz
GHCR_USERNAME=justinlindh
GHCR_TOKEN_FILE=` + tokenPath + `
STATE_FILE=/var/lib/digits-autodeploy/state.json
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=relay
SMTP_FROM=noreply@digits.family
ALERT_TO=you@example.com
HEALTH_TIMEOUT=90s
REVERT_HEALTH_TIMEOUT=60s
EMAIL_DEBOUNCE=30m
POLL_INTERVAL=60s
`
	cfgPath := filepath.Join(dir, "config.env")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Repo != "justinlindh/digits" {
		t.Errorf("Repo=%q", got.Repo)
	}
	if got.GHCRToken != "ghp_abc" {
		t.Errorf("GHCRToken=%q (want ghp_abc, trailing newline must be stripped)", got.GHCRToken)
	}
	if len(got.Services) != 2 || got.Services[0] != "signald" || got.Services[1] != "admind" {
		t.Errorf("Services=%v", got.Services)
	}
	if len(got.HealthURLs) != 2 {
		t.Errorf("HealthURLs=%v", got.HealthURLs)
	}
	if got.HealthTimeout != 90*time.Second {
		t.Errorf("HealthTimeout=%v", got.HealthTimeout)
	}
	if got.EmailDebounce != 30*time.Minute {
		t.Errorf("EmailDebounce=%v", got.EmailDebounce)
	}
}

func TestLoadConfigMissingRequired(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.env")
	if err := os.WriteFile(cfgPath, []byte("REPO=justinlindh/digits\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}
}

func TestLoadConfigOptionalTokenFileMissing(t *testing.T) {
	// GHCR_TOKEN_FILE points at a non-existent path. For public images the
	// token is optional, so a missing file should leave GHCRToken empty
	// rather than aborting config load.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.env")
	body := `
REPO=justinlindh/digits
TAG_PREFIX=server/v
COMPOSE_DIR=/srv
COMPOSE_FILE=docker-compose.prod.yml
COMPOSE_PROJECT=digits-prod
COMPOSE_ENV_FILE=/srv/.env.prod
SERVICES=signald
HEALTH_URLS=http://localhost:8090/healthz
GHCR_USERNAME=justinlindh
GHCR_TOKEN_FILE=` + filepath.Join(dir, "does-not-exist") + `
STATE_FILE=/tmp/state.json
ALERT_TO=alert@example
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v (missing optional token file should not abort)", err)
	}
	if got.GHCRToken != "" {
		t.Errorf("GHCRToken=%q, want empty", got.GHCRToken)
	}
}

func TestLoadConfigOptionalTokenFileUnreadable(t *testing.T) {
	// A file that exists but cannot be read (permission denied, etc) is a
	// real config problem, not an "optional unset" — must still error.
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tok")
	if err := os.WriteFile(tokenPath, []byte("ghp_x"), 0o000); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.env")
	body := `
REPO=justinlindh/digits
TAG_PREFIX=server/v
COMPOSE_DIR=/srv
COMPOSE_FILE=docker-compose.prod.yml
COMPOSE_PROJECT=digits-prod
COMPOSE_ENV_FILE=/srv/.env.prod
SERVICES=signald
HEALTH_URLS=http://localhost:8090/healthz
GHCR_USERNAME=justinlindh
GHCR_TOKEN_FILE=` + tokenPath + `
STATE_FILE=/tmp/state.json
ALERT_TO=alert@example
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for unreadable token file")
	}
}

func TestLoadConfigIgnoresCommentsAndBlankLines(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.env")
	_ = os.WriteFile(cfgPath, []byte("# only a comment\n\n\n"), 0o600)
	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error: missing required fields")
	}
}

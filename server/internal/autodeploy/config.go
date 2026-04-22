package autodeploy

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Repo                string
	TagPrefix           string
	ComposeDir          string
	ComposeFile         string
	ComposeProject      string
	ComposeEnvFile      string
	Services            []string
	HealthURLs          []string
	GHCRUsername        string
	GHCRToken           string
	StateFile           string
	SMTPHost            string
	SMTPPort            string
	SMTPUser            string
	SMTPPass            string
	SMTPFrom            string
	AlertTo             string
	HealthTimeout       time.Duration
	RevertHealthTimeout time.Duration
	EmailDebounce       time.Duration
	PollInterval        time.Duration
}

func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	raw := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf("malformed line: %q", line)
		}
		raw[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if err := sc.Err(); err != nil {
		return Config{}, err
	}

	c := Config{
		Repo:           raw["REPO"],
		TagPrefix:      raw["TAG_PREFIX"],
		ComposeDir:     raw["COMPOSE_DIR"],
		ComposeFile:    raw["COMPOSE_FILE"],
		ComposeProject: raw["COMPOSE_PROJECT"],
		ComposeEnvFile: raw["COMPOSE_ENV_FILE"],
		Services:       splitFields(raw["SERVICES"]),
		HealthURLs:     splitFields(raw["HEALTH_URLS"]),
		GHCRUsername:   raw["GHCR_USERNAME"],
		StateFile:      raw["STATE_FILE"],
		SMTPHost:       raw["SMTP_HOST"],
		SMTPPort:       raw["SMTP_PORT"],
		SMTPUser:       raw["SMTP_USER"],
		SMTPFrom:       raw["SMTP_FROM"],
		AlertTo:        raw["ALERT_TO"],
	}

	for _, r := range []struct {
		name, pathKey string
		dst           *string
	}{
		{"GHCR_TOKEN", "GHCR_TOKEN_FILE", &c.GHCRToken},
		{"SMTP_PASS", "SMTP_PASS_FILE", &c.SMTPPass},
	} {
		if p := raw[r.pathKey]; p != "" {
			b, err := os.ReadFile(p)
			if err != nil {
				return Config{}, fmt.Errorf("read %s=%s: %w", r.pathKey, p, err)
			}
			*r.dst = strings.TrimRight(string(b), "\r\n")
		}
	}

	durations := []struct {
		name string
		src  string
		dst  *time.Duration
		def  time.Duration
	}{
		{"HEALTH_TIMEOUT", raw["HEALTH_TIMEOUT"], &c.HealthTimeout, 90 * time.Second},
		{"REVERT_HEALTH_TIMEOUT", raw["REVERT_HEALTH_TIMEOUT"], &c.RevertHealthTimeout, 60 * time.Second},
		{"EMAIL_DEBOUNCE", raw["EMAIL_DEBOUNCE"], &c.EmailDebounce, 30 * time.Minute},
		{"POLL_INTERVAL", raw["POLL_INTERVAL"], &c.PollInterval, 60 * time.Second},
	}
	for _, d := range durations {
		if d.src == "" {
			*d.dst = d.def
			continue
		}
		v, err := time.ParseDuration(d.src)
		if err != nil {
			return Config{}, fmt.Errorf("parse %s=%q: %w", d.name, d.src, err)
		}
		*d.dst = v
	}

	required := []struct{ name, val string }{
		{"REPO", c.Repo},
		{"TAG_PREFIX", c.TagPrefix},
		{"COMPOSE_DIR", c.ComposeDir},
		{"COMPOSE_FILE", c.ComposeFile},
		{"COMPOSE_PROJECT", c.ComposeProject},
		{"COMPOSE_ENV_FILE", c.ComposeEnvFile},
		{"GHCR_USERNAME", c.GHCRUsername},
		{"STATE_FILE", c.StateFile},
		{"ALERT_TO", c.AlertTo},
	}
	for _, r := range required {
		if r.val == "" {
			return Config{}, fmt.Errorf("missing required field: %s", r.name)
		}
	}
	if len(c.Services) == 0 {
		return Config{}, fmt.Errorf("missing required field: SERVICES")
	}
	if len(c.HealthURLs) == 0 {
		return Config{}, fmt.Errorf("missing required field: HEALTH_URLS")
	}
	return c, nil
}

func splitFields(s string) []string {
	out := []string{}
	for _, f := range strings.Fields(s) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

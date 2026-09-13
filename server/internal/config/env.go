package config

import (
	"os"
	"strconv"
	"strings"
)

// listEnv parses a comma-separated env var into *dst: entries are trimmed and
// lowercased, empties dropped. Keeps the current default when the variable is
// unset or empty.
func listEnv(key string, dst *[]string) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	var out []string
	for _, item := range strings.Split(v, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			out = append(out, item)
		}
	}
	*dst = out
}

// stringEnv assigns a non-empty env var to *dst, keeping the current default
// if the variable is unset. Keeps env wiring scannable instead of a wall of
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

// oneEnv sets *dst to true iff the env var is literal "1". Used for env vars
// that follow the daemon's numeric-flag convention instead of the "true" convention.
func oneEnv(key string, dst *bool) {
	if os.Getenv(key) == "1" {
		*dst = true
	}
}

// intEnv parses the env var as an integer and assigns it to *dst. Keeps the
// current default if the variable is unset or not a valid integer.
func intEnv(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

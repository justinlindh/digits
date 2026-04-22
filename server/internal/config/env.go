package config

import "os"

// StringEnv assigns a non-empty env var to *dst, keeping the current default
// if the variable is unset. Keeps env wiring scannable instead of a wall of
// five-line if blocks.
func StringEnv(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

// BoolEnv sets *dst to true iff the env var is literal "true". Matches the
// existing convention (a stricter-than-strconv.ParseBool check that rejects
// "1", "yes", etc.).
func BoolEnv(key string, dst *bool) {
	if os.Getenv(key) == "true" {
		*dst = true
	}
}

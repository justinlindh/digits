package main

import "testing"

func TestEnvOr(t *testing.T) {
	t.Run("env set", func(t *testing.T) {
		t.Setenv("DEVSEED_TEST_KEY", "from-env")
		got := envOr("DEVSEED_TEST_KEY", "fallback")
		if got != "from-env" {
			t.Fatalf("envOr with set var: got %q, want %q", got, "from-env")
		}
	})

	t.Run("env unset", func(t *testing.T) {
		// Setenv with empty string then delete via Unsetenv would also work,
		// but t.Setenv("", "") does not exist; use a var that is not set.
		t.Setenv("DEVSEED_TEST_KEY_UNSET", "")
		got := envOr("DEVSEED_TEST_KEY_UNSET", "fallback")
		if got != "fallback" {
			t.Fatalf("envOr with empty var: got %q, want %q", got, "fallback")
		}
	})

	t.Run("env missing", func(t *testing.T) {
		got := envOr("DEVSEED_DEFINITELY_NOT_SET_XYZ", "fallback")
		if got != "fallback" {
			t.Fatalf("envOr with missing var: got %q, want %q", got, "fallback")
		}
	})
}

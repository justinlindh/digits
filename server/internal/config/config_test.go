package config

import (
	"testing"
)

func TestStringEnv(t *testing.T) {
	t.Run("sets value when env var is present", func(t *testing.T) {
		t.Setenv("TEST_STRING_VAR", "hello")
		got := "default"
		StringEnv("TEST_STRING_VAR", &got)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("keeps default when env var is unset", func(t *testing.T) {
		got := "default"
		StringEnv("TEST_UNSET_VAR_XYZ", &got)
		if got != "default" {
			t.Errorf("got %q, want %q", got, "default")
		}
	})

	t.Run("keeps default when env var is empty string", func(t *testing.T) {
		t.Setenv("TEST_EMPTY_VAR", "")
		got := "default"
		StringEnv("TEST_EMPTY_VAR", &got)
		if got != "default" {
			t.Errorf("got %q, want %q", got, "default")
		}
	})
}

func TestBoolEnv(t *testing.T) {
	t.Run("sets true when env var is literal true", func(t *testing.T) {
		t.Setenv("TEST_BOOL_VAR", "true")
		got := false
		BoolEnv("TEST_BOOL_VAR", &got)
		if !got {
			t.Error("expected true, got false")
		}
	})

	t.Run("does not set true for 1", func(t *testing.T) {
		t.Setenv("TEST_BOOL_ONE", "1")
		got := false
		BoolEnv("TEST_BOOL_ONE", &got)
		if got {
			t.Error("expected false for value '1', got true")
		}
	})

	t.Run("does not set true for yes", func(t *testing.T) {
		t.Setenv("TEST_BOOL_YES", "yes")
		got := false
		BoolEnv("TEST_BOOL_YES", &got)
		if got {
			t.Error("expected false for value 'yes', got true")
		}
	})

	t.Run("keeps false when env var is unset", func(t *testing.T) {
		got := false
		BoolEnv("TEST_BOOL_UNSET_XYZ", &got)
		if got {
			t.Error("expected false for unset var, got true")
		}
	})

	t.Run("does not reset an already-true value to false", func(t *testing.T) {
		t.Setenv("TEST_BOOL_NOTRUE", "false")
		got := true
		BoolEnv("TEST_BOOL_NOTRUE", &got)
		if !got {
			t.Error("expected true to be unchanged when env is 'false'")
		}
	})
}

func TestOneEnv(t *testing.T) {
	t.Run("sets true when env var is literal 1", func(t *testing.T) {
		t.Setenv("TEST_ONE_VAR", "1")
		got := false
		OneEnv("TEST_ONE_VAR", &got)
		if !got {
			t.Error("expected true, got false")
		}
	})

	t.Run("does not set true for true", func(t *testing.T) {
		t.Setenv("TEST_ONE_TRUE", "true")
		got := false
		OneEnv("TEST_ONE_TRUE", &got)
		if got {
			t.Error("expected false for value 'true', got true")
		}
	})

	t.Run("keeps false when env var is unset", func(t *testing.T) {
		got := false
		OneEnv("TEST_ONE_UNSET_XYZ", &got)
		if got {
			t.Error("expected false for unset var, got true")
		}
	})
}

func TestLoadDefaults(t *testing.T) {
	c := Load()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Addr", c.Addr, ":8443"},
		{"MetricsAddr", c.MetricsAddr, ":9091"},
		{"SMTPPort", c.SMTPPort, "587"},
		{"SMTPFrom", c.SMTPFrom, "noreply@digits.family"},
		{"BaseURL", c.BaseURL, "https://app.digits.family"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

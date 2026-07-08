package config

import (
	"testing"
)

func TestStringEnv(t *testing.T) {
	t.Run("sets value when env var is present", func(t *testing.T) {
		t.Setenv("TEST_STRING_VAR", "hello")
		got := "default"
		stringEnv("TEST_STRING_VAR", &got)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("keeps default when env var is unset", func(t *testing.T) {
		got := "default"
		stringEnv("TEST_UNSET_VAR_XYZ", &got)
		if got != "default" {
			t.Errorf("got %q, want %q", got, "default")
		}
	})

	t.Run("keeps default when env var is empty string", func(t *testing.T) {
		t.Setenv("TEST_EMPTY_VAR", "")
		got := "default"
		stringEnv("TEST_EMPTY_VAR", &got)
		if got != "default" {
			t.Errorf("got %q, want %q", got, "default")
		}
	})
}

func TestBoolEnv(t *testing.T) {
	t.Run("sets true when env var is literal true", func(t *testing.T) {
		t.Setenv("TEST_BOOL_VAR", "true")
		got := false
		boolEnv("TEST_BOOL_VAR", &got)
		if !got {
			t.Error("expected true, got false")
		}
	})

	t.Run("does not set true for 1", func(t *testing.T) {
		t.Setenv("TEST_BOOL_ONE", "1")
		got := false
		boolEnv("TEST_BOOL_ONE", &got)
		if got {
			t.Error("expected false for value '1', got true")
		}
	})

	t.Run("does not set true for yes", func(t *testing.T) {
		t.Setenv("TEST_BOOL_YES", "yes")
		got := false
		boolEnv("TEST_BOOL_YES", &got)
		if got {
			t.Error("expected false for value 'yes', got true")
		}
	})

	t.Run("keeps false when env var is unset", func(t *testing.T) {
		got := false
		boolEnv("TEST_BOOL_UNSET_XYZ", &got)
		if got {
			t.Error("expected false for unset var, got true")
		}
	})

	t.Run("does not reset an already-true value to false", func(t *testing.T) {
		t.Setenv("TEST_BOOL_NOTRUE", "false")
		got := true
		boolEnv("TEST_BOOL_NOTRUE", &got)
		if !got {
			t.Error("expected true to be unchanged when env is 'false'")
		}
	})
}

func TestOneEnv(t *testing.T) {
	t.Run("sets true when env var is literal 1", func(t *testing.T) {
		t.Setenv("TEST_ONE_VAR", "1")
		got := false
		oneEnv("TEST_ONE_VAR", &got)
		if !got {
			t.Error("expected true, got false")
		}
	})

	t.Run("does not set true for true", func(t *testing.T) {
		t.Setenv("TEST_ONE_TRUE", "true")
		got := false
		oneEnv("TEST_ONE_TRUE", &got)
		if got {
			t.Error("expected false for value 'true', got true")
		}
	})

	t.Run("keeps false when env var is unset", func(t *testing.T) {
		got := false
		oneEnv("TEST_ONE_UNSET_XYZ", &got)
		if got {
			t.Error("expected false for unset var, got true")
		}
	})
}

func TestIntEnv(t *testing.T) {
	t.Run("sets value when env var is a valid integer", func(t *testing.T) {
		t.Setenv("TEST_INT_VAR", "42")
		got := 1
		intEnv("TEST_INT_VAR", &got)
		if got != 42 {
			t.Errorf("got %d, want 42", got)
		}
	})

	t.Run("keeps default when env var is not a valid integer", func(t *testing.T) {
		t.Setenv("TEST_INT_BAD", "not-a-number")
		got := 7
		intEnv("TEST_INT_BAD", &got)
		if got != 7 {
			t.Errorf("expected default 7 to be unchanged, got %d", got)
		}
	})

	t.Run("keeps default when env var is unset", func(t *testing.T) {
		got := 7
		intEnv("TEST_INT_UNSET_XYZ", &got)
		if got != 7 {
			t.Errorf("expected default 7 for unset var, got %d", got)
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

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("SIGNALD_ADDR", ":9000")
	t.Setenv("DATABASE_URL", "postgres://localhost/testdb")
	t.Setenv("SIGNALD_TURN_ENABLED", "true")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("SIGNALD_WS_RATE_LIMIT", "60")
	t.Setenv("DEV_MODE", "true")

	c := Load()

	if c.Addr != ":9000" {
		t.Errorf("Addr: got %q, want %q", c.Addr, ":9000")
	}
	if c.DatabaseURL != "postgres://localhost/testdb" {
		t.Errorf("DatabaseURL: got %q, want %q", c.DatabaseURL, "postgres://localhost/testdb")
	}
	if !c.TURNEnabled {
		t.Error("TURNEnabled: expected true")
	}
	if c.RedisURL != "redis://localhost:6379" {
		t.Errorf("RedisURL: got %q, want %q", c.RedisURL, "redis://localhost:6379")
	}
	if c.WSRateLimitPerMin != 60 {
		t.Errorf("WSRateLimitPerMin: got %d, want 60", c.WSRateLimitPerMin)
	}
	if !c.DevMode {
		t.Error("DevMode: expected true")
	}
}

package logging

import (
	"log/slog"
	"testing"
)

// TestSetupSelectsJSONHandler asserts LOG_FORMAT=json wires a JSON inner
// handler, wrapped in the trace-context decorator that every code path relies
// on for Loki<->Tempo correlation.
func TestSetupSelectsJSONHandler(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	Setup()

	tc, ok := slog.Default().Handler().(*traceContextHandler)
	if !ok {
		t.Fatalf("default handler = %T, want *traceContextHandler", slog.Default().Handler())
	}
	if _, ok := tc.inner.(*slog.JSONHandler); !ok {
		t.Errorf("inner handler = %T, want *slog.JSONHandler", tc.inner)
	}
}

// TestSetupDefaultsToTintHandler asserts that without LOG_FORMAT=json the
// inner handler is the colorized tint handler (i.e. not the JSON one), still
// wrapped in the trace-context decorator.
func TestSetupDefaultsToTintHandler(t *testing.T) {
	t.Setenv("LOG_FORMAT", "")
	Setup()

	tc, ok := slog.Default().Handler().(*traceContextHandler)
	if !ok {
		t.Fatalf("default handler = %T, want *traceContextHandler", slog.Default().Handler())
	}
	if _, ok := tc.inner.(*slog.JSONHandler); ok {
		t.Error("inner handler is JSON, want tint handler for non-json LOG_FORMAT")
	}
}

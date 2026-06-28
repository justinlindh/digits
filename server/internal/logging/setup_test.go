package logging

import (
	"log/slog"
	"testing"
)

// defaultInner returns the handler Setup wrapped inside the trace-context
// decorator on the default logger, failing the test if Setup did not install
// that decorator.
func defaultInner(t *testing.T) slog.Handler {
	t.Helper()
	tc, ok := slog.Default().Handler().(*traceContextHandler)
	if !ok {
		t.Fatalf("default handler = %T, want *traceContextHandler", slog.Default().Handler())
	}
	return tc.inner
}

// TestSetupSelectsJSONHandler asserts LOG_FORMAT=json wires a JSON inner
// handler, wrapped in the trace-context decorator that every code path relies
// on for Loki<->Tempo correlation.
func TestSetupSelectsJSONHandler(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	Setup()

	if inner := defaultInner(t); !isJSONHandler(inner) {
		t.Errorf("inner handler = %T, want *slog.JSONHandler", inner)
	}
}

// TestSetupDefaultsToTintHandler asserts that without LOG_FORMAT=json the
// inner handler is the colorized tint handler (i.e. not the JSON one), still
// wrapped in the trace-context decorator.
func TestSetupDefaultsToTintHandler(t *testing.T) {
	t.Setenv("LOG_FORMAT", "")
	Setup()

	if inner := defaultInner(t); isJSONHandler(inner) {
		t.Error("inner handler is JSON, want tint handler for non-json LOG_FORMAT")
	}
}

func isJSONHandler(h slog.Handler) bool {
	_, ok := h.(*slog.JSONHandler)
	return ok
}

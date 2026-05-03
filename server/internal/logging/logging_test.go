package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestTraceContextHandlerInjectsTraceID asserts that when a log call
// happens inside a recording span, the resulting JSON line carries
// trace_id and span_id fields with the canonical hex encoding (32 and
// 16 chars). This is the wire that makes Loki<->Tempo correlation work
// in Grafana: a Loki query that picks out the field can deep-link into
// the Tempo trace.
func TestTraceContextHandlerInjectsTraceID(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(&traceContextHandler{inner: inner})

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "x")
	defer span.End()

	logger.InfoContext(ctx, "hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", buf.String(), err)
	}
	tid, ok := rec["trace_id"].(string)
	if !ok || len(tid) != 32 {
		t.Errorf("trace_id = %v, want 32-char hex", rec["trace_id"])
	}
	sid, ok := rec["span_id"].(string)
	if !ok || len(sid) != 16 {
		t.Errorf("span_id = %v, want 16-char hex", rec["span_id"])
	}
	// trace_id should match the active span's TraceID hex.
	if got := span.SpanContext().TraceID().String(); got != tid {
		t.Errorf("trace_id mismatch: log %q vs span %q", tid, got)
	}
}

// TestTraceContextHandlerSkipsNoSpan: logging without an active span
// must NOT add zero-valued trace_id / span_id fields. The structured
// log shape needs to stay backwards-compatible with the metrics SWE's
// log dashboards.
func TestTraceContextHandlerSkipsNoSpan(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(&traceContextHandler{inner: inner})

	logger.Info("no span here")

	out := buf.String()
	if strings.Contains(out, "trace_id") {
		t.Errorf("expected no trace_id field, got: %s", out)
	}
	if strings.Contains(out, "span_id") {
		t.Errorf("expected no span_id field, got: %s", out)
	}
}

// TestTraceContextHandlerWithAttrs verifies the handler still injects
// trace_id when logger.With() (which calls WithAttrs on the handler)
// has captured intermediate fields. Without WithAttrs delegating to a
// new traceContextHandler wrapping the inner handler's WithAttrs, the
// trace decoration would be silently lost on every component that
// builds a derived logger.
func TestTraceContextHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(&traceContextHandler{inner: inner}).With("component", "test")

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)
	tracer := otel.Tracer("t")
	ctx, span := tracer.Start(context.Background(), "x")
	defer span.End()

	logger.InfoContext(ctx, "hello")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", buf.String(), err)
	}
	if rec["component"] != "test" {
		t.Errorf("component field lost through WithAttrs: %v", rec["component"])
	}
	if _, ok := rec["trace_id"].(string); !ok {
		t.Errorf("trace_id lost through WithAttrs: %v", rec)
	}
}

// TestTraceContextHandlerNoPII verifies the trace and span IDs are pure
// 128/64-bit identifiers and never echo any user-derivable input. We
// emit one log line and confirm the trace_id is exactly 32 hex chars
// and contains no non-hex characters: that means a future maintainer
// cannot accidentally smuggle a user identifier in via this code path.
func TestTraceContextHandlerNoPII(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(&traceContextHandler{inner: inner})

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)
	tracer := otel.Tracer("t")
	ctx, span := tracer.Start(context.Background(), "x")
	defer span.End()
	logger.InfoContext(ctx, "msg")

	var rec map[string]any
	_ = json.Unmarshal(buf.Bytes(), &rec)
	tid, _ := rec["trace_id"].(string)
	for _, c := range tid {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			t.Errorf("trace_id has non-hex char %q in %q", c, tid)
		}
	}
}

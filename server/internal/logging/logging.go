// Package logging configures the process-wide structured logger (slog) and
// injects active OpenTelemetry trace and span IDs into every log record so
// log lines can be correlated with traces in the observability backend.
package logging

import (
	"context"
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"go.opentelemetry.io/otel/trace"
)

// Setup configures the default slog logger.
// LOG_FORMAT=json uses JSON output for production; otherwise uses colorized tint.
//
// All log records are wrapped in a traceContextHandler that, when the
// surrounding handler ran with a recording span in context, appends
// trace_id and span_id fields to the record. This is the bridge that
// makes Loki<->Tempo correlation work in Grafana: a JSON log line
// carrying a trace_id is link-annotated to the Tempo trace by Loki's
// Grafana datasource. The fields are appended, never replacing existing
// fields, so the structured-log shape is unchanged on requests that
// run outside a traced context.
//
// Privacy: trace_id and span_id are random 128- and 64-bit identifiers
// respectively. They do not encode user identity. The trace they refer
// to in Tempo carries only the privacy-respecting attributes documented
// in internal/tracing/tracing.go.
func Setup() {
	var inner slog.Handler
	if os.Getenv("LOG_FORMAT") == "json" {
		inner = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		inner = tint.NewTextHandler(os.Stderr, &tint.Options{
			Level:      slog.LevelInfo,
			TimeFormat: "15:04:05",
		})
	}
	slog.SetDefault(slog.New(&traceContextHandler{inner: inner}))
}

// traceContextHandler is a slog.Handler that decorates log records with
// trace_id and span_id when the supplied context carries a recording
// OpenTelemetry span. All other handler operations delegate to the inner
// handler unchanged.
//
// We implement Handler from scratch (rather than wrapping with
// slog.Handler.WithAttrs etc.) so the trace fields are evaluated lazily
// per-record from the live context, not at logger construction time.
// That means a logger captured at server startup still produces correct
// trace_id values for requests that arrive minutes later, because each
// Handle call reads SpanFromContext(ctx).
type traceContextHandler struct {
	inner slog.Handler
}

// Enabled mirrors the inner handler so log levels work as configured.
func (h *traceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle adds trace_id and span_id to the record when present, then
// delegates to the inner handler. The added fields use the OpenTelemetry
// canonical hex encoding (32 chars for trace, 16 for span) so they line
// up with the values Tempo and Loki use for correlation queries.
func (h *traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	// Only add the fields when a real, non-zero span context is present.
	// SpanFromContext returns a noop span (never nil) for callers that log
	// outside a traced flow; that span has an all-zero context, and adding 32
	// zeros to every log line would be noise.
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs and WithGroup delegate so loggers built via slog.With() or
// logger.WithGroup() preserve the trace decoration through the chain.
func (h *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{inner: h.inner.WithGroup(name)}
}

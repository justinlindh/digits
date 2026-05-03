// Package tracing wires OpenTelemetry tracing for signald and admind.
// The design is bound by the same anti-surveillance policy that governs
// internal/metrics: every span attribute, every event, every link is a
// privacy decision, not a routine instrumentation toggle. See
// docs/mission.md, docs/why-digits.md, and server/docs/tracing.md for the
// product-level rationale.
//
// What is collected (aggregate / structural only):
//
//   - HTTP server spans: method, status code, duration, and a coarse route
//     bucket from internal/metrics.RouteOf. Path components carrying user
//     identifiers (phone numbers, call IDs, magic-link tokens) collapse to
//     template strings; unknown paths collapse to "other". The default
//     otelhttp span name (the raw URL.Path) is replaced by the bucketed
//     route so a span name itself never echoes user data.
//   - HTTP client spans: same approach for outbound requests; request URL
//     is reduced to host plus a static path label set by the caller.
//   - Database client spans: query category (SELECT, INSERT, UPDATE, etc.)
//     plus a static table name when the driver-supplied operation lookup
//     can attribute one. SQL text and bound parameters never reach a span
//     attribute (otelsql is configured with all OmitSQL* options enabled).
//   - Process / runtime resource attributes: service.name, service.version,
//     service.instance.id (the pod / container hostname), deployment
//     environment, plus the static cluster=homelab label.
//   - Span events for signaling errors using the closed-set category
//     identifiers from internal/metrics.SignalingErrorCategory.
//
// What is NEVER collected (do not add attributes for these):
//
//   - Per-user identity: user_id, email, household_id, line/number, etc.
//   - Call identifiers that map back to participants, conference IDs that
//     could correlate with member phone numbers, magic-link tokens.
//   - Authentication material: session cookies, OAuth tokens, password
//     hashes, magic-link tokens.
//   - IP addresses (even hashed), geographic data, or anything derivable
//     from same.
//   - Free-form user content (call SDP fragments, audio, message bodies).
//   - Database query parameters that could echo any of the above.
//
// The Init function is safe to call from cmd/signald and cmd/admind
// regardless of whether OTEL_EXPORTER_OTLP_ENDPOINT is configured: with
// no endpoint, Init returns a no-op shutdown closure and tracing remains
// off. The signald and admind binaries must call shutdown on graceful
// exit so in-flight spans get flushed.
package tracing

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/justinlindh/digits/server/internal/metrics"
)

// Config holds the runtime configuration for tracing initialization.
// Endpoint is read from the standard OTEL_EXPORTER_OTLP_ENDPOINT env var
// in NewConfig; an empty Endpoint disables the exporter (Init returns a
// no-op shutdown). Protocol picks between gRPC (default) and HTTP/protobuf.
type Config struct {
	// ServiceName is the short identifier for this binary, e.g. "signald"
	// or "admind". Becomes the service.name resource attribute.
	ServiceName string
	// ServiceVersion is the build-time version string from internal/version.
	// Becomes the service.version resource attribute.
	ServiceVersion string
	// ServiceCommit is the short git commit. Joined with the version into
	// a service.version-style identifier. Useful for distinguishing two
	// builds carrying the same tag (e.g. dev vs CI artifact).
	ServiceCommit string
	// Endpoint is the OTLP collector endpoint. For gRPC this is host:port;
	// for HTTP it is host:port (no scheme, no path). Empty disables the
	// exporter entirely.
	Endpoint string
	// Protocol selects the OTLP transport. "grpc" (default) or "http".
	// Anything else falls back to gRPC; a typo cannot turn the exporter
	// into a no-op silently.
	Protocol string
	// Insecure controls whether to skip TLS for the OTLP transport. The
	// homelab collector is a ClusterIP service on the cluster network, so
	// this is true by default for that deployment. Production-over-internet
	// deployments must override (set OTEL_EXPORTER_OTLP_INSECURE=false).
	Insecure bool
	// SampleRatio is the head-based sampling ratio (0..1). 1.0 (default)
	// records every span; for high-traffic deployments operators can dial
	// it down via OTEL_TRACES_SAMPLER_ARG.
	SampleRatio float64
	// ResourceAttrs are extra resource attributes parsed from
	// OTEL_RESOURCE_ATTRIBUTES (key=value, comma-separated). The k8s
	// deployment uses this to set cluster=homelab and service.namespace.
	ResourceAttrs []attribute.KeyValue
}

// NewConfig builds a Config from env vars. Reads:
//
//   - OTEL_EXPORTER_OTLP_ENDPOINT (required to enable; empty -> tracing off)
//   - OTEL_EXPORTER_OTLP_PROTOCOL (grpc | http; default grpc)
//   - OTEL_EXPORTER_OTLP_INSECURE (true | false; default true for cluster)
//   - OTEL_TRACES_SAMPLER_ARG (float in 0..1; default 1.0)
//   - OTEL_RESOURCE_ATTRIBUTES (key=value,key=value)
//
// Service name and version come from the caller (Init's argv) so an
// operator cannot swap a binary's identity via env. The standard
// OTEL_SERVICE_NAME env var is honored as a fallback.
func NewConfig(serviceName, serviceVersion, serviceCommit string) Config {
	c := Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		ServiceCommit:  serviceCommit,
		Endpoint:       os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		Protocol:       envOr("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc"),
		Insecure:       os.Getenv("OTEL_EXPORTER_OTLP_INSECURE") != "false",
		SampleRatio:    1.0,
	}
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" && c.ServiceName == "" {
		c.ServiceName = v
	}
	if v := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil && f >= 0 && f <= 1 {
			c.SampleRatio = f
		}
	}
	if v := os.Getenv("OTEL_RESOURCE_ATTRIBUTES"); v != "" {
		c.ResourceAttrs = parseResourceAttrs(v)
	}
	return c
}

// envOr returns the env var value if set, else the fallback. Local helper
// so we don't import internal/config (circular: config imports nothing
// product-specific, and tracing should not pull from config to keep this
// package independently testable).
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseResourceAttrs accepts the OTEL_RESOURCE_ATTRIBUTES wire format
// "k1=v1,k2=v2,..." and returns the parsed attributes. Malformed entries
// (no '=') are skipped so a typo in a deployment manifest doesn't crash
// the pod on startup.
func parseResourceAttrs(s string) []attribute.KeyValue {
	parts := strings.Split(s, ",")
	out := make([]attribute.KeyValue, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		eq := strings.IndexByte(p, '=')
		if eq <= 0 || eq == len(p)-1 {
			continue
		}
		out = append(out, attribute.String(p[:eq], p[eq+1:]))
	}
	return out
}

// Shutdown is a closure returned by Init that flushes any buffered spans
// and closes the exporter. Always call this on graceful exit; signald and
// admind do so via defer in their main goroutines. A nil return value
// indicates Init was a no-op.
type Shutdown func(context.Context) error

// noop is the Shutdown returned when tracing is disabled. Calling it is
// a successful no-op so callers can defer it unconditionally.
func noop(context.Context) error { return nil }

// Init configures the OpenTelemetry global tracer provider with an OTLP
// exporter pointed at cfg.Endpoint. When Endpoint is empty, Init returns
// a no-op shutdown and leaves the global tracer at the default no-op
// provider. The W3C Trace Context propagator is installed unconditionally
// (cheap, and lets in-process spans link across components even without
// an exporter).
//
// Resource attributes are derived from cfg plus the standard semconv
// fields (service.name, service.version, service.instance.id from
// hostname, host.name). The cluster label is left for the operator to
// supply via OTEL_RESOURCE_ATTRIBUTES rather than hardcoded here, so the
// docker prod deployment and the k8s deployment can carry different
// values without a code change.
//
// CAUTION: Init must be called exactly once per process. Re-calling
// installs a second tracer provider; the first one's exporter is leaked.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	// Always install the propagator: even when no exporter is configured
	// (cfg.Endpoint empty), we want incoming traceparent headers to
	// propagate through the process so an upstream collector that injects
	// them sees a continuous trace. The cost is a few atomic loads per
	// request; the safety upside is consistent behavior across deployments.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.Endpoint == "" {
		return noop, nil
	}

	exp, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}

	res, err := buildResource(ctx, cfg)
	if err != nil {
		// Resource detection failures are non-fatal: fall back to a minimal
		// resource carrying the explicit cfg fields. We never want a bad
		// hostname lookup to take signald offline at boot.
		res = fallbackResource(cfg)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(
			sdktrace.TraceIDRatioBased(cfg.SampleRatio),
		)),
	)
	otel.SetTracerProvider(tp)

	return func(shutdownCtx context.Context) error {
		// Flush before close so a clean SIGTERM doesn't drop in-flight spans.
		ferr := tp.ForceFlush(shutdownCtx)
		serr := tp.Shutdown(shutdownCtx)
		return errors.Join(ferr, serr)
	}, nil
}

// newExporter builds the OTLP trace exporter for the configured protocol.
// gRPC is the default (lower per-span overhead, multiplexed connection,
// no per-span TCP setup); HTTP/protobuf is supported for environments
// where gRPC is filtered.
func newExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	switch strings.ToLower(cfg.Protocol) {
	case "http", "http/protobuf":
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
	default:
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		return otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
	}
}

// buildResource assembles the OTel resource (the static attributes
// applied to every span emitted by this process). The order matters:
// caller-supplied resource attrs go first so an explicit
// OTEL_RESOURCE_ATTRIBUTES entry can override a detected default (e.g.
// pinning service.namespace=digits even if the SDK picked something else).
func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	host, _ := os.Hostname() // nolint:errcheck // empty hostname is fine
	base := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		semconv.ServiceInstanceID(host),
		semconv.HostName(host),
	}
	if cfg.ServiceCommit != "" {
		base = append(base, attribute.String("service.commit", cfg.ServiceCommit))
	}
	base = append(base, cfg.ResourceAttrs...)
	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithProcessRuntimeDescription(),
		resource.WithAttributes(base...),
	)
}

// fallbackResource is used when resource detection fails. Carries the
// minimum to identify a service in a multi-tenant collector.
func fallbackResource(cfg Config) *resource.Resource {
	host, _ := os.Hostname() // nolint:errcheck
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
		semconv.ServiceInstanceID(host),
	)
}

// HTTPServerMiddleware returns a server-side HTTP middleware that
// produces one span per inbound request. The span name is bucketed
// through metrics.RouteOf so a phone number, call ID, magic-link token,
// or any other URL-segment user identifier never reaches the trace UI.
//
// We deliberately do NOT use otelhttp.NewHandler here: that wrapper
// records url.path, url.query, and url.full as raw strings, which would
// echo every PII-bearing path component into a Tempo trace attribute
// even with our route override. This middleware records only:
//
//   - http.route: the bucketed route (e.g. "/phones/{number}")
//   - http.method, http.status_code: standard semconv fields with no PII
//   - server.address: the request Host header (in practice the
//     configured domain behind our reverse proxy; technically
//     client-controlled but cannot leak PII)
//
// Inbound traceparent headers are honored via the global propagator
// (installed by Init), so a request from admind carrying a traceparent
// becomes a child of admind's span. The span name format is
// "<service>.http <route-bucket>" to match the metrics labelset shape.
//
// The serviceName argument is used as the operation prefix (e.g.
// "signald.http"); a typo there is a code-only change, not an env knob.
func HTTPServerMiddleware(serviceName string, next http.Handler) http.Handler {
	tracer := otel.Tracer("github.com/justinlindh/digits/server/internal/tracing")
	propagator := otel.GetTextMapPropagator()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drop noise routes before any tracer cost is paid. /metrics,
		// /healthz, and /static/* are high-frequency, low-information
		// requests that would dominate the exporter's payload.
		if isNoiseRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Lift any inbound traceparent header into the request context
		// so the new span becomes a child of the upstream span. When
		// no header is present, this is a no-op and a fresh trace ID
		// is generated below.
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		route := metrics.RouteOf(r.URL.Path)
		spanName := serviceName + ".http " + route
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.route", route),
				attribute.String("http.method", r.Method),
				attribute.String("server.address", r.Host),
			),
		)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.status_code", rec.status))
		if rec.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}
	})
}

// isNoiseRoute returns true for paths we never want to record a span
// for. Kept as a tiny helper so the filter list is one line per entry
// and easy to audit.
func isNoiseRoute(path string) bool {
	switch {
	case path == "/metrics",
		path == "/healthz",
		strings.HasPrefix(path, "/static/"):
		return true
	}
	return false
}

// statusRecorder captures the response status without buffering the body.
// Mirrors the metrics package's recorder; copied (not imported) so the
// tracing middleware does not depend on metrics' internals beyond the
// public RouteOf bucketer.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush and Hijack pass-through preserve SSE / WebSocket upgrade
// behavior; without them, /api/dashboard/stream and /ws would break.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// HTTPClientTransport wraps base with a tracing transport that injects
// W3C traceparent on outbound requests and produces one client span per
// request. The span carries:
//
//   - http.method
//   - http.status_code (set after the request returns)
//   - server.address: the destination host (already a configured
//     service hostname for our internal calls; never a user-derived
//     value because admind only calls signald's /internal/stats)
//
// We deliberately do NOT use otelhttp.NewTransport. That wrapper records
// url.full and url.path as raw strings, which for any per-call outbound
// (today: none; tomorrow: who knows) would echo path components into a
// span attribute. The strip-and-bucket pattern is enforced at the
// transport rather than every call site so a future caller cannot
// regress the privacy boundary by forgetting an option.
func HTTPClientTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &tracingTransport{
		base:   base,
		tracer: otel.Tracer("github.com/justinlindh/digits/server/internal/tracing"),
	}
}

// tracingTransport implements http.RoundTripper. It is intentionally
// minimal; the OTel SDK does the heavy lifting via tracer.Start and the
// global propagator.
type tracingTransport struct {
	base   http.RoundTripper
	tracer trace.Tracer
}

// RoundTrip starts a client span, injects traceparent into the request
// headers, dispatches via the wrapped transport, and records the status.
func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	propagator := otel.GetTextMapPropagator()

	ctx, span := t.tracer.Start(req.Context(), "HTTP "+req.Method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.method", req.Method),
			attribute.String("server.address", req.URL.Host),
		),
	)
	defer span.End()

	// Clone the request so traceparent header injection does not mutate
	// a caller-owned *http.Request. Headers cloned by req.Clone are deep
	// copies, so adding traceparent here doesn't leak into a retry's
	// header set.
	req = req.Clone(ctx)
	propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	return resp, nil
}

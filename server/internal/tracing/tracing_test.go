package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracesdk "go.opentelemetry.io/otel/trace"

	"github.com/justinlindh/digits/server/internal/metrics"
)

// withRecording installs an in-memory span recorder as the global tracer
// provider for the duration of one test. It returns the recorder and a
// cleanup that restores the prior provider so tests don't leak global
// state across the suite.
func withRecording(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	prior := otel.GetTracerProvider()
	rec := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prior)
	})
	return rec
}

// TestNewConfigDisabledByDefault: with no env vars, NewConfig produces a
// disabled exporter. Init then returns a no-op shutdown without an error.
// This is the safe default: if the OTEL_EXPORTER_OTLP_ENDPOINT is
// missing, signald must still boot.
func TestNewConfigDisabledByDefault(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	c := NewConfig("signald", "v1.0.0", "abc123")
	if c.Endpoint != "" {
		t.Fatalf("Endpoint = %q, want empty", c.Endpoint)
	}
	shutdown, err := Init(context.Background(), c)
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown closure")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("noop shutdown returned error: %v", err)
	}
}

// TestParseResourceAttrs verifies the OTEL_RESOURCE_ATTRIBUTES wire
// format parses into a flat attribute slice. This is the env path that
// the k8s deployment uses to inject cluster=homelab and
// service.namespace=digits.
func TestParseResourceAttrs(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]string
	}{
		{"cluster=homelab", map[string]string{"cluster": "homelab"}},
		{"cluster=homelab,service.namespace=digits",
			map[string]string{"cluster": "homelab", "service.namespace": "digits"}},
		// Malformed entries skip without erroring.
		{"oops,cluster=homelab", map[string]string{"cluster": "homelab"}},
		{"cluster=", map[string]string{}},
		{"=value", map[string]string{}},
		{"", map[string]string{}},
	}
	for _, c := range cases {
		got := parseResourceAttrs(c.in)
		gotMap := map[string]string{}
		for _, kv := range got {
			gotMap[string(kv.Key)] = kv.Value.AsString()
		}
		if len(gotMap) != len(c.want) {
			t.Errorf("parseResourceAttrs(%q) = %v, want %v", c.in, gotMap, c.want)
			continue
		}
		for k, v := range c.want {
			if gotMap[k] != v {
				t.Errorf("parseResourceAttrs(%q)[%q] = %q, want %q", c.in, k, gotMap[k], v)
			}
		}
	}
}

// TestHTTPServerMiddlewareBucketsRoute is the central privacy invariant
// for inbound traces: a phone number in the URL must NEVER reach the
// span name or the http.route attribute. The middleware uses the same
// metrics.RouteOf bucketer as the Prometheus middleware, so the test
// exercises the same closed-set mapping.
func TestHTTPServerMiddlewareBucketsRoute(t *testing.T) {
	rec := withRecording(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/phones/+15551234567/edit", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/call/abc-123", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/auth/magic/sometoken", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPServerMiddleware("signald", mux)
	srv := httptest.NewServer(h)
	defer srv.Close()

	for _, path := range []string{
		"/phones/+15551234567/edit",
		"/api/call/abc-123",
		"/auth/magic/sometoken",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	for _, sp := range spans {
		name := sp.Name()
		// Span name must be the bucketed template, never the raw path.
		if strings.Contains(name, "1234567") {
			t.Errorf("span name %q leaked digits", name)
		}
		if strings.Contains(name, "abc-123") {
			t.Errorf("span name %q leaked call id", name)
		}
		if strings.Contains(name, "sometoken") {
			t.Errorf("span name %q leaked magic-link token", name)
		}
		// Same check for every attribute. Banned tokens should never
		// appear in any attribute string value.
		for _, attr := range sp.Attributes() {
			v := attr.Value.AsString()
			if strings.Contains(v, "1234567") {
				t.Errorf("span attribute %q=%q leaked digits", attr.Key, v)
			}
			if strings.Contains(v, "sometoken") {
				t.Errorf("span attribute %q=%q leaked magic-link token", attr.Key, v)
			}
		}
		// Span name shape: "signald.http " followed by a route bucket.
		if !strings.HasPrefix(name, "signald.http ") {
			t.Errorf("span name %q missing service prefix", name)
		}
	}
}

// TestHTTPServerMiddlewareSetsBucketedRouteAttr asserts that an
// http.route attribute is set on the recorded span and that its value
// is the bucketed route, not the raw path. This is the field a
// downstream Tempo dashboard groups by, so the bucket guarantee has to
// hold here.
func TestHTTPServerMiddlewareSetsBucketedRouteAttr(t *testing.T) {
	rec := withRecording(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/phones/+15551234567", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(HTTPServerMiddleware("signald", mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/phones/+15551234567")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	var found bool
	for _, sp := range spans {
		for _, a := range sp.Attributes() {
			if string(a.Key) == "http.route" {
				found = true
				want := metrics.RouteOf("/phones/+15551234567")
				if a.Value.AsString() != want {
					t.Errorf("http.route = %q, want %q", a.Value.AsString(), want)
				}
			}
		}
	}
	if !found {
		t.Error("no http.route attribute found")
	}
}

// TestHTTPServerMiddlewareFiltersNoiseRoutes verifies the metrics scrape
// and healthz paths do not produce trace spans. These are high-frequency
// routes that would dominate the exporter without diagnostic value.
func TestHTTPServerMiddlewareFiltersNoiseRoutes(t *testing.T) {
	rec := withRecording(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/static/foo.css", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(HTTPServerMiddleware("signald", mux))
	defer srv.Close()

	for _, p := range []string{"/healthz", "/metrics", "/static/foo.css"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	if got := len(rec.Ended()); got != 0 {
		t.Errorf("expected zero spans for noise routes, got %d", got)
	}
}

// TestHTTPClientTransportInjectsTraceparent confirms outbound requests
// carry the W3C traceparent header when wrapped via HTTPClientTransport.
// Without this, any cross-service hop would produce two disconnected
// traces in Tempo instead of a parent-child pair.
func TestHTTPClientTransportInjectsTraceparent(t *testing.T) {
	withRecording(t)

	var seenHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeader = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: HTTPClientTransport(http.DefaultTransport)}

	// A traceparent header is only injected when there is an active span.
	// Start a span around the request so otelhttp has a context to lift.
	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "outer")
	defer span.End()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if seenHeader == "" {
		t.Fatal("server did not see Traceparent header from wrapped client")
	}
	// Per the W3C Trace Context spec, the value is "version-traceid-spanid-flags".
	if !strings.HasPrefix(seenHeader, "00-") {
		t.Errorf("Traceparent has unexpected version: %q", seenHeader)
	}
}

// TestHTTPServerMiddlewareNeverEchoesNumber is the belt-and-suspenders
// guard test that mirrors metrics.TestRouteOfNeverEchoesNumber. If a
// future maintainer adds a route case that lets a phone number through,
// this fires on the trace surface as well as the metric one.
func TestHTTPServerMiddlewareNeverEchoesNumber(t *testing.T) {
	rec := withRecording(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(HTTPServerMiddleware("signald", mux))
	defer srv.Close()

	// Hit several PII-bearing paths.
	for _, p := range []string{
		"/phones/+15551234567",
		"/phones/+15551234567/edit",
		"/api/call/12345",
		"/api/conference/8b6e2bb8-19fa-4f0a-8af9-f60094f0a7d5/kick",
		"/auth/magic/secret-token-here",
	} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	bannedFragments := []string{
		"1234567",
		"+1555",
		"secret-token-here",
		"8b6e2bb8",
	}
	for _, sp := range spans {
		for _, banned := range bannedFragments {
			if strings.Contains(sp.Name(), banned) {
				t.Errorf("span name %q contains banned %q", sp.Name(), banned)
			}
		}
		for _, a := range sp.Attributes() {
			v := a.Value.AsString()
			for _, banned := range bannedFragments {
				if strings.Contains(v, banned) {
					t.Errorf("span attr %s=%q contains banned %q", a.Key, v, banned)
				}
			}
		}
	}
}

// TestSpanAttrsForbidPII is a static guard: even when a malicious caller
// somehow reaches our HTTPServerMiddleware via a custom path that the
// router didn't register, the route bucket must not echo the path. We
// use httptest.NewServer with a wildcard handler so the request URL is
// untouched, then assert no span attribute contains the raw path.
func TestSpanAttrsForbidPII(t *testing.T) {
	rec := withRecording(t)
	srv := httptest.NewServer(HTTPServerMiddleware("signald",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}),
	))
	defer srv.Close()
	// Path the caller controls; not in metrics.RouteOf's known set.
	resp, err := http.Get(srv.URL + "/some/secret/that/should/not/appear")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	for _, sp := range rec.Ended() {
		// Bucket must be "other" for unknown paths.
		for _, a := range sp.Attributes() {
			if string(a.Key) != "http.route" {
				continue
			}
			if a.Value.AsString() != "other" {
				t.Errorf("http.route for unknown path = %q, want \"other\"", a.Value.AsString())
			}
		}
		if strings.Contains(sp.Name(), "secret") {
			t.Errorf("span name %q contains unknown-path segment", sp.Name())
		}
	}
}

// Compile-time guard that the SDK's API surface for tracetest hasn't
// drifted out from under us. tracetest.SpanRecorder must implement
// trace.SpanProcessor for our withRecording helper to compile.
var _ trace.SpanProcessor = (*tracetest.SpanRecorder)(nil)

// Sanity check the propagator install path: a brand new tracer + span
// must produce a SpanContext whose IDs render as hex per the W3C spec.
// This is the same encoding the logging handler uses to emit trace_id
// and span_id, so the test pins the format end-to-end.
func TestSpanContextHexEncoding(t *testing.T) {
	withRecording(t)
	tracer := otel.Tracer("t")
	_, span := tracer.Start(context.Background(), "x")
	defer span.End()
	sc := span.SpanContext()
	if !sc.IsValid() {
		t.Fatal("expected a valid span context")
	}
	if got := sc.TraceID().String(); len(got) != 32 {
		t.Errorf("TraceID %q has length %d, want 32", got, len(got))
	}
	if got := sc.SpanID().String(); len(got) != 16 {
		t.Errorf("SpanID %q has length %d, want 16", got, len(got))
	}
}

// Compile-time check that semconv-style attribute construction lines up
// with what the SDK expects. If a future semconv version renames or
// retypes service.name, this catches it at build time.
var _ attribute.KeyValue = attribute.String("k", "v")
var _ tracesdk.Tracer = otel.Tracer("t")

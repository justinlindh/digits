// Package metrics defines the privacy-respecting Prometheus metric set
// exported by signald. The design is deliberately conservative:
// every label and every measurement is reviewed against the digits
// anti-surveillance policy described in docs/mission.md and
// docs/why-digits.md. Reviewers should treat any new label here as a
// privacy decision, not a routine refactor.
//
// What is collected (aggregate only):
//
//   - HTTP request totals and latency, labeled by route group, method, and
//     status code. Route groups are coarse buckets (e.g. /phones, /api/status,
//     /ws) chosen to avoid path components that contain user identifiers.
//   - Active devices (a gauge of currently connected phones, count only).
//   - Active calls (a gauge of in-flight calls, count only).
//   - Signaling errors by category (e.g. turn_alloc_failed, ice_timeout).
//     The category is a fixed enum; no peer identity, number, or content.
//   - Build info as a static gauge labeled with the version and short commit.
//   - Go runtime and process collectors (goroutines, GC pauses, memory, fd
//     count) provided by promhttp / collectors. Same data the Go runtime
//     pprof endpoints already expose.
//
// What is NEVER collected (do not add labels for these):
//
//   - Per-user request counts, per-user latency, or any user identifier.
//   - Per-call duration, per-call participants, or routing details.
//   - Caller or callee phone numbers, or anything derivable from them.
//   - IP addresses (even hashed) or geographic data.
//   - Contact-graph or household membership data.
//   - Magic-link emails, OAuth identities, or session tokens.
//   - Free-form text from user content.
package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Service identifies which binary the metrics belong to. Used as a
// "service" label on shared metrics so a Prometheus scrape config
// can identify the service without renaming the metric family.
type Service string

const (
	ServiceSignald Service = "signald"
)

// SignalingErrorCategory is a closed set of categories for the
// signaling_errors_total counter. The list is intentionally small and
// product-defined: adding a new category is a code change, never a runtime
// label, so an attacker cannot smuggle PII into a label value.
type SignalingErrorCategory string

const (
	ErrTURNAllocFailed SignalingErrorCategory = "turn_alloc_failed"
	ErrICETimeout      SignalingErrorCategory = "ice_timeout"
	ErrPeerUnreachable SignalingErrorCategory = "peer_unreachable"
	ErrCallSetupFailed SignalingErrorCategory = "call_setup_failed"
	ErrAuthFailed      SignalingErrorCategory = "auth_failed"
	ErrInvalidMessage  SignalingErrorCategory = "invalid_message"
	ErrRelayDelivery   SignalingErrorCategory = "relay_delivery"
)

// Registry bundles a Prometheus registry, the metrics registered into it,
// and any GaugeFuncs that read live state. Keep one Registry per process.
type Registry struct {
	Reg *prometheus.Registry

	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	ActiveDevices   prometheus.Gauge
	ActiveCalls     prometheus.Gauge
	SignalingErrors *prometheus.CounterVec
	BuildInfo       *prometheus.GaugeVec
}

// New builds a Registry with all metrics registered. Callers wire live-state
// gauges (active devices, active calls) by calling RegisterDevicesGauge and
// RegisterCallsGauge with closures that read in-memory state. Counters are
// driven by middleware and signaling code.
func New(svc Service, version, commit string) *Registry {
	reg := prometheus.NewRegistry()

	// Standard process and Go runtime collectors. These provide goroutines,
	// GC, memstats, fd count, and process-level CPU/RSS without exposing any
	// per-request or per-user data.
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	r := &Registry{Reg: reg}

	r.HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "digits",
			Subsystem: string(svc),
			Name:      "http_requests_total",
			Help:      "Total HTTP requests handled, partitioned by coarse route group, method, and status code. Routes are bucketed to avoid path components that carry user identifiers.",
		},
		[]string{"route", "method", "status"},
	)
	r.HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "digits",
			Subsystem: string(svc),
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency in seconds, partitioned by coarse route group, method, and status code.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"route", "method", "status"},
	)
	r.ActiveDevices = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "digits",
		Subsystem: string(svc),
		Name:      "active_devices",
		Help:      "Currently connected phones. Count only; no identifiers.",
	})
	r.ActiveCalls = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "digits",
		Subsystem: string(svc),
		Name:      "active_calls",
		Help:      "Currently active calls. Count only; no participants or routing.",
	})
	r.SignalingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "digits",
			Subsystem: string(svc),
			Name:      "signaling_errors_total",
			Help:      "Signaling errors observed by the server, partitioned by a fixed category set. No peer identity is recorded.",
		},
		[]string{"category"},
	)
	r.BuildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "digits",
			Subsystem: string(svc),
			Name:      "build_info",
			Help:      "Static build info. Always 1; the version and commit are carried as labels.",
		},
		[]string{"version", "commit"},
	)
	r.BuildInfo.WithLabelValues(version, commit).Set(1)

	reg.MustRegister(
		r.HTTPRequestsTotal,
		r.HTTPRequestDuration,
		r.ActiveDevices,
		r.ActiveCalls,
		r.SignalingErrors,
		r.BuildInfo,
	)

	return r
}

// RegisterDevicesGauge installs a GaugeFunc that reads active-device count
// from the supplied closure on every scrape. Use a closure that returns
// len(hub.OnlineNumbers()) so we never pre-compute and never persist counts
// elsewhere.
func (r *Registry) RegisterDevicesGauge(read func() float64) {
	g := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "digits",
		Subsystem: "signald",
		Name:      "active_devices_current",
		Help:      "Current active device count, sampled at scrape time. Identical in meaning to active_devices but read live; the static gauge exists for tests that drive it directly.",
	}, read)
	r.Reg.MustRegister(g)
}

// RegisterCallsGauge mirrors RegisterDevicesGauge for active calls.
func (r *Registry) RegisterCallsGauge(read func() float64) {
	g := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "digits",
		Subsystem: "signald",
		Name:      "active_calls_current",
		Help:      "Current active call count, sampled at scrape time. Identical in meaning to active_calls but read live.",
	}, read)
	r.Reg.MustRegister(g)
}

// ObserveSignalingError records one signaling error in the named category.
// The category MUST be one of the SignalingErrorCategory constants; passing
// arbitrary strings here is a code smell and risks accidental PII leakage,
// so callers should always use the exported constants.
func (r *Registry) ObserveSignalingError(category SignalingErrorCategory) {
	r.SignalingErrors.WithLabelValues(string(category)).Inc()
}

// validErrorCategories enumerates the closed set of categories accepted by
// ObserveSignalingErrorCategory. The string form is used because the
// signaling package cannot import this package without a cycle. Any value
// not in the set is dropped to "other" rather than rejected so a caller
// bug can't silently exfiltrate the offending string into a label.
var validErrorCategories = map[string]struct{}{
	string(ErrTURNAllocFailed): {},
	string(ErrICETimeout):      {},
	string(ErrPeerUnreachable): {},
	string(ErrCallSetupFailed): {},
	string(ErrAuthFailed):      {},
	string(ErrInvalidMessage):  {},
	string(ErrRelayDelivery):   {},
}

// ObserveSignalingErrorCategory accepts a string category from a caller
// that cannot import the typed constants (e.g. internal/signaling, which
// would form an import cycle). Unknown categories collapse to "other" so a
// future caller can never smuggle a free-form string into a label value.
func (r *Registry) ObserveSignalingErrorCategory(category string) {
	if _, ok := validErrorCategories[category]; !ok {
		category = "other"
	}
	r.SignalingErrors.WithLabelValues(category).Inc()
}

// statusRecorder captures the response status without buffering the body.
// Flush and Hijack pass through to the underlying ResponseWriter so SSE
// streaming (/api/dashboard/stream) and WebSocket upgrades (/ws) work.
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

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support Hijack")
}

// Middleware returns an http.Handler middleware that records request count
// and duration into the registry. It calls routeOf to bucket the path into
// a coarse route group; that function is the privacy boundary, so it lives
// in this package and is exported for tests.
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, req)
		dur := time.Since(start).Seconds()
		route := RouteOf(req.URL.Path)
		status := strconv.Itoa(rec.status)
		r.HTTPRequestsTotal.WithLabelValues(route, req.Method, status).Inc()
		r.HTTPRequestDuration.WithLabelValues(route, req.Method, status).Observe(dur)
	})
}

// RouteOf maps a request path to a coarse route group. It MUST NOT echo
// path segments that contain user identifiers (phone numbers, UUIDs, call
// IDs, household IDs, magic-link tokens). The mapping is intentionally
// conservative: anything not in the allow-list collapses to "other" so an
// attacker can't smuggle a value into a label by hitting an unmapped path.
//
// Keep this list ordered roughly by request volume so the common case is
// near the top.
func RouteOf(path string) string {
	switch {
	case path == "/":
		return "/"
	case path == "/healthz":
		return "/healthz"
	case path == "/ws" || strings.HasPrefix(path, "/ws/"):
		return "/ws"
	case path == "/static" || strings.HasPrefix(path, "/static/"):
		return "/static"
	case path == "/metrics" || strings.HasPrefix(path, "/internal/metrics"):
		return "/internal/metrics"
	case strings.HasPrefix(path, "/internal/stats"):
		return "/internal/stats"

	// /auth: tokens follow /auth/magic; bucket the verify branch separately
	// from the request branch but never include the token itself.
	case path == "/auth/login":
		return "/auth/login"
	case path == "/auth/logout":
		return "/auth/logout"
	case path == "/auth/magic":
		return "/auth/magic"
	case strings.HasPrefix(path, "/auth/magic/"):
		return "/auth/magic/{token}"
	case strings.HasPrefix(path, "/auth/google"):
		return "/auth/google"
	case strings.HasPrefix(path, "/auth/dev-session"):
		return "/auth/dev-session"

	// /api: known endpoints are listed; per-call IDs collapse.
	case path == "/api/status":
		return "/api/status"
	case path == "/api/active-calls":
		return "/api/active-calls"
	case path == "/api/version":
		return "/api/version"
	case path == "/api/dashboard/stream":
		return "/api/dashboard/stream"
	case path == "/api/lines/number-available":
		return "/api/lines/number-available"
	case strings.HasPrefix(path, "/api/call/"):
		return "/api/call/{id}"
	case strings.HasPrefix(path, "/api/conference/"):
		return "/api/conference/{uuid}"
	case strings.HasPrefix(path, "/api/updates/"):
		return "/api/updates"
	case strings.HasPrefix(path, "/api/"):
		return "/api/other"

	// /phones, /calls, /links, /settings: collapse all per-number paths.
	case path == "/phones":
		return "/phones"
	case strings.HasPrefix(path, "/phones/"):
		return "/phones/{number}"
	case path == "/calls":
		return "/calls"
	case strings.HasPrefix(path, "/call/live/"):
		return "/call/live/{id}"
	case strings.HasPrefix(path, "/conference/live/"):
		return "/conference/live/{uuid}"
	case path == "/links" || strings.HasPrefix(path, "/links/"):
		return "/links"
	case path == "/settings" || strings.HasPrefix(path, "/settings/"):
		return "/settings"
	case path == "/welcome":
		return "/welcome"
	case path == "/onboard":
		return "/onboard"
	case path == "/connecting":
		return "/connecting"

	// Admin routes.
	case path == "/admin" || path == "/admin/" || strings.HasPrefix(path, "/admin/"):
		return "/admin"

	default:
		return "other"
	}
}

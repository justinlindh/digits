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
//   - ICE candidates relayed, labeled by candidate type (host/srflx/prflx/
//     relay) and transport (udp/tcp). Both labels are fixed enums derived
//     from the parsed candidate; no address, port, or peer identity.
//   - ICE-server responses issued, labeled only by whether TURN was included.
//     No device identity and never the TURN username or credential.
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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/justinlindh/digits/server/internal/httputil"
	"github.com/justinlindh/digits/server/internal/ratelimit"
)

const serviceName = "signald"

// Registry bundles a Prometheus registry, the metrics registered into it,
// and any GaugeFuncs that read live state. Keep one Registry per process.
type Registry struct {
	Reg *prometheus.Registry

	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	SignalingErrors  *prometheus.CounterVec
	ICECandidates    *prometheus.CounterVec
	ICEServersIssued *prometheus.CounterVec
	RateLimitRejects *prometheus.CounterVec
	BuildInfo        *prometheus.GaugeVec
}

// New builds a Registry with all metrics registered. Callers wire live-state
// gauges (active devices, active calls) by calling RegisterDevicesGauge and
// RegisterCallsGauge with closures that read in-memory state. Counters are
// driven by middleware and signaling code.
func New(version, commit string) *Registry {
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
			Subsystem: serviceName,
			Name:      "http_requests_total",
			Help:      "Total HTTP requests handled, partitioned by coarse route group, method, and status code. Routes are bucketed to avoid path components that carry user identifiers.",
		},
		[]string{"route", "method", "status"},
	)
	r.HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "digits",
			Subsystem: serviceName,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency in seconds, partitioned by coarse route group, method, and status code.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"route", "method", "status"},
	)
	r.SignalingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "digits",
			Subsystem: serviceName,
			Name:      "signaling_errors_total",
			Help:      "Signaling errors observed by the server, partitioned by a fixed category set. No peer identity is recorded.",
		},
		[]string{"category"},
	)
	r.ICECandidates = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "digits",
			Subsystem: serviceName,
			Name:      "ice_candidates_relayed_total",
			Help:      "ICE candidates relayed between peers, partitioned by candidate type (host/srflx/prflx/relay) and transport (udp/tcp). A rising relay share signals that direct and reflexive paths are failing and media is falling back to TURN. No peer identity is recorded.",
		},
		[]string{"cand_type", "transport"},
	)
	r.ICEServersIssued = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "digits",
			Subsystem: serviceName,
			Name:      "ice_servers_issued_total",
			Help:      "ICE-server responses handed to devices, partitioned by whether TURN was included. turn=\"false\" means the pod issued STUN only, which usually indicates a TURN misconfiguration. No device identity or credential is recorded.",
		},
		[]string{"turn"},
	)
	r.RateLimitRejects = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "digits",
			Subsystem: serviceName,
			Name:      "rate_limit_rejections_total",
			Help:      "Requests rejected by a rate limiter with 429, partitioned by limiter name (a fixed enum of endpoint groups). No IP or identity is recorded.",
		},
		[]string{"limiter"},
	)
	r.BuildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "digits",
			Subsystem: serviceName,
			Name:      "build_info",
			Help:      "Static build info. Always 1; the version and commit are carried as labels.",
		},
		[]string{"version", "commit"},
	)
	r.BuildInfo.WithLabelValues(version, commit).Set(1)

	reg.MustRegister(
		r.HTTPRequestsTotal,
		r.HTTPRequestDuration,
		r.SignalingErrors,
		r.ICECandidates,
		r.ICEServersIssued,
		r.RateLimitRejects,
		r.BuildInfo,
	)

	return r
}

// registerScrapeGauge installs a GaugeFunc that reads its value from the
// supplied closure on every scrape, so counts are never pre-computed or
// persisted elsewhere.
func (r *Registry) registerScrapeGauge(name, help string, read func() float64) {
	g := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "digits",
		Subsystem: serviceName,
		Name:      name,
		Help:      help,
	}, read)
	r.Reg.MustRegister(g)
}

// RegisterDevicesGauge installs a scrape-time gauge for the active-device
// count. Use a closure that returns len(hub.OnlineNumbers()).
func (r *Registry) RegisterDevicesGauge(read func() float64) {
	r.registerScrapeGauge("active_devices_current",
		"Current active device count, sampled at scrape time.",
		read)
}

// RegisterCallsGauge mirrors RegisterDevicesGauge for active calls.
func (r *Registry) RegisterCallsGauge(read func() float64) {
	r.registerScrapeGauge("active_calls_current",
		"Current active call count, sampled at scrape time.",
		read)
}

// validErrorCategories is the closed, product-defined set of categories
// accepted by ObserveSignalingError. The set is intentionally small: adding a
// category is a code change here, never a runtime label, so a caller can never
// smuggle a free-form string (or any PII it might carry) into a label value.
// Any value not in the set collapses to "other" rather than being rejected so
// a caller bug can't silently exfiltrate the offending string into a label.
var validErrorCategories = map[string]struct{}{
	"turn_alloc_failed": {},
	"ice_timeout":       {},
	"call_setup_failed": {},
	"auth_failed":       {},
	"invalid_message":   {},
	"relay_delivery":    {},
	"send_buffer_full":  {},
}

// sanitizeLabel collapses any value outside the closed allowlist to "other", so
// a caller can never widen the label space or smuggle a free-form string (or any
// PII it might carry) into a label value.
func sanitizeLabel(val string, allowed map[string]struct{}) string {
	if _, ok := allowed[val]; !ok {
		return "other"
	}
	return val
}

// ObserveSignalingError records one signaling error, partitioned by category.
// The category is a plain string because the sole caller (internal/signaling)
// cannot import this package without forming an import cycle, so it reports
// categories as string literals. Unknown categories collapse to "other" so a
// caller can never smuggle a free-form string into a label value.
func (r *Registry) ObserveSignalingError(category string) {
	r.SignalingErrors.WithLabelValues(sanitizeLabel(category, validErrorCategories)).Inc()
}

// validCandidateTypes and validTransports are the closed label sets for the
// ICE-candidate counter. As with validErrorCategories, anything outside the
// set collapses to "other" so a malformed candidate line (which the relay
// parses from untrusted device input) can never widen the label space or
// smuggle a value into a label.
var validCandidateTypes = map[string]struct{}{
	"host":  {},
	"srflx": {},
	"prflx": {},
	"relay": {},
}

var validTransports = map[string]struct{}{
	"udp": {},
	"tcp": {},
}

// ObserveICECandidate records one relayed ICE candidate, partitioned by type
// and transport. Unrecognized values collapse to "other".
func (r *Registry) ObserveICECandidate(candType, transport string) {
	r.ICECandidates.WithLabelValues(
		sanitizeLabel(candType, validCandidateTypes),
		sanitizeLabel(transport, validTransports),
	).Inc()
}

// ObserveICEServersIssued records one ICE-server response handed to a device,
// partitioned by whether TURN was included.
func (r *Registry) ObserveICEServersIssued(turn bool) {
	r.ICEServersIssued.WithLabelValues(strconv.FormatBool(turn)).Inc()
}

// validRateLimiters is the closed set of limiter names accepted by
// ObserveRateLimitRejection. As with the other label sets, a value outside it
// collapses to "other" so the label space can never be widened at runtime. It is
// built from ratelimit.Names so the allowlist and the limiters that feed it
// cannot drift: a rename on either side is a compile error, not a mislabeled
// metric.
var validRateLimiters = func() map[string]struct{} {
	m := make(map[string]struct{}, len(ratelimit.Names()))
	for _, name := range ratelimit.Names() {
		m[name] = struct{}{}
	}
	return m
}()

// ObserveRateLimitRejection records one request rejected by a rate limiter,
// partitioned by limiter name. Unknown names collapse to "other".
func (r *Registry) ObserveRateLimitRejection(limiter string) {
	r.RateLimitRejects.WithLabelValues(sanitizeLabel(limiter, validRateLimiters)).Inc()
}

// Middleware returns an http.Handler middleware that records request count
// and duration into the registry. It calls RouteOf to bucket the path into
// a coarse route group; that function is the privacy boundary, so it lives
// in this package and is exported for tests.
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &httputil.StatusRecorder{ResponseWriter: w, Status: http.StatusOK}
		next.ServeHTTP(rec, req)
		dur := time.Since(start).Seconds()
		route := RouteOf(req.URL.Path)
		status := strconv.Itoa(rec.Status)
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
	case strings.HasPrefix(path, "/api/release-audio/"):
		return "/api/release-audio"
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
	case path == "/changelog":
		return "/changelog"
	case strings.HasPrefix(path, "/invite/"):
		return "/invite/{token}"

	default:
		return "other"
	}
}

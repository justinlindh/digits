package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRouteOfBucketsKnownPaths(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/", "/"},
		{"/healthz", "/healthz"},
		{"/static/foo.css", "/static"},
		{"/static/foo.css?v=abc", "/static"},
		{"/ws", "/ws"},
		{"/ws/connect", "/ws"},
		{"/auth/magic", "/auth/magic"},
		{"/auth/magic/abc123tokensecret", "/auth/magic/{token}"},
		{"/api/status", "/api/status"},
		{"/api/call/42/link-health", "/api/call/{id}"},
		{"/api/conference/8b6e2bb8-19fa-4f0a-8af9-f60094f0a7d5/kick", "/api/conference/{uuid}"},
		{"/phones/+15551234567", "/phones/{number}"},
		{"/phones/+15551234567/edit", "/phones/{number}"},
		{"/call/live/42", "/call/live/{id}"},
		{"/conference/live/8b6e2bb8-19fa-4f0a-8af9-f60094f0a7d5", "/conference/live/{uuid}"},
		{"/internal/stats", "/internal/stats"},
		{"/internal/metrics", "/internal/metrics"},
		{"/changelog", "/changelog"},
		{"/invite/abc123secret", "/invite/{token}"},
		{"/invite/abc123secret/accept", "/invite/{token}"},
		{"/api/release-audio/pi/v1.2.3", "/api/release-audio"},
		// Random/unknown paths must NOT echo segments back as labels: that
		// is the leakage path we explicitly guard against.
		{"/some/secret/that/should/not/appear", "other"},
	}
	for _, c := range cases {
		// strip query string before bucketing, like the middleware does
		p := c.in
		if i := strings.Index(p, "?"); i >= 0 {
			p = p[:i]
		}
		if got := RouteOf(p); got != c.want {
			t.Errorf("RouteOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRouteOfNeverEchoesNumber(t *testing.T) {
	// Belt-and-braces: a phone number must never appear in the bucketed
	// route. If a future maintainer adds a typed switch case that lets the
	// number through, this test fires.
	for _, p := range []string{
		"/phones/+15551234567/edit",
		"/phones/15551234567",
		"/phones/+15551234567/voice-style",
		"/api/call/12345/disconnect",
		"/api/conference/abc/kick",
		"/conference/live/abc-def",
	} {
		got := RouteOf(p)
		if strings.Contains(got, "1234567") {
			t.Errorf("RouteOf(%q) leaked digits: %q", p, got)
		}
		// Bucket result should be a static template, never a real path.
		if got == p {
			t.Errorf("RouteOf(%q) returned the raw path", p)
		}
	}
}

func TestMiddlewareCountsAndTimes(t *testing.T) {
	r := New("test", "abc123")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/phones/+15551234567/edit", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	h := r.Middleware(mux)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Drive three requests; vary status to exercise the status label.
	for range 3 {
		resp, err := http.Get(srv.URL + "/api/status")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	resp, err := http.Get(srv.URL + "/phones/+15551234567/edit")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	// Counter assertion via testutil.ToFloat64.
	if got := testutil.ToFloat64(r.HTTPRequestsTotal.WithLabelValues("/api/status", "GET", "200")); got != 3 {
		t.Errorf("api/status counter = %v, want 3", got)
	}
	if got := testutil.ToFloat64(r.HTTPRequestsTotal.WithLabelValues("/phones/{number}", "GET", "404")); got != 1 {
		t.Errorf("phones/{number} counter = %v, want 1", got)
	}

	// Histogram assertion: at least 4 observations recorded across the two
	// label sets. Inspect via Gather rather than a per-label counter.
	mfs, err := r.Reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var foundDur bool
	for _, mf := range mfs {
		if mf.GetName() == "digits_signald_http_request_duration_seconds" {
			foundDur = true
			var totalCount uint64
			for _, m := range mf.GetMetric() {
				totalCount += m.GetHistogram().GetSampleCount()
			}
			if totalCount != 4 {
				t.Errorf("histogram total samples = %d, want 4", totalCount)
			}
		}
	}
	if !foundDur {
		t.Error("histogram not found in registry")
	}
}

func TestActiveDeviceAndCallGauges(t *testing.T) {
	r := New("test", "abc123")
	devCount := 0
	callCount := 0
	r.RegisterDevicesGauge(func() float64 { return float64(devCount) })
	r.RegisterCallsGauge(func() float64 { return float64(callCount) })

	devCount = 7
	callCount = 2

	mfs, err := r.Reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, mf := range mfs {
		switch mf.GetName() {
		case "digits_signald_active_devices_current", "digits_signald_active_calls_current":
			for _, m := range mf.GetMetric() {
				got[mf.GetName()] = m.GetGauge().GetValue()
			}
		}
	}
	if got["digits_signald_active_devices_current"] != 7 {
		t.Errorf("active_devices_current = %v, want 7", got["digits_signald_active_devices_current"])
	}
	if got["digits_signald_active_calls_current"] != 2 {
		t.Errorf("active_calls_current = %v, want 2", got["digits_signald_active_calls_current"])
	}
}

func TestObserveSignalingError(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveSignalingError("turn_alloc_failed")
	r.ObserveSignalingError("turn_alloc_failed")
	r.ObserveSignalingError("ice_timeout")

	if got := testutil.ToFloat64(r.SignalingErrors.WithLabelValues("turn_alloc_failed")); got != 2 {
		t.Errorf("turn_alloc_failed counter = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.SignalingErrors.WithLabelValues("ice_timeout")); got != 1 {
		t.Errorf("ice_timeout counter = %v, want 1", got)
	}
}

// A category outside the closed set must collapse to "other" rather than land
// in a label of its own, so a caller bug can never write a free-form string
// (and any PII it might carry) into the signaling_errors_total label.
func TestObserveSignalingErrorUnknownCollapsesToOther(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveSignalingError("3145551234")
	r.ObserveSignalingError("not-a-category")

	if got := testutil.ToFloat64(r.SignalingErrors.WithLabelValues("other")); got != 2 {
		t.Errorf("other counter = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.SignalingErrors.WithLabelValues("3145551234")); got != 0 {
		t.Errorf("unknown category leaked into its own label: got %v, want 0", got)
	}
}

func TestObserveRateLimitRejection(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveRateLimitRejection("auth_magic")
	r.ObserveRateLimitRejection("auth_magic")
	r.ObserveRateLimitRejection("ws")

	if got := testutil.ToFloat64(r.RateLimitRejects.WithLabelValues("auth_magic")); got != 2 {
		t.Errorf("auth_magic counter = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.RateLimitRejects.WithLabelValues("ws")); got != 1 {
		t.Errorf("ws counter = %v, want 1", got)
	}
}

// An unknown limiter name must collapse to "other" so a caller bug can never
// widen the label space on rate_limit_rejections_total.
func TestObserveRateLimitRejectionUnknownCollapsesToOther(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveRateLimitRejection("not-a-limiter")

	if got := testutil.ToFloat64(r.RateLimitRejects.WithLabelValues("other")); got != 1 {
		t.Errorf("other counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.RateLimitRejects.WithLabelValues("not-a-limiter")); got != 0 {
		t.Errorf("unknown limiter leaked into its own label: got %v, want 0", got)
	}
}

func TestObserveICECandidate(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveICECandidate("relay", "tcp")
	r.ObserveICECandidate("relay", "tcp")
	r.ObserveICECandidate("host", "udp")

	if got := testutil.ToFloat64(r.ICECandidates.WithLabelValues("relay", "tcp")); got != 2 {
		t.Errorf("relay/tcp counter = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.ICECandidates.WithLabelValues("host", "udp")); got != 1 {
		t.Errorf("host/udp counter = %v, want 1", got)
	}
}

// A candidate type or transport outside the closed set must collapse to
// "other" so a malformed candidate line (parsed from untrusted device input)
// can never widen the label space or smuggle an address into a label.
func TestObserveICECandidateUnknownCollapsesToOther(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveICECandidate("192.168.1.1", "sctp")

	if got := testutil.ToFloat64(r.ICECandidates.WithLabelValues("other", "other")); got != 1 {
		t.Errorf("other/other counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.ICECandidates.WithLabelValues("192.168.1.1", "sctp")); got != 0 {
		t.Errorf("unknown labels leaked: got %v, want 0", got)
	}
}

func TestObserveICEServersIssued(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveICEServersIssued(true)
	r.ObserveICEServersIssued(true)
	r.ObserveICEServersIssued(false)

	if got := testutil.ToFloat64(r.ICEServersIssued.WithLabelValues("true")); got != 2 {
		t.Errorf("turn=true counter = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.ICEServersIssued.WithLabelValues("false")); got != 1 {
		t.Errorf("turn=false counter = %v, want 1", got)
	}
}

func TestBuildInfoLabel(t *testing.T) {
	r := New("v1.2.3", "deadbeef")
	if got := testutil.ToFloat64(r.BuildInfo.WithLabelValues("v1.2.3", "deadbeef")); got != 1 {
		t.Errorf("build_info = %v, want 1", got)
	}
}

// Sanity check: scraping the registry with promhttp produces text with the
// expected metric names and no surprise labels (no hidden user identifier
// labels added by collectors).
func TestPromhttpExportsExpectedMetrics(t *testing.T) {
	r := New("test", "abc123")
	// Exercise each metric so the prometheus text exposition emits a HELP/TYPE
	// line for it. Empty *Vec families don't appear in the scrape output until
	// at least one labelled child has been observed.
	r.HTTPRequestsTotal.WithLabelValues("/api/status", "GET", "200").Inc()
	r.HTTPRequestDuration.WithLabelValues("/api/status", "GET", "200").Observe(0.012)
	r.RegisterDevicesGauge(func() float64 { return 0 })
	r.RegisterCallsGauge(func() float64 { return 0 })
	r.ObserveSignalingError("turn_alloc_failed")
	r.ObserveLogin("magic_link", "success")
	r.ObserveMagicLink("issued")
	r.ObservePairing("success")

	srv := httptest.NewServer(promhttp.HandlerFor(r.Reg, promhttp.HandlerOpts{Registry: r.Reg}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(t, resp)

	for _, want := range []string{
		"digits_signald_http_requests_total",
		"digits_signald_http_request_duration_seconds",
		"digits_signald_active_devices_current",
		"digits_signald_active_calls_current",
		"digits_signald_signaling_errors_total",
		"digits_signald_build_info",
		"digits_signald_auth_logins_total",
		"digits_signald_auth_magic_links_total",
		"digits_signald_auth_pairings_total",
		"go_goroutines",
		"process_open_fds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q", want)
		}
	}
	// Forbidden: a typo'd label or an accidental "user" / "phone" label.
	for _, banned := range []string{
		"user_id=", "phone=", "number=", "email=", "ip=",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("scrape body contains banned label %q", banned)
		}
	}
}

// Helper to keep the tests above readable. Reads a response body and fails
// the test if any error occurs.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}

// Sanity assertion: the registry created by New is prometheus.Registerer-
// compatible. If a future change swaps Registry's Reg field for a custom
// type, this still has to compile.
var _ prometheus.Registerer = (*prometheus.Registry)(nil)

func TestObserveLogin(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveLogin("magic_link", "success")
	r.ObserveLogin("magic_link", "success")
	r.ObserveLogin("magic_link", "failure")
	r.ObserveLogin("google", "success")
	r.ObserveLogin("dev", "success")

	if got := testutil.ToFloat64(r.AuthLogins.WithLabelValues("magic_link", "success")); got != 2 {
		t.Errorf("magic_link/success = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.AuthLogins.WithLabelValues("magic_link", "failure")); got != 1 {
		t.Errorf("magic_link/failure = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.AuthLogins.WithLabelValues("google", "success")); got != 1 {
		t.Errorf("google/success = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.AuthLogins.WithLabelValues("dev", "success")); got != 1 {
		t.Errorf("dev/success = %v, want 1", got)
	}
}

// A login method or result outside the closed set must collapse to "other" so a
// caller bug can never smuggle an email or other free-form string into a label.
func TestObserveLoginUnknownCollapsesToOther(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveLogin("ada@example.com", "maybe")

	if got := testutil.ToFloat64(r.AuthLogins.WithLabelValues("other", "other")); got != 1 {
		t.Errorf("other/other = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.AuthLogins.WithLabelValues("ada@example.com", "maybe")); got != 0 {
		t.Errorf("unknown login labels leaked: got %v, want 0", got)
	}
}

func TestObserveMagicLink(t *testing.T) {
	r := New("test", "abc123")
	r.ObserveMagicLink("issued")
	r.ObserveMagicLink("issued")
	r.ObserveMagicLink("consumed")
	r.ObserveMagicLink("token-abc123") // unknown collapses to other

	if got := testutil.ToFloat64(r.MagicLinks.WithLabelValues("issued")); got != 2 {
		t.Errorf("issued = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.MagicLinks.WithLabelValues("consumed")); got != 1 {
		t.Errorf("consumed = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.MagicLinks.WithLabelValues("other")); got != 1 {
		t.Errorf("other = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.MagicLinks.WithLabelValues("token-abc123")); got != 0 {
		t.Errorf("unknown event leaked into its own label: got %v, want 0", got)
	}
}

func TestObservePairing(t *testing.T) {
	r := New("test", "abc123")
	r.ObservePairing("success")
	r.ObservePairing("failure")
	r.ObservePairing("failure")
	r.ObservePairing("482913") // an actual pairing code must never become a label

	if got := testutil.ToFloat64(r.Pairings.WithLabelValues("success")); got != 1 {
		t.Errorf("success = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.Pairings.WithLabelValues("failure")); got != 2 {
		t.Errorf("failure = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.Pairings.WithLabelValues("other")); got != 1 {
		t.Errorf("other = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.Pairings.WithLabelValues("482913")); got != 0 {
		t.Errorf("pairing code leaked into its own label: got %v, want 0", got)
	}
}

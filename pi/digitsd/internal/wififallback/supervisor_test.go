package wififallback

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/config"
)

type fakeNM struct {
	connected bool
	err       error
}

func (f *fakeNM) HasConnectivity() (bool, error) { return f.connected, f.err }

type fakeAP struct {
	hasClient bool
	upCalls   int
	downCalls int
}

func (f *fakeAP) Up() error                { f.upCalls++; return nil }
func (f *fakeAP) Down() error              { f.downCalls++; return nil }
func (f *fakeAP) HasClient() (bool, error) { return f.hasClient, nil }

func testCfg() config.WiFiFallback {
	return config.WiFiFallback{
		Enabled:           true,
		GraceInitial:      5 * time.Minute,
		GraceMax:          30 * time.Minute,
		APNoClientTimeout: 10 * time.Minute,
	}
}

func newTestSupervisor(nm *fakeNM, ap *fakeAP, call *bool) *Supervisor {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewSupervisor(testCfg(), nm, ap, func() bool { return *call }, quiet)
}

func TestStationOKStaysWhenConnected(t *testing.T) {
	nm := &fakeNM{connected: true}
	ap := &fakeAP{}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	now := time.Unix(1000, 0)

	s.Tick(now)

	if s.State() != StateStationOK {
		t.Errorf("state = %v, want StateStationOK", s.State())
	}
	if ap.upCalls != 0 {
		t.Errorf("unexpected AP Up calls: %d", ap.upCalls)
	}
}

func TestStationOKTransitionsOnConnectivityLoss(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	now := time.Unix(1000, 0)

	s.Tick(now)

	if s.State() != StateStationDegraded {
		t.Errorf("state = %v, want StateStationDegraded", s.State())
	}
	wantExpires := now.Add(5 * time.Minute)
	if !s.graceExpires.Equal(wantExpires) {
		t.Errorf("graceExpires = %v, want %v", s.graceExpires, wantExpires)
	}
}

func TestStationDegradedRecoversDuringGrace(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	start := time.Unix(1000, 0)

	s.Tick(start)
	if s.State() != StateStationDegraded {
		t.Fatalf("expected StateStationDegraded, got %v", s.State())
	}

	nm.connected = true
	s.Tick(start.Add(2 * time.Minute))

	if s.State() != StateStationOK {
		t.Errorf("state = %v, want StateStationOK", s.State())
	}
	if ap.upCalls != 0 {
		t.Errorf("AP was unnecessarily brought up: %d calls", ap.upCalls)
	}
}

func TestStationDegradedFlipsToAPAfterGrace(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	start := time.Unix(1000, 0)

	s.Tick(start)                      // degraded, graceExpires = +5m
	s.Tick(start.Add(6 * time.Minute)) // flips to AP_OFFERED

	if s.State() != StateAPOffered {
		t.Errorf("state = %v, want StateAPOffered", s.State())
	}
	if ap.upCalls != 1 {
		t.Errorf("AP.Up calls = %d, want 1", ap.upCalls)
	}
}

func TestCallActiveHoldsFlipToAP(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{}
	call := true
	s := newTestSupervisor(nm, ap, &call)
	start := time.Unix(1000, 0)

	s.Tick(start)                      // degraded
	s.Tick(start.Add(6 * time.Minute)) // grace expired but call active -> hold
	if s.State() != StateStationDegraded {
		t.Errorf("state = %v, want held StateStationDegraded", s.State())
	}
	if ap.upCalls != 0 {
		t.Errorf("AP should not have been brought up during call")
	}

	call = false
	s.Tick(start.Add(7 * time.Minute))
	if s.State() != StateAPOffered {
		t.Errorf("state = %v, want StateAPOffered after call ended", s.State())
	}
	if ap.upCalls != 1 {
		t.Errorf("AP.Up calls = %d, want 1", ap.upCalls)
	}
}

func TestAPOfferedClientAssociates(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	start := time.Unix(1000, 0)

	s.Tick(start)                      // degraded
	s.Tick(start.Add(6 * time.Minute)) // AP_OFFERED

	ap.hasClient = true
	s.Tick(start.Add(7 * time.Minute))

	if s.State() != StateAPActive {
		t.Errorf("state = %v, want StateAPActive", s.State())
	}
}

func TestAPOfferedNoClientTimeoutWithBackoff(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	start := time.Unix(1000, 0)

	s.Tick(start)                       // degraded, backoff=5m, graceExpires=+5m
	s.Tick(start.Add(6 * time.Minute))  // AP_OFFERED, apExpires=+10m from now=+6m -> +16m
	s.Tick(start.Add(17 * time.Minute)) // ap timeout hit -> degraded, backoff grows to 10m

	if s.State() != StateStationDegraded {
		t.Errorf("state = %v, want StateStationDegraded", s.State())
	}
	if ap.downCalls != 1 {
		t.Errorf("AP.Down calls = %d, want 1", ap.downCalls)
	}
	// new graceExpires = now + new backoff = +17m + 10m = +27m
	wantExpires := start.Add(17 * time.Minute).Add(10 * time.Minute)
	if !s.graceExpires.Equal(wantExpires) {
		t.Errorf("graceExpires = %v, want %v", s.graceExpires, wantExpires)
	}
}

func TestBackoffCapsAtMax(t *testing.T) {
	s := &Supervisor{cfg: testCfg()}
	s.backoff = 20 * time.Minute
	s.growBackoff()
	if s.backoff != 30*time.Minute {
		t.Errorf("backoff = %v, want 30m (capped)", s.backoff)
	}
	s.growBackoff()
	if s.backoff != 30*time.Minute {
		t.Errorf("backoff after second grow = %v, want 30m (still capped)", s.backoff)
	}
}

func TestDisabledKillSwitch(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{}
	call := false
	cfg := testCfg()
	cfg.Enabled = false
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewSupervisor(cfg, nm, ap, func() bool { return call }, quiet)

	s.Tick(time.Unix(1000, 0))
	s.Tick(time.Unix(2000, 0))

	if s.State() != StateStationOK {
		t.Errorf("state = %v, want StateStationOK (disabled)", s.State())
	}
	if ap.upCalls != 0 {
		t.Error("AP should never be brought up when disabled")
	}
}

func TestAPActiveClientLeavesReentersAPOffered(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{hasClient: true}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	start := time.Unix(1000, 0)

	s.Tick(start)                      // degraded
	s.Tick(start.Add(6 * time.Minute)) // AP_OFFERED
	if s.State() != StateAPOffered {
		t.Fatalf("after first post-grace tick: state = %v, want StateAPOffered", s.State())
	}
	s.Tick(start.Add(6*time.Minute + time.Second)) // next tick detects client -> AP_ACTIVE
	if s.State() != StateAPActive {
		t.Fatalf("after second post-grace tick: state = %v, want StateAPActive", s.State())
	}

	// Client disconnects
	ap.hasClient = false
	s.Tick(start.Add(7 * time.Minute))

	if s.State() != StateAPOffered {
		t.Errorf("state = %v, want StateAPOffered after client left", s.State())
	}
	wantExpires := start.Add(7 * time.Minute).Add(2 * time.Minute)
	if !s.apExpires.Equal(wantExpires) {
		t.Errorf("apExpires = %v, want %v (2m re-grace)", s.apExpires, wantExpires)
	}
}

func TestNMQueryErrorTreatedAsDisconnected(t *testing.T) {
	nm := &fakeNM{connected: false, err: errors.New("nmcli boom")}
	ap := &fakeAP{}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	start := time.Unix(1000, 0)

	s.Tick(start)                      // degraded (error treated as not connected)
	if s.State() != StateStationDegraded {
		t.Fatalf("state = %v, want StateStationDegraded", s.State())
	}
	s.Tick(start.Add(6 * time.Minute)) // AP_OFFERED after grace
	if s.State() != StateAPOffered {
		t.Errorf("state = %v, want StateAPOffered despite nm errors", s.State())
	}
}

func TestBackoffResetsOnRecovery(t *testing.T) {
	nm := &fakeNM{connected: false}
	ap := &fakeAP{}
	call := false
	s := newTestSupervisor(nm, ap, &call)
	start := time.Unix(1000, 0)

	// Cycle once to grow backoff to 10m.
	s.Tick(start)                          // degraded, backoff=5m, graceExpires=+5m
	s.Tick(start.Add(6 * time.Minute))     // AP_OFFERED, apExpires=+16m
	s.Tick(start.Add(17 * time.Minute))    // timeout -> degraded, backoff=10m, graceExpires=+27m

	// Recover.
	nm.connected = true
	s.Tick(start.Add(18 * time.Minute))    // StationOK, backoff resets to 5m
	if s.State() != StateStationOK {
		t.Fatalf("state = %v, want StateStationOK", s.State())
	}

	// Drop again. New graceExpires should use reset backoff of 5m.
	nm.connected = false
	s.Tick(start.Add(19 * time.Minute))
	wantExpires := start.Add(19 * time.Minute).Add(5 * time.Minute)
	if !s.graceExpires.Equal(wantExpires) {
		t.Errorf("graceExpires = %v, want %v (backoff should have reset)", s.graceExpires, wantExpires)
	}
}

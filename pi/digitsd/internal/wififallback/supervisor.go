package wififallback

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/config"
)

// State represents the current phase of the WiFi fallback supervisor.
type State int

const (
	StateStationOK       State = iota // NM reports full connectivity
	StateStationDegraded              // no connectivity, counting down grace window
	StateAPOffered                    // setup AP up, waiting for client to associate
	StateAPActive                     // client connected, user is configuring
)

func (s State) String() string {
	switch s {
	case StateStationOK:
		return "STATION_OK"
	case StateStationDegraded:
		return "STATION_DEGRADED"
	case StateAPOffered:
		return "AP_OFFERED"
	case StateAPActive:
		return "AP_ACTIVE"
	default:
		return "UNKNOWN"
	}
}

// Supervisor watches NM connectivity and decides when to flip the device
// between station mode and setup-AP mode. It must be driven by Tick (for
// tests) or Run (for production).
type Supervisor struct {
	cfg        config.WiFiFallback
	nm         NMStatusChecker
	ap         APController
	callActive func() bool
	logger     *slog.Logger

	state        atomic.Int32 // stores State
	graceExpires time.Time
	apExpires    time.Time
	backoff      time.Duration
	lastOKAt     time.Time
}

// NewSupervisor creates a Supervisor. If logger is nil, slog.Default() is used.
func NewSupervisor(cfg config.WiFiFallback, nm NMStatusChecker, ap APController, callActive func() bool, logger *slog.Logger) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Supervisor{
		cfg:        cfg,
		nm:         nm,
		ap:         ap,
		callActive: callActive,
		logger:     logger,
		backoff:    cfg.GraceInitial,
	}
	s.state.Store(int32(StateStationOK))
	return s
}

// State returns the current supervisor state.
func (s *Supervisor) State() State {
	return State(s.state.Load())
}

// Tick runs one iteration of the state machine with the given now value.
// Tests call this directly; Run invokes it on a ticker.
func (s *Supervisor) Tick(now time.Time) {
	if !s.cfg.Enabled {
		return
	}
	connected, err := s.nm.HasConnectivity()
	if err != nil {
		s.logger.Warn("wifi-fallback: nm query failed", "err", err)
		// Treat as not connected for escalation purposes.
		connected = false
	}

	switch s.State() {
	case StateStationOK:
		s.tickStationOK(now, connected)
	case StateStationDegraded:
		s.tickStationDegraded(now, connected)
	case StateAPOffered:
		s.tickAPOffered(now, connected)
	case StateAPActive:
		s.tickAPActive(now, connected)
	}
}

func (s *Supervisor) tickStationOK(now time.Time, connected bool) {
	if connected {
		s.lastOKAt = now
		return
	}
	s.transitionTo(now, StateStationDegraded, "connectivity lost")
}

func (s *Supervisor) tickStationDegraded(now time.Time, connected bool) {
	if connected {
		s.transitionTo(now, StateStationOK, "connectivity restored")
		return
	}
	if now.Before(s.graceExpires) {
		return
	}
	if s.callActive() {
		s.logger.Info("wifi-fallback: grace expired but call active, holding")
		return
	}
	s.transitionTo(now, StateAPOffered, "grace expired")
}

func (s *Supervisor) tickAPOffered(now time.Time, _ bool) {
	hasClient, err := s.ap.HasClient()
	if err != nil {
		s.logger.Warn("wifi-fallback: HasClient failed", "err", err)
	}
	if hasClient {
		s.transitionTo(now, StateAPActive, "client associated")
		return
	}
	if now.Before(s.apExpires) {
		return
	}
	if err := s.ap.Down(); err != nil {
		s.logger.Error("wifi-fallback: AP Down failed", "err", err)
	}
	s.growBackoff()
	s.transitionTo(now, StateStationDegraded, "ap no-client timeout")
}

func (s *Supervisor) tickAPActive(now time.Time, _ bool) {
	hasClient, err := s.ap.HasClient()
	if err != nil {
		s.logger.Warn("wifi-fallback: HasClient failed in AP_ACTIVE, holding", "err", err)
		return
	}
	if hasClient {
		return
	}
	s.transitionTo(now, StateAPOffered, "client left")
	// Tighter re-grace than a fresh AP_OFFERED -- if they just need to reconnect.
	s.apExpires = now.Add(2 * time.Minute)
}

func (s *Supervisor) transitionTo(now time.Time, next State, reason string) {
	prev := s.State()
	s.state.Store(int32(next))
	s.logger.Info("wifi-fallback: transition", "from", prev.String(), "to", next.String(), "reason", reason)
	switch next {
	case StateStationOK:
		s.lastOKAt = now
		s.backoff = s.cfg.GraceInitial
	case StateStationDegraded:
		s.graceExpires = now.Add(s.backoff)
	case StateAPOffered:
		// AP is already up when demoting from AP_ACTIVE; don't call Up again.
		if prev != StateAPActive {
			if err := s.ap.Up(); err != nil {
				s.logger.Error("wifi-fallback: AP Up failed", "err", err)
			}
			s.apExpires = now.Add(s.cfg.APNoClientTimeout)
		}
		// When prev == StateAPActive the caller is responsible for setting
		// apExpires to the desired re-grace window.
	}
}

func (s *Supervisor) growBackoff() {
	next := s.backoff * 2
	if next > s.cfg.GraceMax {
		next = s.cfg.GraceMax
	}
	s.backoff = next
}

// Run ticks the supervisor every 5 seconds until ctx is cancelled.
func (s *Supervisor) Run(ctx context.Context) {
	s.logger.Info("wifi-fallback: supervisor started", "enabled", s.cfg.Enabled)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("wifi-fallback: supervisor stopped")
			return
		case now := <-t.C:
			s.Tick(now)
		}
	}
}

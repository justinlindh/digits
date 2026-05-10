package subsystem

import (
	"context"
	"time"
)

type State int

const (
	StatePending State = iota
	StateInitializing
	StateReady
	StateFailed
	StateDegraded
	StateDisabled
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateInitializing:
		return "initializing"
	case StateReady:
		return "ready"
	case StateFailed:
		return "failed"
	case StateDegraded:
		return "degraded"
	case StateDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

type ModuleStatus struct {
	State    State
	Message  string
	Duration time.Duration
}

type Module interface {
	Name() string
	Init(ctx context.Context) error
	Status() ModuleStatus
	Shutdown(ctx context.Context) error
}

// IsReady reports whether m has finished its Init successfully and has not
// since failed or been disabled.
func IsReady(m Module) bool {
	return m.Status().State == StateReady
}

type HealthChecker interface {
	HealthCheck() error
}

type Registration struct {
	Module   Module
	Deps     []string
	Required bool
	Enabled  bool
}

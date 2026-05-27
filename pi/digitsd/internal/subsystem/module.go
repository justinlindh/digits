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

// Registration declares a module and how the Manager should treat it.
// The zero value is the common case: enabled, not required, no deps. Set
// Disabled to register a module that the Manager should skip but still report
// in status snapshots (used for modules managed externally, e.g. by systemd).
type Registration struct {
	Module   Module
	Deps     []string
	Required bool
	Disabled bool
}

package subsystem

import (
	"context"
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

// Module is a unit of initialization managed by Manager. IsReady reports
// whether the module's Init completed successfully; the rich state (message,
// duration) is tracked by the Manager and exposed via Manager.Status.
type Module interface {
	Name() string
	Init(ctx context.Context) error
	IsReady() bool
	Shutdown(ctx context.Context) error
}

// IsReady is a convenience wrapper so callers that hold a Module can write
// `subsystem.IsReady(m)` to match the natural "is X ready?" reading order.
func IsReady(m Module) bool { return m.IsReady() }

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

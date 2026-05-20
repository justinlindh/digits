package subsystem

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// NamedStatus is a snapshot of a single module's state.
type NamedStatus struct {
	Name     string
	State    State
	Message  string
	Duration time.Duration
	Required bool
}

// Manager initializes, monitors, and shuts down a set of registered modules
// in dependency order.
type Manager struct {
	regs   []Registration
	layers [][]Registration
	mu     sync.Mutex
	states map[string]*NamedStatus
}

// NewManager creates a Manager. Enabled (default) modules start as
// StatePending; explicitly Disabled modules start as StateDisabled.
func NewManager(regs []Registration) *Manager {
	states := make(map[string]*NamedStatus, len(regs))
	for _, r := range regs {
		s := &NamedStatus{
			Name:     r.Module.Name(),
			Required: r.Required,
		}
		if r.Disabled {
			s.State = StateDisabled
		} else {
			s.State = StatePending
		}
		states[r.Module.Name()] = s
	}
	return &Manager{
		regs:   regs,
		states: states,
	}
}

// Run initializes all enabled modules in topological layer order.
// Modules within a layer are initialized concurrently.
// A required module failure stops processing of subsequent layers and returns an error.
// An optional module failure is logged but does not halt init.
func (m *Manager) Run(ctx context.Context) error {
	layers, err := topoLayers(m.regs)
	if err != nil {
		return fmt.Errorf("subsystem: dependency resolution failed: %w", err)
	}

	m.mu.Lock()
	m.layers = layers
	m.mu.Unlock()

	for _, layer := range layers {
		if err := m.runLayer(ctx, layer); err != nil {
			return err
		}
	}

	m.logSummary(ctx)
	return nil
}

// runLayer initializes all modules in one layer concurrently and waits for all to finish.
func (m *Manager) runLayer(ctx context.Context, layer []Registration) error {
	type result struct {
		reg Registration
		err error
		dur time.Duration
	}

	results := make(chan result, len(layer))

	for _, r := range layer {
		r := r
		go func() {
			m.setState(r.Module.Name(), StateInitializing, "", 0)
			start := time.Now()
			initErr := r.Module.Init(ctx)
			dur := time.Since(start)
			results <- result{reg: r, err: initErr, dur: dur}
		}()
	}

	var firstRequiredErr error
	for range layer {
		res := <-results
		name := res.reg.Module.Name()
		if res.err != nil {
			msg := res.err.Error()
			m.setState(name, StateFailed, msg, res.dur)
			if res.reg.Required {
				slog.ErrorContext(ctx, "subsystem: module failed", "module", name, "err", res.err)
				if firstRequiredErr == nil {
					firstRequiredErr = fmt.Errorf("required module %s failed: %w", name, res.err)
				}
			} else {
				slog.WarnContext(ctx, "subsystem: module failed (optional, continuing)", "module", name, "err", res.err)
			}
		} else {
			m.setState(name, StateReady, "", res.dur)
			slog.InfoContext(ctx, "subsystem: module ready", "module", name, "duration", res.dur.Round(time.Millisecond))
		}
	}

	return firstRequiredErr
}

// Shutdown shuts down all ready modules in reverse layer order.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	layers := m.layers
	m.mu.Unlock()

	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		var wg sync.WaitGroup
		for _, r := range layer {
			r := r
			if !IsReady(r.Module) {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := r.Module.Shutdown(ctx); err != nil {
					slog.ErrorContext(ctx, "subsystem: module shutdown error", "module", r.Module.Name(), "err", err)
				}
			}()
		}
		wg.Wait()
	}
}

// Status returns a snapshot of all module states.
func (m *Manager) Status() []NamedStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]NamedStatus, 0, len(m.states))
	for _, s := range m.states {
		out = append(out, *s)
	}
	return out
}

func (m *Manager) setState(name string, state State, msg string, dur time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.states[name]; ok {
		s.State = state
		s.Message = msg
		s.Duration = dur
	}
}

func (m *Manager) logSummary(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var ready, total, degraded int
	for _, s := range m.states {
		if s.State == StateDisabled {
			continue
		}
		total++
		switch s.State {
		case StateReady:
			ready++
		case StateDegraded:
			degraded++
		}
	}
	slog.InfoContext(ctx, "subsystem: init complete", "ready", ready, "total", total, "degraded", degraded)
}

// topoLayers sorts regs into dependency-ordered layers using Kahn's algorithm.
// Disabled modules are excluded from the output layers; their names are also
// excluded from the dep graph so that dependents can still resolve.
func topoLayers(regs []Registration) ([][]Registration, error) {
	// Build index of enabled modules by name.
	enabled := make(map[string]bool, len(regs))
	for _, r := range regs {
		if !r.Disabled {
			enabled[r.Module.Name()] = true
		}
	}

	// Build in-degree map and adjacency list (only for enabled modules).
	inDegree := make(map[string]int, len(regs))
	deps := make(map[string][]string, len(regs)) // name -> list of enabled names that must finish first

	for _, r := range regs {
		if r.Disabled {
			continue
		}
		name := r.Module.Name()
		if _, exists := inDegree[name]; !exists {
			inDegree[name] = 0
		}
		for _, dep := range r.Deps {
			if !enabled[dep] {
				// disabled dep: ignore it
				continue
			}
			deps[dep] = append(deps[dep], name)
			inDegree[name]++
		}
	}

	// Collect enabled regs by name for layer construction.
	regByName := make(map[string]Registration, len(regs))
	for _, r := range regs {
		if !r.Disabled {
			regByName[r.Module.Name()] = r
		}
	}

	// Kahn's algorithm.
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	var layers [][]Registration
	processed := 0

	for len(queue) > 0 {
		layer := make([]Registration, 0, len(queue))
		next := queue
		queue = nil

		for _, name := range next {
			layer = append(layer, regByName[name])
			processed++
			for _, dependent := range deps[name] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					queue = append(queue, dependent)
				}
			}
		}
		layers = append(layers, layer)
	}

	if processed != len(inDegree) {
		return nil, fmt.Errorf("subsystem: cycle detected in module dependencies")
	}

	return layers, nil
}

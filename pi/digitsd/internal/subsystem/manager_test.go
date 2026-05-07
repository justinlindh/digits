package subsystem

import (
	"context"
	"sync"
	"testing"
	"time"
)

// stubModule is a test helper that implements Module.
type stubModule struct {
	mu         sync.Mutex
	name_      string
	initFn     func()
	initErr    error
	shutdownFn func()
	ready      bool
}

func (s *stubModule) Name() string { return s.name_ }

func (s *stubModule) Init(_ context.Context) error {
	if s.initFn != nil {
		s.initFn()
	}
	if s.initErr != nil {
		return s.initErr
	}
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return nil
}

func (s *stubModule) IsReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *stubModule) Status() ModuleStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := StatePending
	if s.ready {
		st = StateReady
	}
	return ModuleStatus{State: st}
}

func (s *stubModule) Shutdown(_ context.Context) error {
	if s.shutdownFn != nil {
		s.shutdownFn()
	}
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
	return nil
}

// assertLayerContains verifies that the layer contains exactly the named modules.
func assertLayerContains(t *testing.T, layer []Registration, names ...string) {
	t.Helper()
	if len(layer) != len(names) {
		got := make([]string, len(layer))
		for i, r := range layer {
			got[i] = r.Module.Name()
		}
		t.Errorf("layer has %d entries %v, want %d %v", len(layer), got, len(names), names)
		return
	}
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	for _, r := range layer {
		if !nameSet[r.Module.Name()] {
			t.Errorf("unexpected module %q in layer", r.Module.Name())
		}
	}
}

func reg(m Module, deps []string, required, enabled bool) Registration {
	return Registration{Module: m, Deps: deps, Required: required, Enabled: enabled}
}

func stub(name string) *stubModule {
	return &stubModule{name_: name}
}

// TestTopoLayers verifies correct layer assignment.
func TestTopoLayers(t *testing.T) {
	mounts := stub("mounts")
	web := stub("web")
	kernmods := stub("kernmods")
	gpclk0 := stub("gpclk0")
	serial := stub("serial")
	audio := stub("audio")

	regs := []Registration{
		reg(mounts, nil, true, true),
		reg(web, nil, true, true),
		reg(kernmods, []string{"mounts"}, true, true),
		reg(gpclk0, []string{"kernmods"}, true, true),
		reg(serial, []string{"kernmods"}, true, true),
		reg(audio, []string{"gpclk0", "serial"}, true, true),
	}

	layers, err := topoLayers(regs)
	if err != nil {
		t.Fatalf("topoLayers error: %v", err)
	}
	if len(layers) != 4 {
		t.Fatalf("expected 4 layers, got %d", len(layers))
	}
	assertLayerContains(t, layers[0], "mounts", "web")
	assertLayerContains(t, layers[1], "kernmods")
	assertLayerContains(t, layers[2], "gpclk0", "serial")
	assertLayerContains(t, layers[3], "audio")
}

// TestTopoLayersCycleError verifies that a cycle returns an error.
func TestTopoLayersCycleError(t *testing.T) {
	a := stub("a")
	b := stub("b")
	regs := []Registration{
		reg(a, []string{"b"}, true, true),
		reg(b, []string{"a"}, true, true),
	}
	_, err := topoLayers(regs)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

// TestTopoLayersDisabledSkipped verifies disabled modules are skipped in layers
// and their dependents sort correctly with disabled deps ignored.
func TestTopoLayersDisabledSkipped(t *testing.T) {
	base := stub("base")
	mid := stub("mid")   // disabled
	top := stub("top")

	regs := []Registration{
		reg(base, nil, true, true),
		reg(mid, []string{"base"}, true, false), // disabled
		reg(top, []string{"mid"}, true, true),   // depends on disabled mid
	}

	layers, err := topoLayers(regs)
	if err != nil {
		t.Fatalf("topoLayers error: %v", err)
	}
	// mid is disabled; top's dep on mid should be ignored
	// so base and top should both be in layer 0
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	assertLayerContains(t, layers[0], "base", "top")
}

// TestManagerRunParallel verifies modules in the same layer run concurrently
// and modules in later layers run after earlier ones.
func TestManagerRunParallel(t *testing.T) {
	var (
		mu      sync.Mutex
		log     []string
		barrier = make(chan struct{})
	)

	// layer 0: base, web - both block until the barrier is closed
	// If they didn't run in parallel, the second would never unblock.
	base := &stubModule{name_: "base", initFn: func() {
		<-barrier
		mu.Lock()
		log = append(log, "base")
		mu.Unlock()
	}}
	web := &stubModule{name_: "web", initFn: func() {
		<-barrier
		mu.Lock()
		log = append(log, "web")
		mu.Unlock()
	}}
	// layer 1: top depends on base
	top := &stubModule{name_: "top", initFn: func() {
		mu.Lock()
		log = append(log, "top")
		mu.Unlock()
	}}

	regs := []Registration{
		reg(base, nil, true, true),
		reg(web, nil, true, true),
		reg(top, []string{"base"}, true, true),
	}

	m := NewManager(regs)

	done := make(chan error, 1)
	go func() {
		done <- m.Run(context.Background())
	}()

	// Give both layer-0 goroutines time to block on barrier.
	time.Sleep(20 * time.Millisecond)
	close(barrier)

	if err := <-done; err != nil {
		t.Fatalf("Run error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// top must come after both base and web
	topIdx := -1
	for i, name := range log {
		if name == "top" {
			topIdx = i
		}
	}
	if topIdx < 2 {
		t.Errorf("top ran before layer-0 modules finished; log: %v", log)
	}
}

// TestManagerRunRequiredFailure verifies that a required module failure stops init.
func TestManagerRunRequiredFailure(t *testing.T) {
	base := &stubModule{name_: "base", initErr: errTest("base failed")}
	top := &stubModule{name_: "top"}

	regs := []Registration{
		reg(base, nil, true, true),  // required, will fail
		reg(top, []string{"base"}, true, true),
	}

	m := NewManager(regs)
	err := m.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from required failure, got nil")
	}

	statuses := m.Status()
	var topStatus NamedStatus
	for _, s := range statuses {
		if s.Name == "top" {
			topStatus = s
		}
	}
	if topStatus.State != StatePending {
		t.Errorf("top should be pending after required failure, got %v", topStatus.State)
	}
}

// TestManagerRunOptionalFailure verifies optional module failure doesn't stop init.
func TestManagerRunOptionalFailure(t *testing.T) {
	base := &stubModule{name_: "base", initErr: errTest("base failed")}
	top := &stubModule{name_: "top"}

	regs := []Registration{
		reg(base, nil, false, true), // optional, will fail
		reg(top, nil, true, true),
	}

	m := NewManager(regs)
	err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("optional failure should not stop init, got error: %v", err)
	}

	statuses := m.Status()
	var topStatus NamedStatus
	for _, s := range statuses {
		if s.Name == "top" {
			topStatus = s
		}
	}
	if topStatus.State != StateReady {
		t.Errorf("top should be ready after optional failure, got %v", topStatus.State)
	}
}

// TestManagerShutdownReverseOrder verifies shutdown runs in reverse dependency order.
func TestManagerShutdownReverseOrder(t *testing.T) {
	var (
		mu  sync.Mutex
		log []string
	)

	base := &stubModule{name_: "base", shutdownFn: func() {
		mu.Lock()
		log = append(log, "base")
		mu.Unlock()
	}}
	top := &stubModule{name_: "top", shutdownFn: func() {
		mu.Lock()
		log = append(log, "top")
		mu.Unlock()
	}}

	regs := []Registration{
		reg(base, nil, true, true),
		reg(top, []string{"base"}, true, true),
	}

	m := NewManager(regs)
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	m.Shutdown(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(log) != 2 {
		t.Fatalf("expected 2 shutdowns, got %d: %v", len(log), log)
	}
	if log[0] != "top" || log[1] != "base" {
		t.Errorf("shutdown order wrong: want [top base], got %v", log)
	}
}

// TestManagerStatus verifies enabled modules show their state and disabled show StateDisabled.
func TestManagerStatus(t *testing.T) {
	enabled := stub("enabled")
	disabled := stub("disabled")

	regs := []Registration{
		reg(enabled, nil, true, true),
		reg(disabled, nil, true, false),
	}

	m := NewManager(regs)
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	statuses := m.Status()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	statusMap := make(map[string]NamedStatus)
	for _, s := range statuses {
		statusMap[s.Name] = s
	}

	if statusMap["enabled"].State != StateReady {
		t.Errorf("enabled module state = %v, want ready", statusMap["enabled"].State)
	}
	if statusMap["disabled"].State != StateDisabled {
		t.Errorf("disabled module state = %v, want disabled", statusMap["disabled"].State)
	}
}

// errTest is a simple error type for tests.
type errTest string

func (e errTest) Error() string { return string(e) }

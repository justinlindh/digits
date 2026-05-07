package subsystem

import "testing"

func TestMountsModuleName(t *testing.T) {
	m := NewMountsModule()
	if m.Name() != "mounts" {
		t.Errorf("got %q, want mounts", m.Name())
	}
}

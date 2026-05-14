package subsystem

import "testing"

func TestModuleStatusString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StatePending, "pending"},
		{StateInitializing, "initializing"},
		{StateReady, "ready"},
		{StateFailed, "failed"},
		{StateDegraded, "degraded"},
		{StateDisabled, "disabled"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

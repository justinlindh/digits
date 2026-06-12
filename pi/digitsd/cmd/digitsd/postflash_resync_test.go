package main

import (
	"reflect"
	"testing"
)

// fakePicoResyncer records the commands resyncPicoState emits so tests can
// assert the exact serial sequence without a real *phone.SerialPort.
type fakePicoResyncer struct {
	cmds []string
}

func (f *fakePicoResyncer) StopRing()             { f.cmds = append(f.cmds, "STOP_RING") }
func (f *fakePicoResyncer) LED(mode string)       { f.cmds = append(f.cmds, "LED:"+mode) }
func (f *fakePicoResyncer) StateSet(state string) { f.cmds = append(f.cmds, "STATE:SET:"+state) }

func TestPicoStateForToken(t *testing.T) {
	tests := []struct {
		name        string
		deviceToken string
		want        string
	}{
		{name: "empty token is unpaired", deviceToken: "", want: "UNPAIRED"},
		{name: "non-empty token is paired", deviceToken: "tok-abc123", want: "PAIRED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := picoStateForToken(tc.deviceToken); got != tc.want {
				t.Fatalf("picoStateForToken(%q) = %q, want %q", tc.deviceToken, got, tc.want)
			}
		})
	}
}

// TestResyncPicoState verifies the post-flash resync emits the same hardware
// reset plus STATE:SET sequence the startup path does, deriving the persisted
// phase from the live device token. This is the behavior missing on the runtime
// firmware-flash path that left the fleet breathing at idle.
func TestResyncPicoState(t *testing.T) {
	tests := []struct {
		name        string
		deviceToken string
		want        []string
	}{
		{
			name:        "paired device resyncs to PAIRED",
			deviceToken: "tok-abc123",
			want:        []string{"STOP_RING", "LED:UNLOCK", "STATE:SET:PAIRED"},
		},
		{
			name:        "unpaired device resyncs to UNPAIRED",
			deviceToken: "",
			want:        []string{"STOP_RING", "LED:UNLOCK", "STATE:SET:UNPAIRED"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakePicoResyncer{}
			resyncPicoState(f, tc.deviceToken)
			if !reflect.DeepEqual(f.cmds, tc.want) {
				t.Fatalf("resyncPicoState commands = %v, want %v", f.cmds, tc.want)
			}
		})
	}
}

// TestResetPicoHardware locks in that the hardware reset clears ring and LED and
// never touches the persisted phase, so resyncPicoState is the only path that
// emits STATE:SET.
func TestResetPicoHardware(t *testing.T) {
	f := &fakePicoResyncer{}
	resetPicoHardware(f)
	want := []string{"STOP_RING", "LED:UNLOCK"}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Fatalf("resetPicoHardware commands = %v, want %v", f.cmds, want)
	}
}

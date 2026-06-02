package main

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestConnStateAction(t *testing.T) {
	cases := []struct {
		name            string
		state           webrtc.PeerConnectionState
		recovering      bool
		debouncePending bool
		want            connAction
	}{
		{"connected clears", webrtc.PeerConnectionStateConnected, true, true, actionClearRecovery},
		{"disconnected fresh starts debounce", webrtc.PeerConnectionStateDisconnected, false, false, actionStartDebounce},
		{"disconnected while debouncing is noop", webrtc.PeerConnectionStateDisconnected, false, true, actionNone},
		{"disconnected while recovering is noop", webrtc.PeerConnectionStateDisconnected, true, false, actionNone},
		{"failed fresh enters recovery", webrtc.PeerConnectionStateFailed, false, false, actionEnterRecovery},
		{"failed while recovering is noop", webrtc.PeerConnectionStateFailed, true, false, actionNone},
		{"connecting is noop", webrtc.PeerConnectionStateConnecting, false, false, actionNone},
		{"closed is noop", webrtc.PeerConnectionStateClosed, false, false, actionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := connStateAction(tc.state, tc.recovering, tc.debouncePending)
			if got != tc.want {
				t.Fatalf("connStateAction(%v, recovering=%v, debounce=%v) = %v, want %v",
					tc.state, tc.recovering, tc.debouncePending, got, tc.want)
			}
		})
	}
}

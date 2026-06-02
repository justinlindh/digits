package main

import (
	"testing"

	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
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

func TestReconnectAction(t *testing.T) {
	cases := []struct {
		name      string
		ctrlState phone.State
		hasMesh   bool
		hasPeer   bool
		connState webrtc.PeerConnectionState
		want      reconnAction
	}{
		{"connected 2party media-up resumes noop", phone.StateCONNECTED, false, true, webrtc.PeerConnectionStateConnected, reconnResumeNoop},
		{"connected 2party media-down restarts", phone.StateCONNECTED, false, true, webrtc.PeerConnectionStateDisconnected, reconnResumeRestart},
		{"connected 2party media-failed restarts", phone.StateCONNECTED, false, true, webrtc.PeerConnectionStateFailed, reconnResumeRestart},
		{"conference tears down", phone.StateCONFERENCE_MERGED, true, true, webrtc.PeerConnectionStateConnected, reconnTeardown},
		{"no peer tears down", phone.StateCONNECTED, false, false, webrtc.PeerConnectionStateConnected, reconnTeardown},
		{"ringing tears down", phone.StateRINGING, false, true, webrtc.PeerConnectionStateConnected, reconnTeardown},
		{"voicemail recording tears down", phone.StateVOICEMAIL_RECORDING, false, true, webrtc.PeerConnectionStateConnected, reconnTeardown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reconnectAction(tc.ctrlState, tc.hasMesh, tc.hasPeer, tc.connState)
			if got != tc.want {
				t.Fatalf("reconnectAction(%v, mesh=%v, peer=%v, %v) = %v, want %v",
					tc.ctrlState, tc.hasMesh, tc.hasPeer, tc.connState, got, tc.want)
			}
		})
	}
}

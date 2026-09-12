package phone

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/signal"
)

// connectedController returns a controller in CONNECTED via the normal
// outgoing flow (off-hook, dial, answer) so the failure paths under test
// start from real call state rather than a forced one.
func connectedController(t *testing.T, cb *mockCallbacks) *Controller {
	t.Helper()
	c := newTestController(cb, "")
	c.HandleEvent("HOOK:OFF")
	c.HandleEvent("KEY:3")
	c.HandleEvent("DIAL:3140002")
	waitForCall(cb)
	c.HandleSignal("answer", "3140002")
	if c.State() != StateCONNECTED {
		t.Fatalf("setup: expected CONNECTED, got %s", c.State())
	}
	return c
}

func (m *mockCallbacks) HangupReasons() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.hangupReasons...)
}

// A locally detected connect timeout tears the call down with the
// connect_timeout reason and runs the announced off-hook sequence.
func TestController_ConnectFailed_LocalTimeout(t *testing.T) {
	cb := &mockCallbacks{}
	c := connectedController(t, cb)
	defer c.Close()

	c.HandleSignal("connect_failed", "3140002")

	if c.State() != StateCALL_FAILED {
		t.Fatalf("expected CALL_FAILED, got %s", c.State())
	}
	if got := cb.HangupReasons(); len(got) != 1 || got[0] != signal.HangupReasonConnectTimeout {
		t.Fatalf("HangupCall reasons = %v, want [%s]", got, signal.HangupReasonConnectTimeout)
	}
	if !waitForTone(cb, ToneReorder) {
		t.Fatalf("reorder never played; tones=%v", cb.Tones())
	}
	tones := cb.Tones()
	announce, reorder := -1, -1
	for i, tone := range tones {
		switch tone {
		case ToneCallFailed:
			if announce < 0 {
				announce = i
			}
		case ToneReorder:
			if reorder < 0 {
				reorder = i
			}
		}
	}
	if announce < 0 || reorder < announce {
		t.Fatalf("expected CALL_FAILED announcement before reorder, tones=%v", tones)
	}
}

// A remote hangup carrying connect_timeout (dispatched as connect_failed)
// runs the same treatment; the plain remote hangup path stays announcement
// free and carries no reason.
func TestController_ConnectFailed_VersusPlainRemoteHangup(t *testing.T) {
	cb := &mockCallbacks{}
	c := connectedController(t, cb)
	defer c.Close()

	c.HandleSignal("hangup", "3140002")

	if c.State() != StateREMOTE_HANGUP {
		t.Fatalf("expected REMOTE_HANGUP, got %s", c.State())
	}
	if got := cb.HangupReasons(); len(got) != 1 || got[0] != "" {
		t.Fatalf("HangupCall reasons = %v, want [\"\"]", got)
	}
	if !waitForTone(cb, ToneReorder) {
		t.Fatalf("reorder never played; tones=%v", cb.Tones())
	}
	for _, tone := range cb.Tones() {
		if tone == ToneCallFailed {
			t.Fatalf("plain remote hangup played the failure announcement; tones=%v", cb.Tones())
		}
	}
}

// The off-hook sequence waits for the announcement to finish before reorder
// starts, so the spoken clip is never talked over.
func TestController_ConnectFailed_WaitsForAnnouncement(t *testing.T) {
	var polls atomic.Int32
	cb := &mockCallbacks{}
	cb.oncePlaying = func() bool { return polls.Add(1) <= 5 }
	c := connectedController(t, cb)
	defer c.Close()

	c.HandleSignal("connect_failed", "3140002")

	if !waitForTone(cb, ToneReorder) {
		t.Fatalf("reorder never played; tones=%v", cb.Tones())
	}
	if polls.Load() < 6 {
		t.Fatalf("reorder started after %d OncePlaying polls, want the announcement polled to completion", polls.Load())
	}
}

// Hanging up during the announcement aborts the sequence: no reorder, no
// second hangup, back to IDLE.
func TestController_ConnectFailed_HookOnAborts(t *testing.T) {
	cb := &mockCallbacks{}
	cb.oncePlaying = func() bool { return true }
	c := connectedController(t, cb)
	defer c.Close()

	c.HandleSignal("connect_failed", "3140002")
	if !waitForTone(cb, ToneCallFailed) {
		t.Fatalf("announcement never played; tones=%v", cb.Tones())
	}
	c.HandleEvent("HOOK:ON")

	if c.State() != StateIDLE {
		t.Fatalf("expected IDLE after hook-on, got %s", c.State())
	}
	time.Sleep(20 * time.Millisecond)
	for _, tone := range cb.Tones() {
		if tone == ToneReorder {
			t.Fatalf("reorder played after hook-on; tones=%v", cb.Tones())
		}
	}
	if got := cb.HangupReasons(); len(got) != 1 {
		t.Fatalf("expected exactly one HangupCall, got %v", got)
	}
}

func TestController_ConnectFailed_IgnoredOutsideConnected(t *testing.T) {
	for _, st := range []State{StateIDLE, StateCALLING, StateRINGING, StateREMOTE_HANGUP} {
		cb := &mockCallbacks{}
		c := newTestController(cb, "")
		c.setStateForTest(st)
		c.HandleSignal("connect_failed", "3140002")
		if c.State() != st {
			t.Errorf("state %s: connect_failed moved to %s", st, c.State())
		}
		if got := cb.HangupReasons(); len(got) != 0 {
			t.Errorf("state %s: connect_failed hung up %v", st, got)
		}
		c.Close()
	}
}

// The voicemail greeting goroutine giving up on an unconnected peer reports
// the same reason so the caller gets the failure announcement too.
func TestController_AbortVoicemailGreeting_ReportsConnectTimeout(t *testing.T) {
	cb := &mockCallbacks{
		voicemailEnabled: func() (bool, time.Duration) {
			return true, 20 * time.Millisecond
		},
	}
	c := newTestController(cb, "")
	defer c.Close()

	c.HandleSignal("ring", "")
	waitFor(t, func() bool { return c.State() == StateVOICEMAIL_GREETING }, time.Second, "VOICEMAIL_GREETING")

	if !c.AbortVoicemailGreeting() {
		t.Fatal("expected abort to report true")
	}
	if got := cb.HangupReasons(); len(got) != 1 || got[0] != signal.HangupReasonConnectTimeout {
		t.Fatalf("HangupCall reasons = %v, want [%s]", got, signal.HangupReasonConnectTimeout)
	}
}

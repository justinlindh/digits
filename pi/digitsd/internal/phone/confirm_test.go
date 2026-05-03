package phone

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestConfirmerFireRunsAction(t *testing.T) {
	c := NewConfirmer()
	fired := make(chan struct{}, 1)
	var timedOut atomic.Bool
	if !c.Arm(func() { fired <- struct{}{} }, func() { timedOut.Store(true) }, time.Second) {
		t.Fatal("Arm returned false on fresh Confirmer")
	}
	if !c.Active() {
		t.Fatal("Active should be true after Arm")
	}
	c.Fire()
	if c.Active() {
		t.Fatal("Active should be false after Fire")
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("action never fired")
	}
	time.Sleep(50 * time.Millisecond)
	if timedOut.Load() {
		t.Fatal("onTimeout must not run when Fire wins")
	}
}

func TestConfirmerCancelClearsWithoutCallback(t *testing.T) {
	c := NewConfirmer()
	var actionCalled, timedOut atomic.Bool
	c.Arm(func() { actionCalled.Store(true) }, func() { timedOut.Store(true) }, time.Second)
	c.Cancel()
	if c.Active() {
		t.Fatal("Active should be false after Cancel")
	}
	time.Sleep(50 * time.Millisecond)
	if actionCalled.Load() {
		t.Fatal("action must not run when Cancel wins")
	}
	if timedOut.Load() {
		t.Fatal("onTimeout must not run when Cancel wins")
	}
}

func TestConfirmerTimeoutRunsOnTimeout(t *testing.T) {
	c := NewConfirmer()
	var actionCalled atomic.Bool
	timedOut := make(chan struct{}, 1)
	c.Arm(func() { actionCalled.Store(true) }, func() { timedOut <- struct{}{} }, 30*time.Millisecond)
	select {
	case <-timedOut:
	case <-time.After(time.Second):
		t.Fatal("onTimeout never fired")
	}
	if c.Active() {
		t.Fatal("Active should be false after timeout")
	}
	if actionCalled.Load() {
		t.Fatal("action must not run on timeout")
	}
}

func TestConfirmerSecondArmRefused(t *testing.T) {
	c := NewConfirmer()
	c.Arm(func() {}, func() {}, time.Second)
	if c.Arm(func() {}, func() {}, time.Second) {
		t.Fatal("second Arm should return false while one is pending")
	}
}

func TestConfirmerCancelOnIdleIsNoOp(t *testing.T) {
	c := NewConfirmer()
	c.Cancel() // must not panic
	c.Fire()   // must not panic
	if c.Active() {
		t.Fatal("Active should remain false")
	}
}

func TestConfirmerNilOnTimeoutAllowed(t *testing.T) {
	c := NewConfirmer()
	c.Arm(func() {}, nil, 30*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	if c.Active() {
		t.Fatal("Active should be false after timeout with nil onTimeout")
	}
}

func TestConfirmerFireRacesTimeout(t *testing.T) {
	// Repeatedly arm+Fire just before the timer expires; verify exactly
	// one of (action, onTimeout) runs.
	for i := 0; i < 20; i++ {
		c := NewConfirmer()
		var fired, timedOut atomic.Bool
		c.Arm(func() { fired.Store(true) }, func() { timedOut.Store(true) }, 5*time.Millisecond)
		time.Sleep(5 * time.Millisecond)
		c.Fire()
		time.Sleep(20 * time.Millisecond)
		if fired.Load() && timedOut.Load() {
			t.Fatalf("iter %d: both callbacks ran", i)
		}
	}
}

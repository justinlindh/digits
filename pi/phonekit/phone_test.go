package phonekit

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPhoneLED(t *testing.T) {
	port := newMockPort()
	p := newPhoneFromSerial(newSerial(port))
	defer func() { _ = p.Close() }()

	if err := p.LED("HEARTBEAT"); err != nil {
		t.Fatalf("LED: %v", err)
	}

	if !strings.Contains(port.sent(), "LED:HEARTBEAT\n") {
		t.Errorf("port did not receive LED:HEARTBEAT; sent=%q", port.sent())
	}
}

func TestPhoneSetPhase(t *testing.T) {
	port := newMockPort()
	p := newPhoneFromSerial(newSerial(port))
	defer func() { _ = p.Close() }()

	go func() {
		time.Sleep(20 * time.Millisecond)
		port.feed("STATE:SET:OK\n")
	}()

	if err := p.SetPhase("PAIRED"); err != nil {
		t.Fatalf("SetPhase: %v", err)
	}

	if !strings.Contains(port.sent(), "STATE:SET:PAIRED\n") {
		t.Errorf("port did not receive STATE:SET:PAIRED; sent=%q", port.sent())
	}
}

func TestPhoneSetPhaseError(t *testing.T) {
	port := newMockPort()
	p := newPhoneFromSerial(newSerial(port))
	defer func() { _ = p.Close() }()

	go func() {
		time.Sleep(20 * time.Millisecond)
		port.feed("STATE:SET:ERR:flash verify failed\n")
	}()

	err := p.SetPhase("PAIRED")
	if err == nil {
		t.Fatal("expected error from SetPhase, got nil")
	}
	if !strings.Contains(err.Error(), "flash verify failed") {
		t.Errorf("error message %q does not mention flash verify failed", err.Error())
	}
}

func TestPhonePing(t *testing.T) {
	port := newMockPort()
	p := newPhoneFromSerial(newSerial(port))
	defer func() { _ = p.Close() }()

	go func() {
		time.Sleep(20 * time.Millisecond)
		port.feed("PONG\n")
	}()

	if err := p.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPhoneEvents(t *testing.T) {
	port := newMockPort()
	p := newPhoneFromSerial(newSerial(port))
	defer func() { _ = p.Close() }()

	port.feed("KEY:1\n")

	select {
	case ev := <-p.Events():
		if ev.Type != "KEY" || ev.Value != "1" {
			t.Errorf("got event %+v, want KEY:1", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for KEY:1 event")
	}
}

func TestPhoneWaitForKey(t *testing.T) {
	port := newMockPort()
	p := newPhoneFromSerial(newSerial(port))
	defer func() { _ = p.Close() }()

	go func() {
		time.Sleep(20 * time.Millisecond)
		port.feed("KEY:5\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	key, err := p.WaitForKey(ctx)
	if err != nil {
		t.Fatalf("WaitForKey: %v", err)
	}
	if key != "5" {
		t.Errorf("got key %q, want %q", key, "5")
	}
}

func TestPhoneWaitForHook(t *testing.T) {
	port := newMockPort()
	p := newPhoneFromSerial(newSerial(port))
	defer func() { _ = p.Close() }()

	go func() {
		time.Sleep(20 * time.Millisecond)
		port.feed("HOOK:OFF\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := p.WaitForHook(ctx, "OFF"); err != nil {
		t.Fatalf("WaitForHook: %v", err)
	}
}

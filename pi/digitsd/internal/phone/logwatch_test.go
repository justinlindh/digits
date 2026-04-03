package phone

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func writeLine(f *os.File, ts, dir, event string) error {
	_, err := fmt.Fprintf(f, "%s | %s | %s\n", ts, dir, event)
	if err != nil {
		return err
	}
	return f.Sync()
}

func TestLogWatcher(t *testing.T) {
	f, err := os.CreateTemp("", "uart-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	w, err := NewLogWatcher(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	ts := "2026-03-22 20:00:00"
	if err := writeLine(f, ts, "RX", "HOOK:OFF"); err != nil {
		t.Fatal(err)
	}
	if err := writeLine(f, ts, "RX", "KEY:5"); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(3 * time.Second)
	got := make([]string, 0, 2)
	for len(got) < 2 {
		select {
		case ev := <-w.Events():
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("timeout waiting for events; got %v", got)
		}
	}

	if got[0] != "HOOK:OFF" {
		t.Errorf("expected HOOK:OFF, got %q", got[0])
	}
	if got[1] != "KEY:5" {
		t.Errorf("expected KEY:5, got %q", got[1])
	}
}

func TestLogWatcher_SkipsTX(t *testing.T) {
	f, err := os.CreateTemp("", "uart-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	w, err := NewLogWatcher(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	ts := "2026-03-22 20:00:00"
	if err := writeLine(f, ts, "TX", "RING:ON"); err != nil {
		t.Fatal(err)
	}
	if err := writeLine(f, ts, "RX", "HOOK:ON"); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(3 * time.Second)
	select {
	case ev := <-w.Events():
		if ev != "HOOK:ON" {
			t.Errorf("expected HOOK:ON, got %q", ev)
		}
	case <-timeout:
		t.Fatal("timeout waiting for RX event")
	}

	// Ensure no extra events (TX should have been skipped).
	select {
	case extra := <-w.Events():
		t.Errorf("unexpected extra event: %q", extra)
	case <-time.After(200 * time.Millisecond):
		// Good — no extra events.
	}
}

func writeRealLine(f *os.File, ts, dir, event string) error {
	_, err := fmt.Fprintf(f, "%s %s: %s\n", ts, dir, event)
	if err != nil {
		return err
	}
	return f.Sync()
}

func TestLogWatcher_RealFormat(t *testing.T) {
	f, err := os.CreateTemp("", "uart-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	w, err := NewLogWatcher(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	ts := "2026-03-22 22:16:53"
	if err := writeRealLine(f, ts, "TX", "RING:START"); err != nil {
		t.Fatal(err)
	}
	if err := writeRealLine(f, ts, "RX", "RING:ACK"); err != nil {
		t.Fatal(err)
	}
	if err := writeRealLine(f, ts, "RX", "HOOK:OFF"); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(3 * time.Second)
	got := make([]string, 0, 2)
	for len(got) < 2 {
		select {
		case ev := <-w.Events():
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("timeout waiting for events; got %v", got)
		}
	}

	if got[0] != "RING:ACK" {
		t.Errorf("expected RING:ACK, got %q", got[0])
	}
	if got[1] != "HOOK:OFF" {
		t.Errorf("expected HOOK:OFF, got %q", got[1])
	}
}

func TestLogWatcher_Stop(t *testing.T) {
	f, err := os.CreateTemp("", "uart-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	w, err := NewLogWatcher(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	w.Stop()

	// Give goroutine time to exit.
	time.Sleep(200 * time.Millisecond)

	// Drain events channel — should not panic.
	for {
		select {
		case <-w.Events():
		default:
			return
		}
	}
}

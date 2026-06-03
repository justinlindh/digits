package subsystem

import (
	"errors"
	"testing"
)

// probeAfter returns a probe func that fails the first n calls then succeeds,
// recording the total number of calls.
func probeAfter(n int, calls *int) func() bool {
	return func() bool {
		*calls++
		return *calls > n
	}
}

func TestAttemptSerialBringup_SucceedsWithoutFlash(t *testing.T) {
	calls := 0
	flashed := 0
	flash := func(string) error { flashed++; return nil }

	ok := attemptSerialBringup(probeAfter(0, &calls), flash, 3, 10, func() {})

	if !ok {
		t.Fatal("expected bringup to succeed on first probe")
	}
	if flashed != 0 {
		t.Fatalf("flash should not run when probe succeeds, got %d calls", flashed)
	}
	if calls != 1 {
		t.Fatalf("expected 1 probe, got %d", calls)
	}
}

func TestAttemptSerialBringup_FlashesThenSucceeds(t *testing.T) {
	calls := 0
	flashed := 0
	// Fail every probe until after the flash runs, then succeed.
	probe := func() bool {
		calls++
		return flashed > 0
	}
	flash := func(reason string) error {
		if reason != "serial-init" {
			t.Errorf("unexpected flash reason %q", reason)
		}
		flashed++
		return nil
	}

	ok := attemptSerialBringup(probe, flash, 3, 10, func() {})

	if !ok {
		t.Fatal("expected bringup to succeed after flash")
	}
	if flashed != 1 {
		t.Fatalf("expected exactly 1 flash, got %d", flashed)
	}
	// 3 failed pre-flash probes + at least one successful post-flash probe.
	if calls < 4 {
		t.Fatalf("expected probes before and after flash, got %d", calls)
	}
}

func TestAttemptSerialBringup_NoFlashConfigured(t *testing.T) {
	calls := 0
	ok := attemptSerialBringup(probeAfter(100, &calls), nil, 10, 10, func() {})

	if ok {
		t.Fatal("expected bringup to fail when probe never succeeds and no flash")
	}
	if calls != 10 {
		t.Fatalf("expected 10 probes (no post-flash retries), got %d", calls)
	}
}

func TestAttemptSerialBringup_FlashErrorAborts(t *testing.T) {
	calls := 0
	flashed := 0
	flash := func(string) error { flashed++; return errors.New("no firmware") }

	ok := attemptSerialBringup(probeAfter(100, &calls), flash, 3, 10, func() {})

	if ok {
		t.Fatal("expected bringup to fail when flash errors")
	}
	if flashed != 1 {
		t.Fatalf("expected 1 flash attempt, got %d", flashed)
	}
	// Only the 3 pre-flash probes; no post-flash retries after a flash error.
	if calls != 3 {
		t.Fatalf("expected 3 probes (no post-flash retries), got %d", calls)
	}
}

func TestAttemptSerialBringup_FlashSucceedsButStillUnreachable(t *testing.T) {
	calls := 0
	flashed := 0
	flash := func(string) error { flashed++; return nil }

	ok := attemptSerialBringup(probeAfter(100, &calls), flash, 3, 10, func() {})

	if ok {
		t.Fatal("expected bringup to fail when Pico stays unreachable after flash")
	}
	if flashed != 1 {
		t.Fatalf("expected 1 flash attempt, got %d", flashed)
	}
	// 3 pre-flash + 10 post-flash probes.
	if calls != 13 {
		t.Fatalf("expected 13 probes, got %d", calls)
	}
}

package phone

import (
	"testing"
	"time"
)

// waitForClip receives the next clip name played from the easter-egg detector
// (which fires its callback in a goroutine), or fails the test on timeout.
func waitForClip(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case clip := <-ch:
		return clip
	case <-time.After(time.Second):
		t.Fatal("easter-egg callback never fired")
		return ""
	}
}

func TestEasterEggFunkyTown(t *testing.T) {
	played := make(chan string, 1)
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
	}, func(clip string) { played <- clip })
	d.MinGap = 0

	for _, k := range "554" {
		if d.AddKey(string(k)) {
			t.Fatal("triggered too early")
		}
	}
	if !d.AddKey("2") {
		t.Error("5542 should trigger")
	}
	if got := waitForClip(t, played); got != "funkytown" {
		t.Errorf("expected 'funkytown', got %q", got)
	}
}

func TestEasterEggRickRoll(t *testing.T) {
	played := make(chan string, 1)
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) { played <- clip })
	d.MinGap = 0

	for _, k := range "000" {
		d.AddKey(string(k))
	}
	if !d.AddKey("0") {
		t.Error("0000 should trigger")
	}
	if got := waitForClip(t, played); got != "rickroll" {
		t.Errorf("expected 'rickroll', got %q", got)
	}
}

func TestEasterEggMultiple(t *testing.T) {
	played := make(chan string, 2)
	eggs := []EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}
	d := NewEasterEggDetector(eggs, func(clip string) { played <- clip })
	d.MinGap = 0

	// Trigger funky town
	for _, k := range "5542" {
		d.AddKey(string(k))
	}
	if got := waitForClip(t, played); got != "funkytown" {
		t.Errorf("expected 'funkytown', got %q", got)
	}

	// Then trigger rick roll
	for _, k := range "0000" {
		d.AddKey(string(k))
	}
	if got := waitForClip(t, played); got != "rickroll" {
		t.Errorf("expected 'rickroll', got %q", got)
	}
}

func TestEasterEggTooSlow(t *testing.T) {
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
	}, func(clip string) { t.Error("should not trigger") })
	d.MaxGap = 50 * time.Millisecond

	d.AddKey("5")
	d.AddKey("5")
	d.AddKey("4")
	time.Sleep(100 * time.Millisecond)
	if d.AddKey("2") {
		t.Error("should not trigger with slow timing")
	}
}

func TestEasterEggReset(t *testing.T) {
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
	}, func(clip string) { t.Error("should not trigger after reset") })

	d.AddKey("5")
	d.AddKey("5")
	d.AddKey("4")
	d.Reset()
	if d.AddKey("2") {
		t.Error("should not trigger after reset")
	}
}

func TestEasterEggNoFalsePositive(t *testing.T) {
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) { t.Errorf("false positive: %s", clip) })
	d.MinGap = 0

	for _, k := range "1234567890123" {
		if d.AddKey(string(k)) {
			t.Errorf("false positive on %c", k)
		}
	}
}

func TestEasterEggAfterOtherDigits(t *testing.T) {
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
	}, func(clip string) { t.Errorf("should not trigger after prefix digits, got %s", clip) })
	d.MinGap = 0

	for _, k := range "123" {
		d.AddKey(string(k))
	}
	for _, k := range "5542" {
		if d.AddKey(string(k)) {
			t.Error("should not trigger when other digits were pressed first")
		}
	}
}

func TestEasterEggSuffixNoFalsePositive(t *testing.T) {
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) { t.Errorf("should not trigger on suffix match, got %s", clip) })
	d.MinGap = 0

	for _, k := range "5550000" {
		if d.AddKey(string(k)) {
			t.Error("5550000 should not trigger 0000 easter egg")
		}
	}
}

func TestEasterEggResetClearsTaint(t *testing.T) {
	played := make(chan string, 1)
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) { played <- clip })
	d.MinGap = 0

	for _, k := range "555" {
		d.AddKey(string(k))
	}
	d.Reset()
	for _, k := range "000" {
		d.AddKey(string(k))
	}
	if !d.AddKey("0") {
		t.Error("0000 should trigger after Reset clears taint")
	}
	if got := waitForClip(t, played); got != "rickroll" {
		t.Errorf("expected 'rickroll', got %q", got)
	}
}

func TestDialEasterEggJenny(t *testing.T) {
	egg, ok := DialEasterEggs["8675309"]
	if !ok {
		t.Fatal("8675309 should be in DialEasterEggs")
	}
	if egg.Clip != "jenny" {
		t.Errorf("expected clip 'jenny', got %q", egg.Clip)
	}
}

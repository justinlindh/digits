package phone

import (
	"testing"
	"time"
)

func TestEasterEggFunkyTown(t *testing.T) {
	var played string
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
	}, func(clip string) { played = clip })
	d.MinGap = 0

	for _, k := range "554" {
		if d.AddKey(string(k)) {
			t.Fatal("triggered too early")
		}
	}
	if !d.AddKey("2") {
		t.Error("5542 should trigger")
	}
	time.Sleep(10 * time.Millisecond)
	if played != "funkytown" {
		t.Errorf("expected 'funkytown', got %q", played)
	}
}

func TestEasterEggRickRoll(t *testing.T) {
	var played string
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) { played = clip })
	d.MinGap = 0

	for _, k := range "000" {
		d.AddKey(string(k))
	}
	if !d.AddKey("0") {
		t.Error("0000 should trigger")
	}
	time.Sleep(10 * time.Millisecond)
	if played != "rickroll" {
		t.Errorf("expected 'rickroll', got %q", played)
	}
}

func TestEasterEggMultiple(t *testing.T) {
	var played string
	eggs := []EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}
	d := NewEasterEggDetector(eggs, func(clip string) { played = clip })
	d.MinGap = 0

	// Trigger funky town
	for _, k := range "5542" {
		d.AddKey(string(k))
	}
	time.Sleep(10 * time.Millisecond)
	if played != "funkytown" {
		t.Errorf("expected 'funkytown', got %q", played)
	}

	// Then trigger rick roll
	played = ""
	for _, k := range "0000" {
		d.AddKey(string(k))
	}
	time.Sleep(10 * time.Millisecond)
	if played != "rickroll" {
		t.Errorf("expected 'rickroll', got %q", played)
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
	var played string
	d := NewEasterEggDetector([]EasterEgg{
		{Name: "Funky Town", Trigger: "5542", Clip: "funkytown"},
	}, func(clip string) { played = clip })
	d.MinGap = 0

	for _, k := range "123" {
		d.AddKey(string(k))
	}
	for _, k := range "5542" {
		d.AddKey(string(k))
	}
	time.Sleep(10 * time.Millisecond)
	if played != "funkytown" {
		t.Errorf("expected 'funkytown' after prefix, got %q", played)
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

package phone

import (
	"testing"
	"time"
)

func TestServiceCodeShutdown(t *testing.T) {
	// Don't set callbacks — we're just testing detection, not actual shutdown
	h := NewServiceCodeHandler()
	for _, k := range "*#*" {
		if h.AddKey(string(k)) != ServiceCodeNone {
			t.Fatal("triggered too early")
		}
	}
	if got := h.AddKey("#"); got != ServiceCodeTerminal {
		t.Errorf("*#*# should trigger as terminal, got %v", got)
	}
}

func TestServiceCodeReboot(t *testing.T) {
	h := NewServiceCodeHandler()
	for _, k := range "*##" {
		h.AddKey(string(k))
	}
	if got := h.AddKey("*"); got != ServiceCodeTerminal {
		t.Errorf("*##* should trigger as terminal, got %v", got)
	}
}

func TestServiceCodeVolume(t *testing.T) {
	var gotLevel int
	h := NewServiceCodeHandler()
	h.SetVolumeCallback(func(level int) { gotLevel = level })

	for _, k := range "*#*" {
		h.AddKey(string(k))
	}
	if got := h.AddKey("5"); got != ServiceCodeNonTerminal {
		t.Errorf("*#*5 should trigger as non-terminal, got %v", got)
	}
	if gotLevel != 5 {
		t.Errorf("expected level 5, got %d", gotLevel)
	}
}

func TestServiceCodeAudioTest(t *testing.T) {
	h := NewServiceCodeHandler()
	h.SetAudioTestCallback(func() {})

	code := "*#8378#"
	var triggered bool
	for i, k := range code {
		result := h.AddKey(string(k))
		if result != ServiceCodeNone && i < len(code)-1 {
			t.Fatalf("triggered too early at index %d", i)
		}
		if result != ServiceCodeNone {
			if result != ServiceCodeNonTerminal {
				t.Errorf("audio test should be non-terminal, got %v", result)
			}
			triggered = true
		}
	}
	if !triggered {
		t.Error("*#8378# (*#TEST#) should trigger audio test")
	}
}

func TestServiceCodeVolume8Works(t *testing.T) {
	var gotLevel int
	h := NewServiceCodeHandler()
	h.SetVolumeCallback(func(level int) { gotLevel = level })

	for _, k := range "*#*" {
		h.AddKey(string(k))
	}
	if got := h.AddKey("8"); got != ServiceCodeNonTerminal {
		t.Errorf("*#*8 should trigger volume as non-terminal, got %v", got)
	}
	if gotLevel != 8 {
		t.Errorf("expected level 8, got %d", gotLevel)
	}
}

func TestServiceCodeNoMatch(t *testing.T) {
	h := NewServiceCodeHandler()
	for _, k := range "1234" {
		if h.AddKey(string(k)) != ServiceCodeNone {
			t.Error("1234 should not trigger")
		}
	}
}

func TestServiceCodeIncomplete(t *testing.T) {
	h := NewServiceCodeHandler()
	for _, k := range "*#*" {
		if h.AddKey(string(k)) != ServiceCodeNone {
			t.Error("incomplete code should not trigger")
		}
	}
}

func TestServiceCodeAtEnd(t *testing.T) {
	h := NewServiceCodeHandler()
	// Type some digits first, then a service code
	for _, k := range "12" {
		h.AddKey(string(k))
	}
	for _, k := range "*#*" {
		h.AddKey(string(k))
	}
	if got := h.AddKey("#"); got != ServiceCodeTerminal {
		t.Errorf("*#*# at end of buffer should trigger as terminal, got %v", got)
	}
}

func TestServiceCodeReset(t *testing.T) {
	h := NewServiceCodeHandler()
	h.AddKey("*")
	h.AddKey("#")
	h.AddKey("*")
	h.Reset()
	// After reset, adding "#" should NOT complete *#*#
	if h.AddKey("#") != ServiceCodeNone {
		t.Error("should not trigger after reset")
	}
}

// TestServiceCodeSetup verifies that *#73887# triggers the setup callback.
func TestServiceCodeSetup(t *testing.T) {
	called := make(chan struct{}, 1)
	h := NewServiceCodeHandler()
	h.SetSetupCallback(func() { called <- struct{}{} })

	code := "*#73887#"
	var triggered bool
	for i, k := range code {
		result := h.AddKey(string(k))
		if result != ServiceCodeNone && i < len(code)-1 {
			t.Fatalf("triggered too early at index %d", i)
		}
		if result != ServiceCodeNone {
			if result != ServiceCodeTerminal {
				t.Errorf("setup should be terminal, got %v", result)
			}
			triggered = true
		}
	}
	if !triggered {
		t.Error("*#73887# should trigger setup code")
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Error("setup callback never fired")
	}
}

// TestServiceCodeSetupNotTriggeredByPrefix verifies that partial sequences
// don't fire.
func TestServiceCodeSetupNotTriggeredByPrefix(t *testing.T) {
	h := NewServiceCodeHandler()
	h.SetSetupCallback(func() { t.Error("setup should not trigger on partial sequence") })

	// Type all but the last character
	for _, k := range "*#73887" {
		if h.AddKey(string(k)) != ServiceCodeNone {
			t.Error("should not trigger before final #")
		}
	}
}

// TestServiceCodeSetupAfterOtherKeys ensures setup code is detected even
// when preceded by other keypresses (rolling buffer behavior).
func TestServiceCodeSetupAfterOtherKeys(t *testing.T) {
	h := NewServiceCodeHandler()
	h.SetSetupCallback(func() {}) // register to prevent defaultSetupAction (which reboots)

	// Type some digits first
	for _, k := range "555" {
		h.AddKey(string(k))
	}
	// Then the full setup code; track whether the final key fires the trigger
	var triggered bool
	code := "*#73887#"
	for i, k := range code {
		result := h.AddKey(string(k))
		if result != ServiceCodeNone && i == len(code)-1 {
			triggered = true
		}
	}

	if !triggered {
		t.Error("*#73887# should trigger even when preceded by other keys")
	}
}

// TestServiceCodeSetupRegistered verifies the setup callback is stored.
func TestServiceCodeSetupRegistered(t *testing.T) {
	h := NewServiceCodeHandler()
	if h.onSetup != nil {
		t.Error("onSetup should be nil before registration")
	}
	h.SetSetupCallback(func() {})
	if h.onSetup == nil {
		t.Error("onSetup should be non-nil after registration")
	}
}

func TestServiceCodeRepair(t *testing.T) {
	called := make(chan struct{}, 1)
	h := NewServiceCodeHandler()
	h.SetRepairCallback(func() { called <- struct{}{} })

	code := "*#0*"
	var triggered bool
	for i, k := range code {
		result := h.AddKey(string(k))
		if result != ServiceCodeNone && i == len(code)-1 {
			if result != ServiceCodeTerminal {
				t.Errorf("repair should be terminal, got %v", result)
			}
			triggered = true
		}
	}
	if !triggered {
		t.Error("*#0* should trigger repair")
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Error("repair callback never fired")
	}
}

func TestServiceCodeFactoryReset(t *testing.T) {
	called := make(chan struct{}, 1)
	h := NewServiceCodeHandler()
	h.SetFactoryResetCallback(func() { called <- struct{}{} })

	code := "*#00000#"
	var triggered bool
	for i, k := range code {
		result := h.AddKey(string(k))
		if result != ServiceCodeNone && i == len(code)-1 {
			if result != ServiceCodeTerminal {
				t.Errorf("factory reset should be terminal, got %v", result)
			}
			triggered = true
		}
	}
	if !triggered {
		t.Error("*#00000# should trigger factory reset")
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Error("factory-reset callback never fired")
	}
}

func TestServiceCodeRepairDoesNotTriggerShutdown(t *testing.T) {
	h := NewServiceCodeHandler()
	h.SetShutdownCallback(func() { t.Error("shutdown should not trigger on *#0*") })
	h.SetRepairCallback(func() {})

	for _, k := range "*#0*" {
		h.AddKey(string(k))
	}
}

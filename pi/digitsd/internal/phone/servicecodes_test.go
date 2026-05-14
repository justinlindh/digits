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
	h.OnVolume = func(level int) { gotLevel = level }

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
	h.OnAudioTest = func() {}

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
	h.OnVolume = func(level int) { gotLevel = level }

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
	h.OnSetup = func() { called <- struct{}{} }

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
	h.OnSetup = func() { t.Error("setup should not trigger on partial sequence") }

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
	h.OnSetup = func() {} // register to prevent defaultSetupAction (which reboots)

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
	if h.OnSetup != nil {
		t.Error("OnSetup should be nil before registration")
	}
	h.OnSetup = func() {}
	if h.OnSetup == nil {
		t.Error("OnSetup should be non-nil after registration")
	}
}

func TestServiceCodeRepair(t *testing.T) {
	called := make(chan struct{}, 1)
	h := NewServiceCodeHandler()
	h.OnRepair = func() { called <- struct{}{} }

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
	h.OnFactoryReset = func() { called <- struct{}{} }

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
	h.OnShutdown = func() { t.Error("shutdown should not trigger on *#0*") }
	h.OnRepair = func() {}

	for _, k := range "*#0*" {
		h.AddKey(string(k))
	}
}

func TestServiceCodeInCode(t *testing.T) {
	h := NewServiceCodeHandler()
	if h.InCode() {
		t.Error("empty buffer should not be InCode")
	}
	h.AddKey("0")
	if h.InCode() {
		t.Error("buffer starting with digit should not be InCode")
	}
	h.Reset()
	h.AddKey("*")
	if h.InCode() {
		t.Error("lone '*' should not be InCode (a code requires the '*#' prefix)")
	}
	h.AddKey("0")
	if h.InCode() {
		t.Error("'*' followed by digit should not be InCode (not a code prefix)")
	}
	h.Reset()
	for _, k := range "*#" {
		h.AddKey(string(k))
	}
	if !h.InCode() {
		t.Error("'*#' prefix should be InCode")
	}
	for _, k := range "000" {
		h.AddKey(string(k))
	}
	if !h.InCode() {
		t.Error("mid-code buffer (*#000) should be InCode")
	}
}

// TestFactoryResetVsRickRollEasterEgg exercises the dispatcher pattern from
// cmd/digitsd/main.go that gates easter eggs on InCode(). It guards against
// the regression where the "0000" easter egg ate the 4th zero of *#00000#
// before the service code handler could see the full sequence.
func TestFactoryResetVsRickRollEasterEgg(t *testing.T) {
	resetFired := make(chan struct{}, 1)
	svc := NewServiceCodeHandler()
	svc.OnFactoryReset = func() { resetFired <- struct{}{} }

	rickRoll := make(chan string, 1)
	eggs := NewEasterEggDetector([]EasterEgg{
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) { rickRoll <- clip })
	eggs.MinGap = 0

	for _, k := range "*#00000#" {
		key := string(k)
		// Mirrors main.go dispatch: suppress easter eggs while mid-code.
		if !svc.InCode() {
			if eggs.AddKey(key) {
				continue
			}
		}
		svc.AddKey(key)
	}

	select {
	case <-resetFired:
	case <-time.After(time.Second):
		t.Fatal("factory reset never fired for *#00000#")
	}
	select {
	case clip := <-rickRoll:
		t.Errorf("Rick Roll should not fire during *#00000#, got clip %q", clip)
	case <-time.After(50 * time.Millisecond):
		// expected: no easter egg
	}
}

// TestEasterEggBlockedAfterLoneStar verifies that a stray '*' prefix
// prevents the following "0000" from triggering the Rick Roll easter egg.
// Easter eggs require an exact match from the start of the dialing session.
func TestEasterEggBlockedAfterLoneStar(t *testing.T) {
	svc := NewServiceCodeHandler()
	eggs := NewEasterEggDetector([]EasterEgg{
		{Name: "Rick Roll", Trigger: "0000", Clip: "rickroll"},
	}, func(clip string) { t.Errorf("should not trigger after * prefix, got %s", clip) })
	eggs.MinGap = 0

	for _, k := range "*0000" {
		key := string(k)
		if !svc.InCode() {
			if eggs.AddKey(key) {
				t.Error("easter egg should not trigger with * prefix")
			}
		}
		svc.AddKey(key)
	}
}

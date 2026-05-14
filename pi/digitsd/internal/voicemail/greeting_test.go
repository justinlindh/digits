package voicemail

import (
	"os"
	"testing"
)

func TestHasGreeting_NoFile(t *testing.T) {
	s := newTestStore(t, Options{})
	if s.HasGreeting() {
		t.Error("HasGreeting should be false with no greeting file")
	}
}

func TestGreetingRecordAndCheck(t *testing.T) {
	s := newTestStore(t, Options{})

	rec, err := s.BeginGreetingRecording()
	if err != nil {
		t.Fatalf("BeginGreetingRecording: %v", err)
	}
	if _, err := rec.AppendFrame([]byte{0x01, 0x02}); err != nil {
		t.Fatalf("AppendFrame: %v", err)
	}
	if _, err := rec.AppendFrame([]byte{0x03, 0x04}); err != nil {
		t.Fatalf("AppendFrame: %v", err)
	}
	if _, err := rec.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	if !s.HasGreeting() {
		t.Error("HasGreeting should be true after recording")
	}

	path := s.GreetingPath()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("greeting file should exist at %s: %v", path, err)
	}
}

func TestGreetingRecordOverwritesPrevious(t *testing.T) {
	s := newTestStore(t, Options{})

	// Record first greeting.
	rec1, err := s.BeginGreetingRecording()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rec1.AppendFrame([]byte{0x01}); err != nil {
		t.Fatal(err)
	}
	if _, err := rec1.Finalize(); err != nil {
		t.Fatal(err)
	}

	// Record second greeting (should overwrite).
	rec2, err := s.BeginGreetingRecording()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rec2.AppendFrame([]byte{0x02}); err != nil {
		t.Fatal(err)
	}
	if _, err := rec2.AppendFrame([]byte{0x03}); err != nil {
		t.Fatal(err)
	}
	if _, err := rec2.Finalize(); err != nil {
		t.Fatal(err)
	}

	if !s.HasGreeting() {
		t.Error("HasGreeting should be true")
	}

	// Only one greeting file should exist.
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	greetingCount := 0
	for _, e := range entries {
		if e.Name() == "greeting.frames" {
			greetingCount++
		}
	}
	if greetingCount != 1 {
		t.Errorf("expected 1 greeting.frames, got %d", greetingCount)
	}
}

func TestDeleteGreeting(t *testing.T) {
	s := newTestStore(t, Options{})

	rec, err := s.BeginGreetingRecording()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rec.AppendFrame([]byte{0x01}); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Finalize(); err != nil {
		t.Fatal(err)
	}

	if !s.HasGreeting() {
		t.Fatal("greeting should exist before delete")
	}

	if err := s.DeleteGreeting(); err != nil {
		t.Fatalf("DeleteGreeting: %v", err)
	}
	if s.HasGreeting() {
		t.Error("HasGreeting should be false after delete")
	}
}

func TestDeleteGreeting_NoFile(t *testing.T) {
	s := newTestStore(t, Options{})
	if err := s.DeleteGreeting(); err != nil {
		t.Errorf("DeleteGreeting on missing file should not error: %v", err)
	}
}

func TestGreetingPlayback(t *testing.T) {
	s := newTestStore(t, Options{})

	rec, err := s.BeginGreetingRecording()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{{0x01, 0x02}, {0x03, 0x04}, {0x05}}
	for _, payload := range want {
		if _, err := rec.AppendFrame(payload); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rec.Finalize(); err != nil {
		t.Fatal(err)
	}

	p, err := s.OpenGreeting()
	if err != nil {
		t.Fatalf("OpenGreeting: %v", err)
	}
	defer p.Close() //nolint:errcheck

	for i, expected := range want {
		got, err := p.NextFrame()
		if err != nil {
			t.Fatalf("NextFrame[%d]: %v", i, err)
		}
		if string(got) != string(expected) {
			t.Errorf("frame %d = %x, want %x", i, got, expected)
		}
	}
}

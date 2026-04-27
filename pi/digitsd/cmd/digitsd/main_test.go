package main

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPollPing_Immediate(t *testing.T) {
	calls := 0
	err := pollPing(func() error { calls++; return nil }, 5*time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("pollPing returned error on immediate success: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 ping, got %d", calls)
	}
}

func TestPollPing_SucceedsOnRetry(t *testing.T) {
	calls := 0
	err := pollPing(func() error {
		calls++
		if calls < 3 {
			return errors.New("not ready")
		}
		return nil
	}, 5*time.Second, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("pollPing returned error after retry success: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 pings, got %d", calls)
	}
}

func TestPollPing_Deadline(t *testing.T) {
	calls := 0
	want := errors.New("never ready")
	start := time.Now()
	err := pollPing(func() error { calls++; return want }, 50*time.Millisecond, 10*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, want) {
		t.Errorf("pollPing returned %v, want last error %v", err, want)
	}
	if calls < 2 {
		t.Errorf("expected at least 2 attempts before deadline, got %d", calls)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("pollPing returned before deadline: %v elapsed", elapsed)
	}
}

func TestFirmwareNeedsReflash(t *testing.T) {
	cases := []struct {
		name    string
		pico    string
		bundled string
		want    bool
	}{
		{"both empty", "", "", false},
		{"pico empty", "", "1.7.0", false},
		{"bundled empty (sidecar absent)", "1.7.0", "", false},
		{"identical versions", "1.7.0", "1.7.0", false},
		{"older pico than bundled", "1.5.0-69-g8fc14f5a-dirty", "1.7.0", true},
		{"newer pico than bundled", "1.8.0", "1.7.0", true},
		{"dirty bundled, clean pico", "1.7.0", "1.7.0-3-gabcd-dirty", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firmwareNeedsReflash(tc.pico, tc.bundled)
			if got != tc.want {
				t.Errorf("firmwareNeedsReflash(%q, %q) = %v, want %v", tc.pico, tc.bundled, got, tc.want)
			}
		})
	}
}

func TestReadBundledFirmwareVersion_Missing(t *testing.T) {
	// Default path almost certainly doesn't exist on the dev host. The
	// function must return "" without erroring so firmwareNeedsReflash
	// short-circuits to "no reflash needed."
	if _, err := os.Stat(defaultFirmwareVersionPath); err == nil {
		t.Skipf("%s exists on this host; skipping", defaultFirmwareVersionPath)
	}
	if got := readBundledFirmwareVersion(); got != "" {
		t.Errorf("readBundledFirmwareVersion() with missing file = %q, want %q", got, "")
	}
}

func TestWritePCMWav(t *testing.T) {
	samples := []int16{0, 100, -100, 32767, -32768, 0}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")

	if err := writePCMWav(path, samples, 48000); err != nil {
		t.Fatalf("writePCMWav: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Minimum WAV header = 44 bytes + 2*len(samples) payload.
	want := 44 + 2*len(samples)
	if len(data) != want {
		t.Fatalf("file size: got %d, want %d", len(data), want)
	}

	if string(data[0:4]) != "RIFF" {
		t.Errorf("magic: got %q, want RIFF", data[0:4])
	}
	if string(data[8:12]) != "WAVE" {
		t.Errorf("format: got %q, want WAVE", data[8:12])
	}
	if string(data[12:16]) != "fmt " {
		t.Errorf("fmt chunk: got %q", data[12:16])
	}
	if binary.LittleEndian.Uint16(data[20:22]) != 1 {
		t.Errorf("audio format: got %d, want 1 (PCM)", binary.LittleEndian.Uint16(data[20:22]))
	}
	if binary.LittleEndian.Uint16(data[22:24]) != 1 {
		t.Errorf("channels: got %d, want 1", binary.LittleEndian.Uint16(data[22:24]))
	}
	if binary.LittleEndian.Uint32(data[24:28]) != 48000 {
		t.Errorf("sample rate: got %d, want 48000", binary.LittleEndian.Uint32(data[24:28]))
	}
	if binary.LittleEndian.Uint16(data[34:36]) != 16 {
		t.Errorf("bits per sample: got %d, want 16", binary.LittleEndian.Uint16(data[34:36]))
	}
	if string(data[36:40]) != "data" {
		t.Errorf("data chunk: got %q", data[36:40])
	}

	// Verify payload is the original samples.
	for i, s := range samples {
		got := int16(binary.LittleEndian.Uint16(data[44+2*i : 44+2*i+2]))
		if got != s {
			t.Errorf("sample %d: got %d, want %d", i, got, s)
		}
	}
}

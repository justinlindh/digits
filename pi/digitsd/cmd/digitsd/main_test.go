package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

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

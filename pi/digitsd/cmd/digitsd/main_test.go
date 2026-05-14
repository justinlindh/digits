package main

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/config"
	"github.com/justinlindh/digits/pi/digitsd/internal/voicemail"
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

// TestLEDModeWithVoicemailHint exercises the OFF-to-SLOWER_PULSE rewrite
// that powers the message-waiting indicator. The function is pure
// relative to d.serial (no I/O), so the test asserts return values
// directly without a fake serial port.
func TestLEDModeWithVoicemailHint(t *testing.T) {
	mustStoreWithMessages := func(t *testing.T, unheard, heard int) *voicemail.Store {
		t.Helper()
		s, err := voicemail.Open(t.TempDir(), voicemail.Options{})
		if err != nil {
			t.Fatalf("voicemail.Open: %v", err)
		}
		for i := 0; i < unheard+heard; i++ {
			r, err := s.BeginRecording()
			if err != nil {
				t.Fatalf("BeginRecording[%d]: %v", i, err)
			}
			// One frame is enough; Finalize promotes any non-empty recording.
			if _, err := r.AppendFrame([]byte{0xff, 0x00}); err != nil {
				t.Fatalf("AppendFrame[%d]: %v", i, err)
			}
			m, err := r.Finalize()
			if err != nil {
				t.Fatalf("Finalize[%d]: %v", i, err)
			}
			if i < heard {
				if err := s.MarkHeard(m.ID); err != nil {
					t.Fatalf("MarkHeard[%d]: %v", i, err)
				}
			}
			// Recordings share their ID with UnixMilli; sleep a touch to keep IDs unique.
			time.Sleep(2 * time.Millisecond)
		}
		return s
	}

	tests := []struct {
		name           string
		mode           string
		voicemailOn    bool
		store          func(*testing.T) *voicemail.Store
		want           string
	}{
		{
			name:        "off with feature disabled passes through",
			mode:        "OFF",
			voicemailOn: false,
			store:       func(t *testing.T) *voicemail.Store { return mustStoreWithMessages(t, 3, 0) },
			want:        "OFF",
		},
		{
			name:        "off with feature enabled and no store passes through",
			mode:        "OFF",
			voicemailOn: true,
			store:       func(t *testing.T) *voicemail.Store { return nil },
			want:        "OFF",
		},
		{
			name:        "off with no messages passes through",
			mode:        "OFF",
			voicemailOn: true,
			store:       func(t *testing.T) *voicemail.Store { return mustStoreWithMessages(t, 0, 0) },
			want:        "OFF",
		},
		{
			name:        "off with one unheard rewrites to slower pulse",
			mode:        "OFF",
			voicemailOn: true,
			store:       func(t *testing.T) *voicemail.Store { return mustStoreWithMessages(t, 1, 0) },
			want:        "SLOWER_PULSE",
		},
		{
			name:        "off with all heard passes through",
			mode:        "OFF",
			voicemailOn: true,
			store:       func(t *testing.T) *voicemail.Store { return mustStoreWithMessages(t, 0, 2) },
			want:        "OFF",
		},
		{
			name:        "blink is never rewritten even with unheard",
			mode:        "BLINK",
			voicemailOn: true,
			store:       func(t *testing.T) *voicemail.Store { return mustStoreWithMessages(t, 5, 0) },
			want:        "BLINK",
		},
		{
			name:        "on is never rewritten even with unheard",
			mode:        "ON",
			voicemailOn: true,
			store:       func(t *testing.T) *voicemail.Store { return mustStoreWithMessages(t, 1, 0) },
			want:        "ON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &daemonCallbacks{
				cfg: &config.Config{Voicemail: config.Voicemail{Enabled: tt.voicemailOn}},
			}
			d.voicemailStore = tt.store(t)
			if got := d.ledModeWithVoicemailHint(tt.mode); got != tt.want {
				t.Errorf("ledModeWithVoicemailHint(%q) = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

// TestOpenNextUnheardLocked_AfterIDSkipsCurrent locks down the "#" skip
// behavior. The original implementation always returned the first unheard
// message, so "#" (which leaves the heard flag untouched) replayed the
// current message. The afterID parameter exists to skip past the current
// message during a "#" press.
func TestOpenNextUnheardLocked_AfterIDSkipsCurrent(t *testing.T) {
	store, err := voicemail.Open(t.TempDir(), voicemail.Options{})
	if err != nil {
		t.Fatalf("voicemail.Open: %v", err)
	}

	// Three unheard messages with strictly increasing IDs.
	ids := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		r, err := store.BeginRecording()
		if err != nil {
			t.Fatalf("BeginRecording[%d]: %v", i, err)
		}
		if _, err := r.AppendFrame([]byte{0xff, 0x00}); err != nil {
			t.Fatalf("AppendFrame[%d]: %v", i, err)
		}
		m, err := r.Finalize()
		if err != nil {
			t.Fatalf("Finalize[%d]: %v", i, err)
		}
		ids = append(ids, m.ID)
		// Recording IDs are UnixMilli; ensure they don't collide.
		time.Sleep(2 * time.Millisecond)
	}

	d := &daemonCallbacks{voicemailStore: store}

	// Helper: open + assert ID + tear down so the next call starts clean.
	openExpect := func(afterID int64, want int64, label string) {
		t.Helper()
		d.voicemailMu.Lock()
		defer d.voicemailMu.Unlock()
		sess, err := d.openNextUnheardLocked(store, afterID)
		if err != nil {
			t.Fatalf("%s: openNextUnheardLocked(%d): %v", label, afterID, err)
		}
		if want == 0 {
			if sess != nil {
				_ = sess.player.Close()
				d.voicemailPlayback = nil
				t.Fatalf("%s: openNextUnheardLocked(%d) = id %d, want nil", label, afterID, sess.id)
			}
			return
		}
		if sess == nil {
			t.Fatalf("%s: openNextUnheardLocked(%d) = nil, want id %d", label, afterID, want)
		}
		if sess.id != want {
			t.Errorf("%s: openNextUnheardLocked(%d) = id %d, want id %d", label, afterID, sess.id, want)
		}
		_ = sess.player.Close()
		d.voicemailPlayback = nil
	}

	openExpect(0, ids[0], "afterID=0 returns first unheard")
	openExpect(ids[0], ids[1], "afterID=first skips to second")
	openExpect(ids[1], ids[2], "afterID=second skips to third")
	openExpect(ids[2], 0, "afterID=last yields no next message")
}

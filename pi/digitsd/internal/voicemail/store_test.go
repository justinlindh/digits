package voicemail

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T, opts Options) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func writeMessage(t *testing.T, s *Store, payloads ...[]byte) Message {
	t.Helper()
	r, err := s.BeginRecording()
	if err != nil {
		t.Fatalf("BeginRecording: %v", err)
	}
	for _, p := range payloads {
		if _, err := r.AppendFrame(p); err != nil {
			t.Fatalf("AppendFrame: %v", err)
		}
	}
	m, err := r.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return m
}

func TestRecordAndPlayRoundTrip(t *testing.T) {
	s := newTestStore(t, Options{})
	want := [][]byte{
		[]byte{0x01, 0x02, 0x03},
		[]byte{0x04, 0x05},
		[]byte{0x06},
	}
	m := writeMessage(t, s, want...)
	if m.ID == 0 {
		t.Fatal("Finalize returned zero ID")
	}
	if m.Duration != 3*20*time.Millisecond {
		t.Errorf("duration = %v, want 60ms", m.Duration)
	}

	p, err := s.OpenPlayer(m.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
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
	if _, err := p.NextFrame(); !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF after last frame, got %v", err)
	}
}

func TestEmptyRecordingDiscarded(t *testing.T) {
	s := newTestStore(t, Options{})
	r, err := s.BeginRecording()
	if err != nil {
		t.Fatalf("BeginRecording: %v", err)
	}
	m, err := r.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if m.ID != 0 {
		t.Errorf("empty Finalize returned ID %d, want 0", m.ID)
	}
	msgs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("List: %d messages after empty Finalize, want 0", len(msgs))
	}
}

func TestDiscardRemovesTempFile(t *testing.T) {
	s := newTestStore(t, Options{})
	r, err := s.BeginRecording()
	if err != nil {
		t.Fatalf("BeginRecording: %v", err)
	}
	if _, err := r.AppendFrame([]byte{0xff}); err != nil {
		t.Fatal(err)
	}
	r.Discard()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("dir has %d entries after Discard, want 0", len(entries))
	}
}

func TestUnheardCountAndMarkHeard(t *testing.T) {
	s := newTestStore(t, Options{Now: counterClock()})
	a := writeMessage(t, s, []byte{1})
	_ = writeMessage(t, s, []byte{2})

	got, err := s.UnheardCount()
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("UnheardCount = %d, want 2", got)
	}

	if err := s.MarkHeard(a.ID); err != nil {
		t.Fatalf("MarkHeard: %v", err)
	}
	got, err = s.UnheardCount()
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("UnheardCount after MarkHeard = %d, want 1", got)
	}

	// Idempotent
	if err := s.MarkHeard(a.ID); err != nil {
		t.Fatalf("MarkHeard idempotent: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t, Options{Now: counterClock()})
	m := writeMessage(t, s, []byte{0xaa})
	if err := s.Delete(m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	msgs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("List after Delete: %d, want 0", len(msgs))
	}
	// Deleting a missing ID is not an error.
	if err := s.Delete(m.ID); err != nil {
		t.Errorf("Delete of missing ID returned %v, want nil", err)
	}
}

func TestFIFOEviction(t *testing.T) {
	s := newTestStore(t, Options{MaxMessages: 2, Now: counterClock()})
	a := writeMessage(t, s, []byte{1})
	b := writeMessage(t, s, []byte{2})
	c := writeMessage(t, s, []byte{3})

	msgs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("List: %d messages, want 2", len(msgs))
	}
	if msgs[0].ID != b.ID || msgs[1].ID != c.ID {
		t.Errorf("after eviction: ids = [%d %d], want [%d %d]", msgs[0].ID, msgs[1].ID, b.ID, c.ID)
	}
	if _, err := os.Stat(filepath.Join(s.dir, fileFor(a.ID))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("oldest frames file still present after eviction: %v", err)
	}
}

func TestMaxMessageDurationCap(t *testing.T) {
	s := newTestStore(t, Options{MaxMessageDuration: 60 * time.Millisecond})
	r, err := s.BeginRecording()
	if err != nil {
		t.Fatal(err)
	}
	// 3 frames * 20ms = 60ms => atCap on the third.
	for i := 0; i < 2; i++ {
		atCap, err := r.AppendFrame([]byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if atCap {
			t.Errorf("frame %d reported atCap early", i)
		}
	}
	atCap, err := r.AppendFrame([]byte{0xff})
	if err != nil {
		t.Fatal(err)
	}
	if !atCap {
		t.Errorf("expected atCap=true on 3rd frame")
	}
	if _, err := r.Finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSweepRemovesOrphanTmp(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, "12345.frames.tmp")
	if err := os.WriteFile(orphan, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("orphan tmp survived Open: %v", err)
	}
}

func TestPlayerHandlesTruncatedTrailer(t *testing.T) {
	s := newTestStore(t, Options{})
	m := writeMessage(t, s, []byte{0xaa, 0xbb})

	// Truncate the frames file to simulate a partial trailing record.
	f, err := os.OpenFile(m.Path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2); err != nil { // leave only first two bytes of header
		t.Fatal(err)
	}
	f.Close() //nolint:errcheck

	p, err := s.OpenPlayer(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close() //nolint:errcheck
	if _, err := p.NextFrame(); !errors.Is(err, io.EOF) {
		t.Errorf("truncated read should yield EOF, got %v", err)
	}
}

// counterClock returns a Now that advances 1ms per call so successive
// recordings get distinct IDs without sleeping.
func counterClock() func() time.Time {
	var n int64
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Millisecond)
	}
}

func fileFor(id int64) string { return fmt.Sprintf("%d.frames", id) }

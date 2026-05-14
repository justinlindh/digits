// Package voicemail provides a phone-local store for answering-machine
// recordings. Each digitsd instance owns its messages on disk under a single
// directory. There is no server-side index; messages are retrieved only at
// the phone they were left at.
//
// On-disk layout under Dir:
//
//	<unix_ms>.frames    length-prefixed Opus payloads (uint32 LE length, then bytes)
//	<unix_ms>.meta      JSON metadata (heard flag, duration estimate)
//
// Writes are crash-safe: recordings stream to <id>.frames.tmp and rename
// atomically only on Finalize. A crash mid-recording leaves an orphan .tmp
// that the next OpenStore sweep removes.
package voicemail

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Frame size constant matches internal/codec/opus.go (20ms at 48kHz).
const opusFrameMs = 20

// Options controls store retention.
type Options struct {
	// MaxMessages caps the number of stored messages. When exceeded, the
	// oldest are deleted (FIFO). Zero disables the cap.
	MaxMessages int
	// MaxMessageDuration caps a single recording. Zero disables the cap;
	// the recorder will accept frames indefinitely until Finalize.
	MaxMessageDuration time.Duration
	// Now is injected for tests. Defaults to time.Now.
	Now func() time.Time
}

// Message is a finalized voicemail.
type Message struct {
	ID         int64         // unix milliseconds; also the on-disk filename stem
	Heard      bool          // true once played to completion at least once
	Duration   time.Duration // approximate, computed from frame count at finalize
	Path       string        // absolute path to the .frames file
	RecordedAt time.Time
}

type metaFile struct {
	Heard      bool      `json:"heard"`
	DurationMs int64     `json:"duration_ms"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Store is the on-disk message archive. Methods are safe for concurrent use.
type Store struct {
	dir  string
	opts Options

	mu sync.Mutex
}

// Open opens or creates the store at dir. Orphan .tmp files from a prior
// crash are removed.
func Open(dir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("voicemail: mkdir %s: %w", dir, err)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Store{dir: dir, opts: opts}
	if err := s.cleanOrphans(); err != nil {
		slog.Warn("voicemail: orphan cleanup failed", "error", err)
	}
	return s, nil
}

func (s *Store) cleanOrphans() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) == ".tmp" {
			if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// List returns all finalized messages, oldest first.
func (s *Store) List() ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() ([]Message, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var msgs []Message
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".frames" {
			continue
		}
		stem := name[:len(name)-len(".frames")]
		id, err := parseID(stem)
		if err != nil {
			continue
		}
		framesPath := filepath.Join(s.dir, name)
		// Finalize renames .frames before acquiring s.mu to write .meta, so a
		// List() racing that gap sees a present .frames with no .meta yet.
		// Tolerate that here: missing meta yields a zero-value record and the
		// ID-based RecordedAt fallback below.
		meta, err := s.readMeta(id)
		if err != nil {
			slog.Warn("voicemail: meta read failed", "id", id, "error", err)
			meta = metaFile{}
		}
		recordedAt := meta.RecordedAt
		if recordedAt.IsZero() {
			recordedAt = time.UnixMilli(id)
		}
		msgs = append(msgs, Message{
			ID:         id,
			Heard:      meta.Heard,
			Duration:   time.Duration(meta.DurationMs) * time.Millisecond,
			Path:       framesPath,
			RecordedAt: recordedAt,
		})
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	return msgs, nil
}

// UnheardCount returns the number of messages whose Heard flag is false.
func (s *Store) UnheardCount() (int, error) {
	msgs, err := s.List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range msgs {
		if !m.Heard {
			n++
		}
	}
	return n, nil
}

// MarkHeard sets the Heard flag for the given message ID.
func (s *Store) MarkHeard(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMeta(id)
	if err != nil {
		return err
	}
	if meta.Heard {
		return nil
	}
	meta.Heard = true
	return s.writeMeta(id, meta)
}

// Delete removes a message and its metadata.
func (s *Store) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	framesPath := filepath.Join(s.dir, fmt.Sprintf("%d.frames", id))
	metaPath := filepath.Join(s.dir, fmt.Sprintf("%d.meta", id))
	if err := os.Remove(framesPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(metaPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// BeginRecording opens a fresh Recorder. AppendFrame queues raw Opus payloads
// (one per 20ms RTP frame). Finalize promotes the temp file to a real message.
// Discard removes the temp file without finalizing.
func (s *Store) BeginRecording() (*Recorder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.opts.Now().UnixMilli()
	framesPath := filepath.Join(s.dir, fmt.Sprintf("%d.frames", id))
	tmpPath := framesPath + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("voicemail: open tmp: %w", err)
	}
	return &Recorder{
		store:    s,
		id:       id,
		tmpPath:  tmpPath,
		finalPath: framesPath,
		file:     f,
		maxFrames: framesForDuration(s.opts.MaxMessageDuration),
	}, nil
}

// Recorder writes Opus frames for a single message.
type Recorder struct {
	store     *Store
	id        int64
	tmpPath   string
	finalPath string

	mu        sync.Mutex
	file      *os.File
	frames    int
	maxFrames int // 0 means no cap
	closed    bool
}

// AppendFrame writes one Opus payload. Returns true if the message has hit its
// configured duration cap and should be finalized; false otherwise.
func (r *Recorder) AppendFrame(payload []byte) (atCap bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false, errors.New("voicemail: recorder closed")
	}
	if len(payload) > 0xffff {
		return false, fmt.Errorf("voicemail: opus payload too large (%d)", len(payload))
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := r.file.Write(hdr[:]); err != nil {
		return false, err
	}
	if _, err := r.file.Write(payload); err != nil {
		return false, err
	}
	r.frames++
	if r.maxFrames > 0 && r.frames >= r.maxFrames {
		return true, nil
	}
	return false, nil
}

// Frames returns the number of frames written so far. For tests and cap checks.
func (r *Recorder) Frames() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frames
}

// Finalize closes the temp file, fsyncs, renames to the canonical path,
// writes the metadata file, and applies retention. If no frames were written,
// the temp file is discarded instead and (0, nil) returned.
func (r *Recorder) Finalize() (Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Message{}, errors.New("voicemail: recorder already closed")
	}
	r.closed = true

	if r.frames == 0 {
		_ = r.file.Close()
		_ = os.Remove(r.tmpPath)
		return Message{}, nil
	}

	if err := r.file.Sync(); err != nil {
		_ = r.file.Close()
		_ = os.Remove(r.tmpPath)
		return Message{}, fmt.Errorf("voicemail: fsync: %w", err)
	}
	if err := r.file.Close(); err != nil {
		_ = os.Remove(r.tmpPath)
		return Message{}, fmt.Errorf("voicemail: close: %w", err)
	}
	if err := os.Rename(r.tmpPath, r.finalPath); err != nil {
		_ = os.Remove(r.tmpPath)
		return Message{}, fmt.Errorf("voicemail: rename: %w", err)
	}

	duration := time.Duration(r.frames) * opusFrameMs * time.Millisecond
	recordedAt := time.UnixMilli(r.id)
	meta := metaFile{
		Heard:      false,
		DurationMs: duration.Milliseconds(),
		RecordedAt: recordedAt,
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if err := r.store.writeMeta(r.id, meta); err != nil {
		return Message{}, fmt.Errorf("voicemail: write meta: %w", err)
	}

	if err := r.store.evictLocked(); err != nil {
		slog.Warn("voicemail: eviction failed", "error", err)
	}

	return Message{
		ID:         r.id,
		Heard:      false,
		Duration:   duration,
		Path:       r.finalPath,
		RecordedAt: recordedAt,
	}, nil
}

// Discard closes the temp file and removes it without finalizing. Safe to
// call after Finalize (no-op).
func (r *Recorder) Discard() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	_ = r.file.Close()
	_ = os.Remove(r.tmpPath)
}

// Player iterates Opus frames for a finalized message. Frame returns
// io.EOF when the message ends.
type Player struct {
	file *os.File
}

// Open returns a Player for the given message ID.
func (s *Store) Open(id int64) (*Player, error) {
	path := filepath.Join(s.dir, fmt.Sprintf("%d.frames", id))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &Player{file: f}, nil
}

// NextFrame returns the next Opus payload, or io.EOF when exhausted.
func (p *Player) NextFrame() ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(p.file, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// Truncated header: treat as end of stream rather than an error,
			// since a crash mid-write could leave a partial trailer.
			return nil, io.EOF
		}
		return nil, err
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if n == 0 {
		return []byte{}, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(p.file, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}
	return buf, nil
}

// Close releases the underlying file handle.
func (p *Player) Close() error {
	return p.file.Close()
}

// --- internal helpers ---

func (s *Store) metaPath(id int64) string {
	return filepath.Join(s.dir, fmt.Sprintf("%d.meta", id))
}

func (s *Store) readMeta(id int64) (metaFile, error) {
	var m metaFile
	data, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m, nil
		}
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return metaFile{}, err
	}
	return m, nil
}

func (s *Store) writeMeta(id int64, m metaFile) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := s.metaPath(id) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.metaPath(id))
}

// evictLocked enforces MaxMessages by deleting oldest first. Caller holds s.mu.
func (s *Store) evictLocked() error {
	if s.opts.MaxMessages <= 0 {
		return nil
	}
	msgs, err := s.listLocked()
	if err != nil {
		return err
	}
	excess := len(msgs) - s.opts.MaxMessages
	if excess <= 0 {
		return nil
	}
	for i := 0; i < excess; i++ {
		framesPath := filepath.Join(s.dir, fmt.Sprintf("%d.frames", msgs[i].ID))
		metaPath := s.metaPath(msgs[i].ID)
		if err := os.Remove(framesPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(metaPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func parseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// framesForDuration returns the cap in 20ms frames, or 0 to disable.
func framesForDuration(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d / (opusFrameMs * time.Millisecond))
}

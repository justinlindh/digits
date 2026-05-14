package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"sync"
)

// FrameWriter is the interface the mixer writes to. Playback implements this.
// Using an interface allows mock testing without ALSA hardware.
type FrameWriter interface {
	WriteFrame(samples []int16) error
	PeriodSize() int
}

// Mixer is a single-threaded audio render loop. All playback goes through it.
// External callers set state (PlayLoop, PlayOnce, AddWebRTCSource / RemoveWebRTCSource, StopTone).
// The render goroutine reads state and writes one mixed period per tick.
//
// The render thread is the sole writer to the FrameWriter — no other goroutine
// may call WriteFrame. This eliminates the concurrent-write races that caused
// choppy audio in the old TonePlayer + Keepalive architecture.
//
// When idle (no loop, no one-shot, no WebRTC), the render loop writes silence,
// replacing the Keepalive goroutine. DAC keepalive is free.
// debugPCMFile is a raw PCM capture of everything sent to the DAC.
// Set via EnableCapture/DisableCapture. Only written from render loop.
type Mixer struct {
	capturePath string   // if non-empty, render loop writes raw PCM here
	captureFile *os.File // open file handle (nil when not capturing)
	w      FrameWriter
	period int

	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{} // closed by renderLoop on exit; Stop blocks on it
	running bool

	// Loop source: loops named tone until StopTone is called.
	loopSamples []int16
	loopName    string
	loopPos     int

	// One-shot queue: each entry plays once, then is dequeued.
	// Multiple PlayOnce calls queue sequentially.
	onceQueue [][]int16
	oncePos   int

	// WebRTC sources: decoded PCM frames from remote peers, keyed by peer identifier.
	// Non-blocking reads in render loop — frames are dropped if behind.
	webrtcMu      sync.Mutex
	webrtcSources map[string]chan []int16

	// Loaded tones (name → PCM samples, S16_LE mono 48kHz)
	tones map[string][]int16
}

// NewMixer creates a Mixer that writes to the given FrameWriter.
func NewMixer(w FrameWriter) *Mixer {
	return &Mixer{
		w:             w,
		period:        w.PeriodSize(),
		tones:         make(map[string][]int16),
		webrtcSources: make(map[string]chan []int16),
	}
}

// Start begins the render loop goroutine.
func (m *Mixer) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return
	}
	m.running = true
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	go m.renderLoop(m.stopCh, m.doneCh)
	slog.Info("mixer: render loop started")
}

// Stop halts the render loop. Blocks until the goroutine has exited so callers
// can safely observe everything the loop wrote.
func (m *Mixer) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	close(m.stopCh)
	m.running = false
	done := m.doneCh
	m.mu.Unlock()
	<-done
	slog.Info("mixer: render loop stopped")
}

// renderLoop is the single goroutine that writes to hardware.
// It runs at ALSA's pace: snd_pcm_writei blocks ~20ms per period.
func (m *Mixer) renderLoop(stop, done chan struct{}) {
	// Defers run LIFO: close(done) is registered first so it runs LAST,
	// guaranteeing Stop() unblocks even if the loop panics.
	defer close(done)
	defer func() {
		if r := recover(); r != nil {
			slog.Error("mixer: render loop panic", "panic", r, "stack", string(debug.Stack()))
			// Keep running in sync with reality so Stop() doesn't try to
			// close a dead channel and a later Start() can observe the
			// halted state accurately.
			m.mu.Lock()
			m.running = false
			m.mu.Unlock()
		}
	}()
	buf := make([]int16, m.period)
	for {
		select {
		case <-stop:
			return
		default:
		}

		// Zero the buffer (silence = DAC keepalive when idle)
		for i := range buf {
			buf[i] = 0
		}

		m.mu.Lock()

		// 1. Mix looping tone (wraps around the tone samples)
		if m.loopSamples != nil {
			for i := 0; i < m.period; i++ {
				buf[i] = clampAdd(buf[i], m.loopSamples[m.loopPos])
				m.loopPos++
				if m.loopPos >= len(m.loopSamples) {
					m.loopPos = 0
				}
			}
		}

		// 2. Mix one-shot tone(s) — seamlessly chain queued clips within one frame
		for i := 0; i < m.period && len(m.onceQueue) > 0; {
			src := m.onceQueue[0]
			for ; i < m.period && m.oncePos < len(src); i++ {
				buf[i] = clampAdd(buf[i], src[m.oncePos])
				m.oncePos++
			}
			if m.oncePos >= len(src) {
				// This one-shot is finished; advance to next clip seamlessly
				m.onceQueue = m.onceQueue[1:]
				m.oncePos = 0
				// Continue filling the frame from the next queued clip (no gap)
			}
		}

		m.mu.Unlock()

		// 3. Mix WebRTC audio from all sources (non-blocking — drop if channel is empty)
		m.readWebRTCSources(buf)

		// 4. Write the mixed period to hardware (only this goroutine does this)
		m.w.WriteFrame(buf) //nolint:errcheck

		// 5. Capture raw PCM if enabled (same goroutine, no lock needed for file write)
		if m.captureFile != nil {
			binary.Write(m.captureFile, binary.LittleEndian, buf) //nolint:errcheck
		}
	}
}

// clampAdd adds two int16 values with saturation (no overflow wrap-around).
// EnableCapture starts writing raw S16LE PCM to path. Call from main, not render loop.
func (m *Mixer) EnableCapture(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	m.captureFile = f
	m.capturePath = path
	slog.Info("mixer: PCM capture enabled", "path", path)
	return nil
}

// DisableCapture stops capturing and closes the file.
func (m *Mixer) DisableCapture() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.captureFile != nil {
		if err := m.captureFile.Close(); err != nil {
			slog.Warn("mixer: close capture file failed", "path", m.capturePath, "error", err)
		}
		slog.Info("mixer: PCM capture stopped", "path", m.capturePath)
		m.captureFile = nil
		m.capturePath = ""
	}
}

func clampAdd(a, b int16) int16 {
	sum := int32(a) + int32(b)
	if sum > 32767 {
		return 32767
	}
	if sum < -32768 {
		return -32768
	}
	return int16(sum)
}

// LoadTone registers a named PCM tone for playback.
func (m *Mixer) LoadTone(name string, samples []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tones[name] = samples
}

// LoadTonesFromDir loads all .wav files from a directory into the mixer.
// Reuses loadWAV from tones.go. Expects S16_LE mono 48kHz WAV files.
func (m *Mixer) LoadTonesFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read tone dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".wav" {
			continue
		}
		name := e.Name()[:len(e.Name())-4] // strip .wav extension
		samples, err := loadWAV(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("load %s: %w", e.Name(), err)
		}
		m.LoadTone(name, samples)
		slog.Info("mixer: loaded tone", "name", name, "samples", len(samples), "duration_s", float64(len(samples))/48000)
	}
	return nil
}

// PlayLoop starts looping a named tone. Takes effect on the next render tick (~20ms).
// Replaces any currently playing loop. Does not clear the one-shot queue.
func (m *Mixer) PlayLoop(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	samples, ok := m.tones[name]
	if !ok {
		slog.Warn("mixer: unknown tone", "name", name)
		return
	}
	m.loopSamples = samples
	m.loopName = name
	m.loopPos = 0
}

// StopTone silences the currently looping tone within one period (~20ms).
// Queued one-shots and WebRTC sources are untouched so the FSM can kill the
// dial tone without racing a DTMF beep the daemon just queued. Use StopAll
// to wipe everything.
func (m *Mixer) StopTone() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loopSamples = nil
	m.loopName = ""
	m.loopPos = 0
}

// StopAll stops all audio: loops, one-shots, and clears all WebRTC sources.
// Use on hang-up to guarantee silence.
func (m *Mixer) StopAll() {
	m.mu.Lock()
	m.loopSamples = nil
	m.loopName = ""
	m.loopPos = 0
	m.onceQueue = nil
	m.oncePos = 0
	m.mu.Unlock()
	// Clear all WebRTC sources. Channels are GC'd when senders release them.
	m.webrtcMu.Lock()
	defer m.webrtcMu.Unlock()
	m.webrtcSources = make(map[string]chan []int16)
}

// OncePlaying returns true if any one-shot tones are still in the queue.
func (m *Mixer) OncePlaying() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.onceQueue) > 0
}

// Active returns the name of the currently looping tone, or "" if none.
func (m *Mixer) Active() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loopName
}

// ToneNames returns the sorted names of all loaded tones.
func (m *Mixer) ToneNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.tones))
	for k := range m.tones {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// PlayOnce queues a one-shot tone to play once, mixed over any active loop.
// Multiple PlayOnce calls queue sequentially (real-phone DTMF beep behavior).
func (m *Mixer) PlayOnce(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	samples, ok := m.tones[name]
	if !ok {
		slog.Warn("mixer: unknown tone", "name", name)
		return
	}
	m.onceQueue = append(m.onceQueue, samples)
}

// PlayOnceSamples queues raw PCM samples for one-shot playback.
func (m *Mixer) PlayOnceSamples(samples []int16) {
	if len(samples) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onceQueue = append(m.onceQueue, samples)
}

// AddWebRTCSource registers a named WebRTC audio source and returns the channel
// to send decoded PCM frames into. If the key already exists, the existing
// channel is returned. Callers write into this channel; the render loop reads
// non-blocking and mixes all sources together.
func (m *Mixer) AddWebRTCSource(key string) chan []int16 {
	m.webrtcMu.Lock()
	defer m.webrtcMu.Unlock()
	if existing, ok := m.webrtcSources[key]; ok {
		return existing
	}
	ch := make(chan []int16, 8)
	m.webrtcSources[key] = ch
	return ch
}

// RemoveWebRTCSource removes a named WebRTC audio source. The channel is not
// closed — senders may continue writing until they exit; frames accumulate in
// the buffer and are GC'd when the sender releases its reference. Safe to call
// from any goroutine.
func (m *Mixer) RemoveWebRTCSource(key string) {
	m.webrtcMu.Lock()
	defer m.webrtcMu.Unlock()
	delete(m.webrtcSources, key)
}

// ImportWebRTCSource registers an existing channel under the given key. Unlike
// AddWebRTCSource, no new channel is allocated; the caller is the channel's
// producer and the mixer becomes a consumer. Used by voicemail pickup, where
// the OnRemoteTrack decode goroutine is already filling a channel and the
// mixer just needs to start draining it for the earpiece. Existing entries at
// the same key are overwritten, mirroring map semantics; the documented call
// sites guarantee no prior AddWebRTCSource for the same peer.
func (m *Mixer) ImportWebRTCSource(key string, ch chan []int16) {
	m.webrtcMu.Lock()
	defer m.webrtcMu.Unlock()
	m.webrtcSources[key] = ch
}

// readWebRTCSources reads one frame (non-blocking) from each registered source
// and accumulates it into buf via clampAdd. Called only from the render loop.
func (m *Mixer) readWebRTCSources(buf []int16) {
	m.webrtcMu.Lock()
	defer m.webrtcMu.Unlock()
	for _, ch := range m.webrtcSources {
		select {
		case frame, ok := <-ch:
			if !ok {
				continue
			}
			for i := 0; i < len(buf) && i < len(frame); i++ {
				buf[i] = clampAdd(buf[i], frame[i])
			}
		default:
			// no frame available from this source right now
		}
	}
}

// loadWAV reads a WAV file and returns the PCM samples as []int16.
// Parses RIFF/WAV chunks to find the actual data offset (handles extended headers).
func loadWAV(path string) ([]int16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			slog.Warn("close WAV failed", "path", path, "error", cerr)
		}
	}()

	// Read RIFF header
	var riffHdr [12]byte
	if _, err := io.ReadFull(f, riffHdr[:]); err != nil {
		return nil, fmt.Errorf("read RIFF header: %w", err)
	}
	if string(riffHdr[:4]) != "RIFF" || string(riffHdr[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a WAV file")
	}

	// Walk chunks until we find "data"
	for {
		var chunkHdr [8]byte
		if _, err := io.ReadFull(f, chunkHdr[:]); err != nil {
			return nil, fmt.Errorf("read chunk header: %w", err)
		}
		chunkID := string(chunkHdr[:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHdr[4:8])

		if chunkID == "data" {
			nSamples := int(chunkSize) / 2
			samples := make([]int16, nSamples)
			if err := binary.Read(f, binary.LittleEndian, samples); err != nil {
				return nil, fmt.Errorf("read PCM: %w", err)
			}
			return samples, nil
		}

		// Skip this chunk (pad to even boundary per RIFF spec)
		skip := int64(chunkSize)
		if chunkSize%2 != 0 {
			skip++
		}
		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return nil, fmt.Errorf("skip chunk %q: %w", chunkID, err)
		}
	}
}

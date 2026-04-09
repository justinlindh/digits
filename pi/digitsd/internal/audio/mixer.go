package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// FrameWriter is the interface the mixer writes to. Playback implements this.
// Using an interface allows mock testing without ALSA hardware.
type FrameWriter interface {
	WriteFrame(samples []int16) error
	PeriodSize() int
}

// Mixer is a single-threaded audio render loop. All playback goes through it.
// External callers set state (PlayLoop, PlayOnce, FeedWebRTC, StopTone).
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
	running bool

	// Loop source: loops named tone until StopTone is called.
	loopSamples []int16
	loopName    string
	loopPos     int

	// One-shot queue: each entry plays once, then is dequeued.
	// Multiple PlayOnce calls queue sequentially.
	onceQueue [][]int16
	oncePos   int

	// WebRTC source: decoded PCM frames from remote peer.
	// Non-blocking read in render loop — frames are dropped if behind.
	webrtcCh chan []int16

	// Loaded tones (name → PCM samples, S16_LE mono 48kHz)
	tones map[string][]int16
}

// NewMixer creates a Mixer that writes to the given FrameWriter.
func NewMixer(w FrameWriter) *Mixer {
	return &Mixer{
		w:        w,
		period:   w.PeriodSize(),
		tones:    make(map[string][]int16),
		webrtcCh: make(chan []int16, 8),
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
	go m.renderLoop(m.stopCh)
	slog.Info("mixer: render loop started")
}

// Stop halts the render loop. Blocks until the goroutine has exited.
func (m *Mixer) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	ch := m.stopCh
	close(ch)
	m.running = false
	m.mu.Unlock()
	// Wait for render loop to drain — it exits on next select check
	slog.Info("mixer: render loop stopped")
}

// renderLoop is the single goroutine that writes to hardware.
// It runs at ALSA's pace: snd_pcm_writei blocks ~20ms per period.
func (m *Mixer) renderLoop(stop chan struct{}) {
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

		// 3. Mix WebRTC audio (non-blocking — drop if channel is empty)
		select {
		case webrtcPCM := <-m.webrtcCh:
			for i := 0; i < len(buf) && i < len(webrtcPCM); i++ {
				buf[i] = clampAdd(buf[i], webrtcPCM[i])
			}
		default:
		}

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

// StopTone stops the looping tone and clears any queued one-shots.
// Takes effect within one period (~20ms).
func (m *Mixer) StopTone() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loopSamples = nil
	m.loopName = ""
	m.loopPos = 0
	// Do NOT clear onceQueue — one-shot tones (DTMF) should survive
	// a loop stop. The main loop calls StopTone then PlayOnce in sequence;
	// clearing onceQueue here would eat the just-queued tone.
}

// StopAll stops all audio: loops, one-shots, and drains the WebRTC channel.
// Use on hang-up to guarantee silence.
func (m *Mixer) StopAll() {
	m.mu.Lock()
	m.loopSamples = nil
	m.loopName = ""
	m.loopPos = 0
	m.onceQueue = nil
	m.oncePos = 0
	m.mu.Unlock()
	// Drain any queued WebRTC frames
	for {
		select {
		case <-m.webrtcCh:
		default:
			return
		}
	}
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

// FeedWebRTC sends decoded PCM from a WebRTC remote track into the mixer.
// Non-blocking: drops the frame if the render loop isn't keeping up.
// Call from the WebRTC track reader goroutine.
func (m *Mixer) FeedWebRTC(samples []int16) {
	select {
	case m.webrtcCh <- samples:
	default:
		// Drop frame — mixer is behind
	}
}

// WebRTCChan returns the WebRTC feed channel for callers that want direct access.
func (m *Mixer) WebRTCChan() chan<- []int16 {
	return m.webrtcCh
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

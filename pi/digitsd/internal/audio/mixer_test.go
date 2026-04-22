package audio

import (
	"encoding/binary"
	"os"
	"testing"
	"time"
)

// mockWriter captures written periods for testing without ALSA hardware.
// Includes a small sleep to simulate ALSA blocking (~1ms per period)
// so time.Sleep-based test synchronization works reliably.
type mockWriter struct {
	periods [][]int16
}

func (m *mockWriter) WriteFrame(samples []int16) error {
	cp := make([]int16, len(samples))
	copy(cp, samples)
	m.periods = append(m.periods, cp)
	time.Sleep(1 * time.Millisecond)
	return nil
}

func (m *mockWriter) PeriodSize() int { return 960 }

// createMinimalWAV writes a minimal WAV file (44-byte header + int16 samples).
func createMinimalWAV(t *testing.T, path string, samples []int16) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	dataSize := len(samples) * 2
	riffSize := 36 + dataSize
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(riffSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(header[22:24], 1)  // mono
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))
	_, _ = f.Write(header)
	_ = binary.Write(f, binary.LittleEndian, samples)
}

// --- Silence / keepalive ---

func TestMixerWritesSilenceWhenIdle(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)
	mx.Start()
	time.Sleep(50 * time.Millisecond) // ~2-3 periods at 20ms each
	mx.Stop()

	if len(w.periods) < 2 {
		t.Fatalf("expected >=2 periods, got %d", len(w.periods))
	}
	// All samples should be zero (silence = keepalive)
	for i, p := range w.periods {
		for j, s := range p {
			if s != 0 {
				t.Fatalf("period %d sample %d: expected 0 (silence), got %d", i, j, s)
			}
		}
	}
}

// --- clampAdd ---

func TestClampAdd(t *testing.T) {
	cases := []struct {
		a, b, want int16
	}{
		{0, 0, 0},
		{100, 200, 300},
		{32767, 1, 32767},    // overflow clamp
		{-32768, -1, -32768}, // underflow clamp
		{1000, -500, 500},
		{-1000, 500, -500},
	}
	for _, c := range cases {
		got := clampAdd(c.a, c.b)
		if got != c.want {
			t.Errorf("clampAdd(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// --- PlayLoop ---

func TestMixerPlayLoopOutputsToneSamples(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)

	// 2 periods of ascending values
	tone := make([]int16, 1920)
	for i := range tone {
		tone[i] = int16(i % 1000)
	}
	mx.LoadTone("test_tone", tone)

	// Queue the loop BEFORE Start so the first rendered period is deterministic.
	// (If queued after Start, the render loop may have already written one or
	// more silence periods before PlayLoop returns, making periods[0] a race.)
	mx.PlayLoop("test_tone")
	mx.Start()
	time.Sleep(50 * time.Millisecond)
	mx.StopTone()
	mx.Stop()

	if len(w.periods) < 2 {
		t.Fatalf("expected >=2 periods, got %d", len(w.periods))
	}

	// First period should match tone[0:960]
	for i := 0; i < 960; i++ {
		if w.periods[0][i] != tone[i] {
			t.Fatalf("period 0 sample %d: expected %d, got %d", i, tone[i], w.periods[0][i])
		}
	}
}

func TestMixerStopToneReturnsToSilence(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)

	tone := make([]int16, 960)
	for i := range tone {
		tone[i] = 1000
	}
	mx.LoadTone("test", tone)

	mx.Start()
	mx.PlayLoop("test")
	time.Sleep(30 * time.Millisecond)
	mx.StopTone()
	time.Sleep(50 * time.Millisecond) // let render loop write silence
	mx.Stop()

	// Last period should be silence
	last := w.periods[len(w.periods)-1]
	for i, s := range last {
		if s != 0 {
			t.Fatalf("last period sample %d: expected 0 (silence after StopTone), got %d", i, s)
		}
	}
}

func TestMixerActiveReturnsLoopName(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)

	tone := make([]int16, 960)
	mx.LoadTone("dialtone", tone)

	mx.Start()
	if mx.Active() != "" {
		t.Errorf("Active() before PlayLoop: expected empty, got %q", mx.Active())
	}
	mx.PlayLoop("dialtone")
	time.Sleep(10 * time.Millisecond)
	if mx.Active() != "dialtone" {
		t.Errorf("Active() after PlayLoop: expected %q, got %q", "dialtone", mx.Active())
	}
	mx.StopTone()
	if mx.Active() != "" {
		t.Errorf("Active() after StopTone: expected empty, got %q", mx.Active())
	}
	mx.Stop()
}

// --- PlayOnce ---

func TestMixerPlayOnce(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)

	// 480-sample tone (half a period) — should finish mid-period
	tone := make([]int16, 480)
	for i := range tone {
		tone[i] = 500
	}
	mx.LoadTone("beep", tone)

	// Queue the one-shot BEFORE Start so the first rendered period is the tone.
	// Otherwise the render loop may emit a silence period before PlayOnce queues.
	mx.PlayOnce("beep")
	mx.Start()
	time.Sleep(50 * time.Millisecond)
	mx.Stop()

	if len(w.periods) < 1 {
		t.Fatal("no periods written")
	}
	p := w.periods[0]
	// First 480 samples should be 500
	if p[0] != 500 {
		t.Errorf("sample 0: expected 500, got %d", p[0])
	}
	if p[479] != 500 {
		t.Errorf("sample 479: expected 500, got %d", p[479])
	}
	// Remaining 480 samples should be 0 (silence after one-shot finishes)
	if p[480] != 0 {
		t.Errorf("sample 480: expected 0 (silence after one-shot), got %d", p[480])
	}
}

// TestMixerStopTonePreservesOnceQueue locks in the fix for the first-keypress
// bug: the daemon layer queues a DTMF beep just before the FSM calls StopTone
// to kill the dial tone loop. StopTone must stop only the loop, never wipe
// the one-shot queue, or the beep is silently dropped before the render tick.
func TestMixerStopTonePreservesOnceQueue(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)

	loop := make([]int16, 960)
	for i := range loop {
		loop[i] = 100
	}
	beep := make([]int16, 480)
	for i := range beep {
		beep[i] = 500
	}
	mx.LoadTone("loop", loop)
	mx.LoadTone("beep", beep)

	// Replay the real sequence that hits on a first keypress in StateDIALTONE:
	// dial tone is looping, daemon queues a DTMF one-shot, then the FSM fires
	// StopTone. Do all three before Start so the first rendered period is
	// deterministic.
	mx.PlayLoop("loop")
	mx.PlayOnce("beep")
	mx.StopTone()

	mx.Start()
	time.Sleep(50 * time.Millisecond)
	mx.Stop()

	if len(w.periods) < 1 {
		t.Fatal("no periods written")
	}
	p := w.periods[0]
	// First 480 samples should be the beep (500), not silence or the loop.
	if p[0] != 500 {
		t.Errorf("sample 0: expected 500 (DTMF beep survived StopTone), got %d", p[0])
	}
	if p[479] != 500 {
		t.Errorf("sample 479: expected 500, got %d", p[479])
	}
	// After the beep ends, we should be silent — the loop must have stopped.
	if p[480] != 0 {
		t.Errorf("sample 480: expected 0 (loop stopped, beep done), got %d", p[480])
	}
}

func TestMixerPlayOnceOverLoop(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)

	loop := make([]int16, 960)
	for i := range loop {
		loop[i] = 100
	}
	once := make([]int16, 960)
	for i := range once {
		once[i] = 200
	}
	mx.LoadTone("loop", loop)
	mx.LoadTone("beep", once)

	mx.Start()
	mx.PlayLoop("loop")
	time.Sleep(25 * time.Millisecond) // let loop start
	mx.PlayOnce("beep")
	time.Sleep(50 * time.Millisecond)
	mx.Stop()

	// Find a period where both are mixed: 100 + 200 = 300
	found := false
	for _, p := range w.periods {
		if p[0] == 300 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find a period with mixed loop+once (300), but didn't")
	}
}

// --- FeedWebRTC ---

func TestMixerWebRTCFeed(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)

	mx.Start()

	// Feed a WebRTC frame via a named source
	ch := mx.AddWebRTCSource("default")
	frame := make([]int16, 960)
	for i := range frame {
		frame[i] = 777
	}
	ch <- frame
	time.Sleep(50 * time.Millisecond)
	mx.Stop()

	// At least one period should contain 777
	found := false
	for _, p := range w.periods {
		if p[0] == 777 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected WebRTC samples (777) in output, but didn't find any")
	}
}

func TestMixerWebRTCMixesWithLoop(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)

	loop := make([]int16, 960)
	for i := range loop {
		loop[i] = 100
	}
	mx.LoadTone("loop", loop)

	mx.Start()
	mx.PlayLoop("loop")
	time.Sleep(25 * time.Millisecond)

	// Feed WebRTC frame via a named source
	ch := mx.AddWebRTCSource("default")
	frame := make([]int16, 960)
	for i := range frame {
		frame[i] = 50
	}
	ch <- frame
	time.Sleep(50 * time.Millisecond)
	mx.Stop()

	// Find a period with 100 + 50 = 150
	found := false
	for _, p := range w.periods {
		if p[0] == 150 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find a period with loop+webrtc mixed (150)")
	}
}

// --- loadWAV ---

func TestLoadWAV(t *testing.T) {
	// Create a minimal WAV file: 44-byte header + 4 samples (8 bytes)
	f, err := os.CreateTemp("", "test*.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	// Write a proper WAV header with fmt and data chunks
	pcm := []byte{
		0x00, 0x00, // 0
		0xE8, 0x03, // 1000
		0x18, 0xFC, // -1000
		0xFF, 0x7F, // 32767
	}
	dataSize := len(pcm)
	riffSize := 36 + dataSize
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(riffSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(header[22:24], 1)  // mono
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))
	_, _ = f.Write(header)
	_, _ = f.Write(pcm)
	_ = f.Close()

	samples, err := loadWAV(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(samples))
	}
	if samples[0] != 0 {
		t.Errorf("sample[0]: expected 0, got %d", samples[0])
	}
	if samples[1] != 1000 {
		t.Errorf("sample[1]: expected 1000, got %d", samples[1])
	}
	if samples[2] != -1000 {
		t.Errorf("sample[2]: expected -1000, got %d", samples[2])
	}
	if samples[3] != 32767 {
		t.Errorf("sample[3]: expected 32767, got %d", samples[3])
	}
}

func TestLoadWAVDir(t *testing.T) {
	// Create a temp directory with two WAV files
	dir, err := os.MkdirTemp("", "tones")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	for _, name := range []string{"tone_dial.wav", "dtmf_1.wav"} {
		f, err := os.Create(dir + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		pcm := []byte{0x00, 0x00, 0xFF, 0x7F}
		hdr := make([]byte, 44)
		copy(hdr[0:4], "RIFF")
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+len(pcm)))
		copy(hdr[8:12], "WAVE")
		copy(hdr[12:16], "fmt ")
		binary.LittleEndian.PutUint32(hdr[16:20], 16)
		binary.LittleEndian.PutUint16(hdr[20:22], 1)
		binary.LittleEndian.PutUint16(hdr[22:24], 1)
		copy(hdr[36:40], "data")
		binary.LittleEndian.PutUint32(hdr[40:44], uint32(len(pcm)))
		_, _ = f.Write(hdr)
		_, _ = f.Write(pcm)
		_ = f.Close()
	}

	// Verify loadWAV works on each file directly
	for _, name := range []string{"tone_dial.wav", "dtmf_1.wav"} {
		samples, err := loadWAV(dir + "/" + name)
		if err != nil {
			t.Errorf("load %s: %v", name, err)
		}
		if len(samples) != 2 {
			t.Errorf("%s: expected 2 samples, got %d", name, len(samples))
		}
	}
}

// --- AddWebRTCSource / RemoveWebRTCSource ---

func TestMixer_TwoWebRTCSources_SumAndClip(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)
	mx.Start()
	defer mx.Stop()

	chA := mx.AddWebRTCSource("peerA")
	chB := mx.AddWebRTCSource("peerB")

	frameA := make([]int16, 960)
	frameB := make([]int16, 960)
	for i := range frameA {
		frameA[i] = 8192
		frameB[i] = 8192
	}
	chA <- frameA
	chB <- frameB

	time.Sleep(50 * time.Millisecond)
	mx.Stop()

	// Find a period where the two sources summed: 8192 + 8192 = 16384
	found := false
	for _, p := range w.periods {
		if p[0] == 16384 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a period with sum 16384 (8192+8192), but didn't find any")
	}
}

func TestMixer_TwoWebRTCSources_Clipping(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)
	mx.Start()
	defer mx.Stop()

	chA := mx.AddWebRTCSource("peerA")
	chB := mx.AddWebRTCSource("peerB")

	frameA := make([]int16, 960)
	frameB := make([]int16, 960)
	for i := range frameA {
		frameA[i] = 30000
		frameB[i] = 30000
	}
	chA <- frameA
	chB <- frameB

	time.Sleep(50 * time.Millisecond)
	mx.Stop()

	// 30000 + 30000 = 60000 which overflows int16 max (32767) — expect saturation
	found := false
	for _, p := range w.periods {
		if p[0] == 32767 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a period with saturated value 32767, but didn't find any")
	}
}

func TestMixer_AddAndRemoveSource(t *testing.T) {
	w := &mockWriter{}
	mx := NewMixer(w)
	mx.Start()
	defer mx.Stop()

	ch := mx.AddWebRTCSource("peerX")
	if ch == nil {
		t.Fatal("expected non-nil channel from AddWebRTCSource")
	}

	// Adding the same key again should return the same channel
	ch2 := mx.AddWebRTCSource("peerX")
	if ch2 != ch {
		t.Error("expected same channel for duplicate key")
	}

	mx.RemoveWebRTCSource("peerX")

	// After removal, a new AddWebRTCSource with the same key should return a fresh channel
	ch3 := mx.AddWebRTCSource("peerX")
	if ch3 == nil {
		t.Fatal("expected non-nil channel after re-adding")
	}
	if ch3 == ch {
		t.Error("expected fresh channel after remove+re-add, but got the same channel")
	}
	mx.RemoveWebRTCSource("peerX")
}

// --- LoadTonesFromDir ---

func TestMixerLoadTonesFromDir(t *testing.T) {
	dir := t.TempDir()

	// Create two minimal WAV files
	createMinimalWAV(t, dir+"/tone_dial.wav", []int16{100, 200})
	createMinimalWAV(t, dir+"/dtmf_1.wav", []int16{300, 400})

	w := &mockWriter{}
	mx := NewMixer(w)

	if err := mx.LoadTonesFromDir(dir); err != nil {
		t.Fatalf("LoadTonesFromDir: %v", err)
	}

	// Both tones should be loaded
	mx.mu.Lock()
	_, hasDial := mx.tones["tone_dial"]
	_, hasDTMF := mx.tones["dtmf_1"]
	mx.mu.Unlock()

	if !hasDial {
		t.Error("tone_dial not loaded")
	}
	if !hasDTMF {
		t.Error("dtmf_1 not loaded")
	}
}

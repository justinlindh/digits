package phone

import (
	"log/slog"
	"sync"
	"time"
)

type timedKey struct {
	key  string
	when time.Time
}

// EasterEgg defines a keypad sequence that triggers an audio clip.
type EasterEgg struct {
	Name    string // for logging
	Trigger string // key sequence to match
	Clip    string // WAV name to play (loaded by mixer)
}

// EasterEggDetector watches keypress timing to detect easter egg sequences.
// Each key must arrive within MinGap–MaxGap of the previous one, and the
// sequence must be the very first keys pressed in a dialing session (no
// suffix matching).
type EasterEggDetector struct {
	mu      sync.Mutex
	eggs    []EasterEgg
	maxLen  int // longest trigger length, computed once at construction
	buf     []timedKey
	tainted bool // set when non-egg keys precede; cleared by Reset
	play    func(clip string)

	MaxGap time.Duration
	MinGap time.Duration
}

// NewEasterEggDetector creates a detector with the given eggs and playback callback.
func NewEasterEggDetector(eggs []EasterEgg, play func(clip string)) *EasterEggDetector {
	maxLen := 0
	for _, e := range eggs {
		if len(e.Trigger) > maxLen {
			maxLen = len(e.Trigger)
		}
	}
	return &EasterEggDetector{
		eggs:   eggs,
		maxLen: maxLen,
		play:   play,
		MaxGap: 5 * time.Second,
		MinGap: 100 * time.Millisecond,
	}
}

// AddKey processes a keypress. Returns true if an easter egg was triggered.
// The sequence must be the very first keys pressed in a dialing session:
// typing any non-matching prefix taints the detector until the next Reset.
func (d *EasterEggDetector) AddKey(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tainted {
		return false
	}

	now := time.Now()

	if len(d.buf) > 0 {
		gap := now.Sub(d.buf[len(d.buf)-1].when)
		if gap > d.MaxGap || gap < d.MinGap {
			d.tainted = true
			d.buf = d.buf[:0]
			return false
		}
	}

	d.buf = append(d.buf, timedKey{key: key, when: now})

	if len(d.buf) > d.maxLen {
		d.tainted = true
		d.buf = d.buf[:0]
		return false
	}

	seq := d.sequence()

	for _, e := range d.eggs {
		if seq == e.Trigger {
			d.buf = d.buf[:0]
			slog.Info("phone: easter egg detected", "name", e.Name)
			if d.play != nil {
				clip := e.Clip
				go d.play(clip)
			}
			return true
		}
	}

	return false
}

// Reset clears the buffer and tainted flag (e.g., on hang-up).
func (d *EasterEggDetector) Reset() {
	d.mu.Lock()
	d.buf = d.buf[:0]
	d.tainted = false
	d.mu.Unlock()
}

func (d *EasterEggDetector) sequence() string {
	s := make([]byte, len(d.buf))
	for i, tk := range d.buf {
		if len(tk.key) > 0 {
			s[i] = tk.key[0]
		}
	}
	return string(s)
}

// DialEasterEggs maps 7-digit phone numbers to audio clips.
// Checked when a DIAL:XXXXXXX event is received, before placing a call.
var DialEasterEggs = map[string]EasterEgg{
	"8675309": {Name: "Jenny", Trigger: "8675309", Clip: "jenny"},
}

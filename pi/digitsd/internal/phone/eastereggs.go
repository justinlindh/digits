package phone

import (
	"log"
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
// Each key must arrive within MinGap–MaxGap of the previous one.
type EasterEggDetector struct {
	mu   sync.Mutex
	eggs []EasterEgg
	buf  []timedKey
	play func(clip string) // callback to play a clip

	MaxGap time.Duration
	MinGap time.Duration
}

// NewEasterEggDetector creates a detector with the given eggs and playback callback.
func NewEasterEggDetector(eggs []EasterEgg, play func(clip string)) *EasterEggDetector {
	maxTrigger := 0
	for _, e := range eggs {
		if len(e.Trigger) > maxTrigger {
			maxTrigger = len(e.Trigger)
		}
	}
	return &EasterEggDetector{
		eggs:   eggs,
		play:   play,
		MaxGap: 1500 * time.Millisecond,
		MinGap: 100 * time.Millisecond,
	}
}

// AddKey processes a keypress. Returns true if an easter egg was triggered.
func (d *EasterEggDetector) AddKey(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// Check timing against last entry
	if len(d.buf) > 0 {
		gap := now.Sub(d.buf[len(d.buf)-1].when)
		if gap > d.MaxGap || gap < d.MinGap {
			d.buf = d.buf[:0]
		}
	}

	d.buf = append(d.buf, timedKey{key: key, when: now})

	// Keep buffer trimmed to longest trigger
	maxLen := 0
	for _, e := range d.eggs {
		if len(e.Trigger) > maxLen {
			maxLen = len(e.Trigger)
		}
	}
	if len(d.buf) > maxLen {
		d.buf = d.buf[len(d.buf)-maxLen:]
	}

	seq := d.sequence()

	// Check each egg (longest triggers first to avoid partial false matches)
	for _, e := range d.eggs {
		tLen := len(e.Trigger)
		if len(seq) >= tLen && seq[len(seq)-tLen:] == e.Trigger {
			d.buf = d.buf[:0]
			log.Printf("phone: 🎶 Easter egg: %s detected!", e.Name)
			if d.play != nil {
				clip := e.Clip
				go d.play(clip)
			}
			return true
		}
	}

	return false
}

// Reset clears the buffer (e.g., on hang-up).
func (d *EasterEggDetector) Reset() {
	d.mu.Lock()
	d.buf = d.buf[:0]
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

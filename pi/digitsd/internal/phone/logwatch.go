package phone

import (
	"bufio"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// LogWatcher tails a UART log file and emits parsed RX events.
type LogWatcher struct {
	events chan string
	stop   chan struct{}
}

// NewLogWatcher opens path, seeks to end, and starts a background tail goroutine.
func NewLogWatcher(path string) (*LogWatcher, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}

	w := &LogWatcher{
		events: make(chan string, 64),
		stop:   make(chan struct{}),
	}

	go w.tailLoop(f)
	return w, nil
}

// Events returns the channel of parsed RX event strings.
func (w *LogWatcher) Events() <-chan string {
	return w.events
}

// Stop shuts down the watcher goroutine.
func (w *LogWatcher) Stop() {
	close(w.stop)
}

// tailLoop reads lines from f, parsing and forwarding RX events.
func (w *LogWatcher) tailLoop(f *os.File) {
	defer f.Close()
	r := bufio.NewReader(f)

	for {
		// Non-blocking stop check.
		select {
		case <-w.stop:
			return
		default:
		}

		line, err := r.ReadString('\n')
		if err != nil {
			// EOF or no data yet — sleep and retry.
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// Parse log format: "2026-03-22 20:00:00 RX: HOOK:OFF"
		// Also supports pipe-delimited: "2026-03-22 20:00:00 | RX | HOOK:OFF"
		line = strings.TrimSpace(line)
		var direction, event string
		if strings.Contains(line, " | ") {
			parts := strings.Split(line, " | ")
			if len(parts) < 3 {
				continue
			}
			direction = strings.TrimSpace(parts[1])
			event = strings.TrimSpace(parts[2])
		} else {
			// Space-delimited: "2026-03-22 20:00:00 RX: HOOK:OFF"
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			// fields[0]=date, fields[1]=time, fields[2]=direction (TX:/RX:), fields[3:]=event
			direction = strings.TrimSuffix(fields[2], ":")
			event = strings.Join(fields[3:], ":")
		}

		if direction != "RX" {
			continue
		}
		if event == "" {
			continue
		}

		select {
		case w.events <- event:
		default:
			log.Printf("logwatch: events channel full, dropping event: %s", event)
		}
	}
}

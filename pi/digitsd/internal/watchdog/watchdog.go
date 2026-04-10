package watchdog

import (
	"log"
	"os"
	"sync"
	"time"
)

type Watchdog struct {
	f      *os.File
	done   chan struct{}
	closed sync.Once
}

func Open(path string) (*Watchdog, error) {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	return &Watchdog{f: f, done: make(chan struct{})}, nil
}

func (w *Watchdog) Pet() error {
	_, err := w.f.Write([]byte("1"))
	return err
}

func (w *Watchdog) Start(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := w.Pet(); err != nil {
					log.Printf("watchdog: pet failed: %v", err)
				}
			case <-w.done:
				return
			}
		}
	}()
}

func (w *Watchdog) Close() {
	w.closed.Do(func() {
		close(w.done)
		_, _ = w.f.Write([]byte("V"))
		_ = w.f.Close()
	})
}

package email

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type blockingSender struct {
	mu      sync.Mutex
	release chan struct{}
	sent    []string
	err     error
}

func (b *blockingSender) Send(to, subject, htmlBody string) error {
	<-b.release
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, to)
	return b.err
}

// Send must return before the wrapped sender finishes, so a caller's
// response time does not depend on whether mail was actually sent.
func TestAsyncSender_ReturnsWithoutWaiting(t *testing.T) {
	inner := &blockingSender{release: make(chan struct{})}
	s := NewAsyncSender(inner)

	done := make(chan error, 1)
	go func() { done <- s.Send("a@example.com", "s", "b") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Send returned error %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Send blocked on the wrapped sender")
	}

	close(inner.release)
	s.Wait()
	inner.mu.Lock()
	defer inner.mu.Unlock()
	if len(inner.sent) != 1 || inner.sent[0] != "a@example.com" {
		t.Errorf("wrapped sender got %v, want the one message", inner.sent)
	}
}

// A wrapped-sender failure cannot reach the caller any more; it is logged.
// This only checks the wrapper does not panic or block on it.
func TestAsyncSender_SwallowsWrappedError(t *testing.T) {
	inner := &blockingSender{release: make(chan struct{}), err: errors.New("smtp down")}
	close(inner.release)
	s := NewAsyncSender(inner)
	if err := s.Send("a@example.com", "s", "b"); err != nil {
		t.Fatalf("Send returned %v, want nil", err)
	}
	s.Wait()
}

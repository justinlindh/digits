package email

import (
	"log/slog"
	"sync"
)

// AsyncSender hands each message to the wrapped Sender on its own goroutine
// and returns at once. Callers that must answer in constant time regardless
// of whether mail went out (the login form, which must not reveal which
// addresses it knows) wrap their real sender in this. Delivery failures are
// logged rather than returned, since no caller is waiting for them.
type AsyncSender struct {
	inner Sender
	wg    sync.WaitGroup
}

// NewAsyncSender wraps inner.
func NewAsyncSender(inner Sender) *AsyncSender {
	return &AsyncSender{inner: inner}
}

// Send queues the message and returns nil immediately.
func (a *AsyncSender) Send(to, subject, htmlBody string) error {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := a.inner.Send(to, subject, htmlBody); err != nil {
			slog.Error("async email send failed", "err", err)
		}
	}()
	return nil
}

// Wait blocks until every queued message has been handed to the wrapped
// sender. Used at shutdown and in tests.
func (a *AsyncSender) Wait() {
	a.wg.Wait()
}

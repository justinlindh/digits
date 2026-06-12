package events_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/events"
)

func TestBroadcaster_NotifyDeliversToSingleSubscriber(t *testing.T) {
	b := events.New()
	ch, unsub := b.Subscribe()
	t.Cleanup(unsub)

	b.Notify()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected notification within 1s")
	}
}

func TestBroadcaster_NotifyFanOutsToAllSubscribers(t *testing.T) {
	b := events.New()
	ch1, unsub1 := b.Subscribe()
	t.Cleanup(unsub1)
	ch2, unsub2 := b.Subscribe()
	t.Cleanup(unsub2)

	b.Notify()

	for i, ch := range []<-chan struct{}{ch1, ch2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive notification", i)
		}
	}
}

func TestBroadcaster_UnsubscribeStopsDelivery(t *testing.T) {
	b := events.New()
	ch, unsub := b.Subscribe()
	unsub()

	b.Notify()

	select {
	case <-ch:
		t.Fatal("expected no notification after unsubscribe")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBroadcaster_NotifyCoalescesWhenBufferFull(t *testing.T) {
	b := events.New()
	ch, unsub := b.Subscribe()
	t.Cleanup(unsub)

	// First Notify fills the buffer-1 channel.
	b.Notify()
	// Subsequent Notifies must not block; they coalesce into the existing pending wake.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Notify()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify blocked when subscriber buffer was full")
	}

	// Subscriber sees exactly one wake (coalesced).
	<-ch
	select {
	case <-ch:
		t.Fatal("expected coalesced single wake, got multiple")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBroadcaster_ConcurrentSubscribeNotifyUnsubscribe(t *testing.T) {
	b := events.New()
	var received int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsub := b.Subscribe()
			defer unsub()
			deadline := time.After(200 * time.Millisecond)
			for {
				select {
				case <-ch:
					atomic.AddInt64(&received, 1)
				case <-deadline:
					return
				}
			}
		}()
	}

	notifyDone := make(chan struct{})
	go func() {
		defer close(notifyDone)
		for i := 0; i < 200; i++ {
			b.Notify()
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
	<-notifyDone
	if atomic.LoadInt64(&received) == 0 {
		t.Fatal("expected at least one delivered notification under concurrency")
	}
}

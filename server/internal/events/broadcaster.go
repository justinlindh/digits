// Package events provides a tiny in-process pub/sub broadcaster used to
// signal "dashboard state may have changed" without carrying any payload.
//
// Sources (calls.Tracker, signaling.Hub) call Notify after mutating
// state. Each subscriber's channel has buffer size 1, and Notify performs
// a non-blocking send: bursts of notifications coalesce into a single
// pending wake, so subscribers see at most one signal per outstanding
// state change. Subscribers re-snapshot their own view on wake.
//
// When Redis is configured via SetRedis, Notify also publishes to a shared
// channel so that other pods receive cross-pod wake signals. RunRedis
// subscribes to that channel and wakes local subscribers on messages from
// other pods, filtering self by comparing the payload to podID.
package events

import (
	"context"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"
)

const notifyChannel = "digits:notify"

// Broadcaster fans out content-free wake signals to subscribers.
//
// Zero value is not usable; use New.
type Broadcaster struct {
	mu     sync.Mutex
	subs   map[chan struct{}]struct{}
	client redis.UniversalClient
	podID  string
}

// New returns an empty Broadcaster ready for use.
func New() *Broadcaster {
	return &Broadcaster{subs: make(map[chan struct{}]struct{})}
}

// SetRedis configures Redis pub/sub for cross-pod notification.
// Pass nil to disable (single-instance mode).
func (b *Broadcaster) SetRedis(client redis.UniversalClient, podID string) {
	b.mu.Lock()
	b.client = client
	b.podID = podID
	b.mu.Unlock()
}

// RunRedis subscribes to the Redis notify channel and wakes local
// subscribers on messages from other pods. Returns when ctx is cancelled
// or the subscription channel closes. No-op if SetRedis was not called.
func (b *Broadcaster) RunRedis(ctx context.Context) {
	b.mu.Lock()
	client := b.client
	podID := b.podID
	b.mu.Unlock()
	if client == nil {
		return
	}

	sub := client.Subscribe(ctx, notifyChannel)
	defer func() { _ = sub.Close() }()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if msg.Payload == podID {
				continue
			}
			b.notifyLocal()
		}
	}
}

// Subscribe registers a new subscriber and returns its receive channel
// plus an unsubscribe function. The channel has buffer size 1; bursts of
// Notify calls collapse into a single pending wake.
func (b *Broadcaster) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// Notify wakes every local subscriber and publishes to Redis so other
// pods wake their subscribers too. Best-effort, non-blocking: if a
// subscriber's buffer is full (a previous wake is already pending), the
// send is dropped, and the subscriber will pick up the latest state on
// its next loop iteration.
func (b *Broadcaster) Notify() {
	b.mu.Lock()
	client := b.client
	podID := b.podID
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	b.mu.Unlock()

	if client != nil {
		if err := client.Publish(context.Background(), notifyChannel, podID).Err(); err != nil {
			slog.Error("redis: dashboard notify publish failed", "err", err)
		}
	}
}

// notifyLocal wakes local subscribers without publishing to Redis,
// avoiding echo loops when processing inbound Redis messages.
func (b *Broadcaster) notifyLocal() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

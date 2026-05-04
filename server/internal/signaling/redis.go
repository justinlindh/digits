package signaling

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

const redisChannel = "digits:signal"

// Envelope is the wire format for messages published to the Redis pub/sub
// channel. Each pod publishes when a local lookup misses, and subscribing
// pods attempt local delivery.
type Envelope struct {
	PodID      string   `json:"pod"`
	TargetType string   `json:"type"`   // "number", "hardware", or "broadcast"
	Target     string   `json:"target"` // phone number, hardware ID, or empty for broadcast
	Message    *Message `json:"msg"`
}

// redisPubSub is the interface the Hub uses for cross-pod messaging. The
// concrete implementation is RedisBridge; tests may substitute a fake.
type redisPubSub interface {
	Publish(ctx context.Context, env *Envelope)
	Subscribe(ctx context.Context) <-chan *Envelope
}

// RedisBridge wraps a Redis pub/sub connection for cross-pod signaling.
// The zero value is not usable; create with NewRedisBridge.
type RedisBridge struct {
	client *redis.Client
	podID  string

	published atomic.Int64
	received  atomic.Int64
}

// compile-time check
var _ redisPubSub = (*RedisBridge)(nil)

// NewRedisBridge connects to Redis at the given URL and returns a bridge
// ready for use. The URL should be a redis:// or rediss:// connection
// string. Call Close when done.
func NewRedisBridge(redisURL string) (*RedisBridge, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)

	podID, _ := os.Hostname()
	if podID == "" {
		podID = "unknown"
	}

	return &RedisBridge{
		client: client,
		podID:  podID,
	}, nil
}

// Publish sends an envelope to the Redis channel. Errors are logged but
// not returned because delivery is best-effort (the target may be offline
// on all pods).
func (b *RedisBridge) Publish(ctx context.Context, env *Envelope) {
	env.PodID = b.podID
	data, err := json.Marshal(env)
	if err != nil {
		slog.Error("redis: marshal envelope failed", "err", err)
		return
	}
	if err := b.client.Publish(ctx, redisChannel, data).Err(); err != nil {
		slog.Error("redis: publish failed", "err", err)
		return
	}
	b.published.Add(1)
	slog.Debug("redis: published message", "pod", b.podID)
}

// Subscribe returns a channel that yields envelopes from other pods.
// Messages originating from this pod are filtered out. The returned
// channel is closed when ctx is cancelled or the subscription ends.
func (b *RedisBridge) Subscribe(ctx context.Context) <-chan *Envelope {
	ch := make(chan *Envelope, 64)
	sub := b.client.Subscribe(ctx, redisChannel)

	go func() {
		defer close(ch)
		defer func() { _ = sub.Close() }()

		redisCh := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case redisMsg, ok := <-redisCh:
				if !ok {
					return
				}
				var env Envelope
				if err := json.Unmarshal([]byte(redisMsg.Payload), &env); err != nil {
					slog.Warn("redis: unmarshal envelope failed", "err", err)
					continue
				}
				// Skip messages from this pod to avoid echo.
				if env.PodID == b.podID {
					continue
				}
				b.received.Add(1)
				ch <- &env
			}
		}
	}()

	return ch
}

// Published returns the total number of messages published by this bridge.
func (b *RedisBridge) Published() int64 {
	return b.published.Load()
}

// Received returns the total number of messages received (from other pods)
// by this bridge.
func (b *RedisBridge) Received() int64 {
	return b.received.Load()
}

// Close shuts down the Redis client connection.
func (b *RedisBridge) Close() error {
	return b.client.Close()
}

// Ping verifies the Redis connection is alive.
func (b *RedisBridge) Ping(ctx context.Context) error {
	return b.client.Ping(ctx).Err()
}

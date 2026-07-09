package signaling

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisChannel = "digits:signal"

// Envelope is the wire format for messages published to the Redis pub/sub
// channel. Each pod publishes when a local lookup misses, and subscribing
// pods attempt local delivery.
type Envelope struct {
	PodID      string   `json:"pod"`
	TargetType string   `json:"type"`   // "number", "hardware", "broadcast", or "reconnect"
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
	client redis.UniversalClient
	podID  string
}

// compile-time check
var _ redisPubSub = (*RedisBridge)(nil)

// NewRedisBridge connects to Redis and returns a bridge ready for use.
// It supports two modes:
//
//   - Standard: pass a redis:// or rediss:// URL.
//   - Sentinel: pass a comma-separated list of sentinel addresses as the URL
//     with the master name in REDIS_SENTINEL_MASTER.
//     Format: "sentinel-0:26379,sentinel-1:26379,sentinel-2:26379"
//
// The mode is selected by the presence of REDIS_SENTINEL_MASTER in the
// environment. When set, redisURL is treated as a comma-separated sentinel
// address list. When unset, redisURL is parsed as a standard Redis URL.
// In sentinel mode, connection is lazy; callers must Ping to verify
// reachability.
func NewRedisBridge(redisURL string) (*RedisBridge, error) {
	var client redis.UniversalClient

	if master := os.Getenv("REDIS_SENTINEL_MASTER"); master != "" {
		addrs := sentinelAddrs(redisURL)
		client = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    master,
			SentinelAddrs: addrs,
		})
		slog.Info("redis: using sentinel failover", "master", master, "sentinels", addrs)
	} else {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			return nil, err
		}
		client = redis.NewClient(opts)
	}

	podID, _ := os.Hostname() //nolint:errcheck // empty hostname is fine
	if podID == "" {
		podID = "unknown"
	}

	return &RedisBridge{
		client: client,
		podID:  podID,
	}, nil
}

// rateLimitRedisTimeout caps a single rate-limit round-trip at the socket level.
// The rate limiter sits on the synchronous auth and WebSocket-upgrade paths, so
// its Redis client is tuned far tighter than the bridge's defaults: a
// slow-but-reachable Redis should fail open in a fraction of a second rather
// than stall a login for the multi-second default read timeout.
const rateLimitRedisTimeout = 500 * time.Millisecond

// NewRateLimitRedisClient builds a Redis client dedicated to the rate limiter,
// separate from the signaling bridge's client. Two things make it hot-path safe:
// ContextTimeoutEnabled makes go-redis honor per-call context deadlines (it
// ignores them by default, so the limiter's own timeout would otherwise be
// inert), and the tight dial/read/write timeouts bound each socket operation.
// Because the pool is its own, a stalled Redis check can neither block nor
// exhaust the connections the bridge and state stores share. Mode selection
// (standard URL vs sentinel) mirrors NewRedisBridge.
func NewRateLimitRedisClient(redisURL string) (redis.UniversalClient, error) {
	if master := os.Getenv("REDIS_SENTINEL_MASTER"); master != "" {
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:            master,
			SentinelAddrs:         sentinelAddrs(redisURL),
			ContextTimeoutEnabled: true,
			DialTimeout:           rateLimitRedisTimeout,
			ReadTimeout:           rateLimitRedisTimeout,
			WriteTimeout:          rateLimitRedisTimeout,
		}), nil
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	opts.ContextTimeoutEnabled = true
	opts.DialTimeout = rateLimitRedisTimeout
	opts.ReadTimeout = rateLimitRedisTimeout
	opts.WriteTimeout = rateLimitRedisTimeout
	return redis.NewClient(opts), nil
}

// sentinelAddrs parses a comma-separated sentinel address list, trimming spaces.
func sentinelAddrs(list string) []string {
	addrs := strings.Split(list, ",")
	for i := range addrs {
		addrs[i] = strings.TrimSpace(addrs[i])
	}
	return addrs
}

// Publish sends an envelope to the Redis channel. Errors are logged but
// not returned because delivery is best-effort (the target may be offline
// on all pods).
func (b *RedisBridge) Publish(ctx context.Context, env *Envelope) {
	env.PodID = b.podID
	data, err := json.Marshal(env)
	if err != nil {
		slog.ErrorContext(ctx, "redis: marshal envelope failed", "err", err)
		return
	}
	if err := b.client.Publish(ctx, redisChannel, data).Err(); err != nil {
		slog.ErrorContext(ctx, "redis: publish failed", "err", err)
		return
	}
	slog.DebugContext(ctx, "redis: published message", "pod", b.podID)
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
					slog.WarnContext(ctx, "redis: unmarshal envelope failed", "err", err)
					continue
				}
				// Skip messages from this pod to avoid echo.
				if env.PodID == b.podID {
					continue
				}
				// Guard the send so a stalled consumer (full buffer) cannot
				// pin this goroutine open past context cancellation.
				select {
				case ch <- &env:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch
}

// Close shuts down the Redis client connection.
func (b *RedisBridge) Close() error {
	return b.client.Close()
}

// Ping verifies the Redis connection is alive.
func (b *RedisBridge) Ping(ctx context.Context) error {
	return b.client.Ping(ctx).Err()
}

// Client returns the underlying Redis client for shared use by state stores.
func (b *RedisBridge) Client() redis.UniversalClient {
	return b.client
}

// PodID returns this bridge's pod identifier.
func (b *RedisBridge) PodID() string {
	return b.podID
}

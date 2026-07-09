// Package ratelimit provides IP-based rate limiting for sensitive HTTP
// endpoints (auth, pairing, invite, WebSocket upgrade) to blunt brute-force
// and enumeration attacks.
//
// Two backends implement the same behavior:
//
//   - An in-memory fixed-window token bucket, scoped to a single process. This
//     is the zero-config default and is all that dev and single-replica
//     deployments need.
//   - A Redis-backed fixed-window counter shared across replicas. Production
//     runs multiple signald pods behind a load balancer, so a per-process
//     limiter multiplies the effective limit by the replica count and can be
//     evaded by spraying requests across pods. The Redis backend keeps one
//     counter per IP in the shared store so the configured limit holds for the
//     whole fleet.
//
// Callers construct a limiter with New and pick the backend by whether a Redis
// client is supplied in the Config. Both backends are wrapped by the same
// Middleware, which resolves the client IP, returns 429 on rejection, and
// records a rejection metric.
package ratelimit

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/justinlindh/digits/server/internal/httputil"
)

// checker decides whether a single request from ip is within limit. It is the
// swappable backend behind the shared HTTP plumbing in Limiter: an in-memory
// counter for single-process deployments, or a Redis counter shared across
// replicas.
type checker interface {
	allow(ctx context.Context, ip string) bool
}

// Config parameterizes a limiter. A zero Redis field selects the in-memory
// backend, so dev and single-replica deployments work with no extra wiring.
type Config struct {
	// Name identifies the endpoint group. It is the label value on the
	// rejection metric, so it must come from the closed set the metrics
	// package allows (see metrics.ObserveRateLimitRejection).
	Name string
	// Limit is the maximum number of requests permitted per Window per IP.
	Limit int
	// Window is the fixed-window length.
	Window time.Duration
	// TrustedProxies is the reverse-proxy hop count used to resolve the client
	// IP from X-Forwarded-For (see httputil.ClientIP).
	TrustedProxies int
	// Redis, when non-nil, selects the shared Redis backend. When nil the
	// in-memory backend is used.
	Redis redis.UniversalClient
	// OnReject, when non-nil, is called with Name each time a request is
	// rejected. The web handler wires this to the Prometheus rejection counter;
	// keeping it a callback lets this package stay free of a metrics import.
	OnReject func(name string)
}

// New builds a Limiter using the Redis backend when cfg.Redis is set and the
// in-memory backend otherwise.
func New(cfg Config) *Limiter {
	l := &Limiter{
		name:           cfg.Name,
		trustedProxies: cfg.TrustedProxies,
		onReject:       cfg.OnReject,
	}
	if cfg.Redis != nil {
		l.check = newRedisChecker(cfg.Redis, cfg.Name, cfg.Limit, cfg.Window)
	} else {
		l.check = newMemoryChecker(cfg.Limit, cfg.Window)
	}
	return l
}

// Limiter is the HTTP plumbing shared by both backends. The backend (in-memory
// or Redis-backed) is the injected checker, so the request path is identical
// regardless of where the counts live.
type Limiter struct {
	check          checker
	name           string
	trustedProxies int
	onReject       func(name string)
}

// Middleware returns an http.Handler that rejects requests over the rate limit
// with 429 and records a rejection via OnReject.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := httputil.ClientIP(r, l.trustedProxies)
		if !l.check.allow(r.Context(), ip) {
			if l.onReject != nil {
				l.onReject(l.name)
			}
			http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- In-memory backend ---

type bucket struct {
	tokens    int
	lastReset time.Time
}

// evictInterval is how often stale buckets are swept. Eviction happens lazily
// inside allow rather than on a background goroutine, so a memoryChecker holds
// no resources beyond its map.
const evictInterval = 5 * time.Minute

// memoryChecker is an in-process fixed-window token bucket keyed by IP.
type memoryChecker struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	limit     int
	window    time.Duration
	lastEvict time.Time
}

func newMemoryChecker(limit int, window time.Duration) *memoryChecker {
	return &memoryChecker{
		buckets:   make(map[string]*bucket),
		limit:     limit,
		window:    window,
		lastEvict: time.Now(),
	}
}

// allow reports whether ip is within its rate limit. The context is unused; the
// in-memory backend needs no I/O.
func (c *memoryChecker) allow(_ context.Context, ip string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if now.Sub(c.lastEvict) >= evictInterval {
		c.evictExpiredLocked(now)
		c.lastEvict = now
	}
	b, ok := c.buckets[ip]
	if !ok || now.Sub(b.lastReset) >= c.window {
		c.buckets[ip] = &bucket{tokens: c.limit - 1, lastReset: now}
		return true
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// evictExpiredLocked removes buckets whose window has lapsed. Callers must hold
// c.mu.
func (c *memoryChecker) evictExpiredLocked(now time.Time) {
	for ip, b := range c.buckets {
		if now.Sub(b.lastReset) >= c.window {
			delete(c.buckets, ip)
		}
	}
}

// --- Redis backend ---

// incrExpireScript implements one atomic fixed-window step: increment the IP's
// counter and, on the first hit of a fresh window, set the window's TTL. Doing
// the INCR and PEXPIRE in one script closes the race where a crash between the
// two would leave a counter with no expiry, wedging the IP until manual
// eviction.
var incrExpireScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
	redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return current
`)

// redisChecker is a fixed-window counter shared across replicas via Redis.
type redisChecker struct {
	client   redis.UniversalClient
	name     string
	keyBase  string
	limit    int
	windowMS int64
}

func newRedisChecker(client redis.UniversalClient, name string, limit int, window time.Duration) *redisChecker {
	return &redisChecker{
		client:   client,
		name:     name,
		keyBase:  "ratelimit:" + name + ":",
		limit:    limit,
		windowMS: window.Milliseconds(),
	}
}

// allow atomically increments the shared per-IP counter and reports whether the
// request is within limit.
//
// On any Redis error we FAIL OPEN: the request is allowed. Rate limiting here
// is a protective backstop against brute-force and enumeration, not a
// correctness gate. If the shared store is unreachable, rejecting every auth,
// pairing, invite, and WebSocket request would convert a cache outage into a
// full user-facing outage: a self-inflicted denial of service that hurts
// legitimate users far more than the abuse the limiter guards against. Redis
// outages are short and rare relative to the always-on auth surface they would
// otherwise take down, so allowing traffic through for the duration is the
// deliberate trade. The error is logged so an outage is still observable.
func (c *redisChecker) allow(ctx context.Context, ip string) bool {
	n, err := incrExpireScript.Run(ctx, c.client, []string{c.keyBase + ip}, c.windowMS).Int64()
	if err != nil {
		slog.WarnContext(ctx, "ratelimit: redis check failed, allowing request", "limiter", c.name, "err", err)
		return true
	}
	return n <= int64(c.limit)
}

// server/internal/ratelimit/ratelimit_test.go
package ratelimit

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// --- In-memory backend ---

func TestMemoryAllowsUnderLimit(t *testing.T) {
	c := newMemoryChecker(5, time.Minute)
	for i := range 5 {
		if !c.allow(context.Background(), "1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestMemoryBlocksOverLimit(t *testing.T) {
	c := newMemoryChecker(3, time.Minute)
	for range 3 {
		c.allow(context.Background(), "1.2.3.4")
	}
	if c.allow(context.Background(), "1.2.3.4") {
		t.Fatal("4th request should be blocked")
	}
}

func TestMemoryTracksIPsSeparately(t *testing.T) {
	c := newMemoryChecker(1, time.Minute)
	if !c.allow(context.Background(), "1.1.1.1") {
		t.Fatal("first IP should be allowed")
	}
	if !c.allow(context.Background(), "2.2.2.2") {
		t.Fatal("second IP should be allowed")
	}
	if c.allow(context.Background(), "1.1.1.1") {
		t.Fatal("first IP should be blocked after limit")
	}
}

func TestMemoryRefillsAfterWindow(t *testing.T) {
	c := newMemoryChecker(1, 50*time.Millisecond)
	c.allow(context.Background(), "1.2.3.4")
	if c.allow(context.Background(), "1.2.3.4") {
		t.Fatal("should be blocked immediately")
	}
	time.Sleep(60 * time.Millisecond)
	if !c.allow(context.Background(), "1.2.3.4") {
		t.Fatal("should be allowed after window")
	}
}

func TestMemoryEvictExpiredRemovesStaleEntries(t *testing.T) {
	c := newMemoryChecker(1, 50*time.Millisecond)
	c.allow(context.Background(), "stale-ip")
	time.Sleep(60 * time.Millisecond)
	c.mu.Lock()
	c.evictExpiredLocked(time.Now())
	_, exists := c.buckets["stale-ip"]
	c.mu.Unlock()
	if exists {
		t.Fatal("stale entry should have been cleaned up")
	}
}

func TestMemoryAllowEvictsLazily(t *testing.T) {
	c := newMemoryChecker(1, 50*time.Millisecond)
	c.allow(context.Background(), "stale-ip")
	time.Sleep(60 * time.Millisecond)
	// Force the next allow to run an eviction sweep.
	c.mu.Lock()
	c.lastEvict = time.Now().Add(-evictInterval)
	c.mu.Unlock()
	c.allow(context.Background(), "fresh-ip")
	c.mu.Lock()
	_, exists := c.buckets["stale-ip"]
	c.mu.Unlock()
	if exists {
		t.Fatal("stale entry should have been evicted by allow")
	}
}

// --- Middleware ---

func TestMiddleware429(t *testing.T) {
	lim := New(Config{Name: "test", Limit: 1, Window: time.Minute, TrustedProxies: 1})
	handler := lim.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/auth/magic", nil)
	req.RemoteAddr = "1.2.3.4:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", w.Code)
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d, want 429", w.Code)
	}
}

func TestMiddlewareRespectsXForwardedFor(t *testing.T) {
	lim := New(Config{Name: "test", Limit: 1, Window: time.Minute, TrustedProxies: 1})
	handler := lim.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "99.99.99.99")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", w.Code)
	}

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d, want 429", w.Code)
	}

	req2 := httptest.NewRequest("POST", "/", nil)
	req2.RemoteAddr = "5.5.5.5:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req2)
	if w.Code != http.StatusOK {
		t.Fatalf("different IP: got %d, want 200", w.Code)
	}
}

func TestMiddlewareCallsOnRejectOnlyWhenRejected(t *testing.T) {
	var rejected []string
	lim := New(Config{
		Name: "auth_magic", Limit: 1, Window: time.Minute, TrustedProxies: 1,
		OnReject: func(name string) { rejected = append(rejected, name) },
	})
	handler := lim.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest("POST", "/auth/magic", nil)
	req.RemoteAddr = "1.2.3.4:12345"

	handler.ServeHTTP(httptest.NewRecorder(), req) // allowed
	if len(rejected) != 0 {
		t.Fatalf("OnReject should not fire on an allowed request, got %v", rejected)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req) // rejected
	if len(rejected) != 1 || rejected[0] != "auth_magic" {
		t.Fatalf("OnReject should fire once with the limiter name, got %v", rejected)
	}
}

// --- Redis backend ---

func newTestRedis(t *testing.T) redis.UniversalClient {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestRedisAllowsUnderLimitThenBlocks(t *testing.T) {
	c := newRedisChecker(newTestRedis(t), "auth_magic", 3, time.Minute)
	for i := range 3 {
		if !c.allow(context.Background(), "1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if c.allow(context.Background(), "1.2.3.4") {
		t.Fatal("4th request should be blocked")
	}
}

func TestRedisTracksIPsSeparately(t *testing.T) {
	c := newRedisChecker(newTestRedis(t), "auth_magic", 1, time.Minute)
	if !c.allow(context.Background(), "1.1.1.1") {
		t.Fatal("first IP should be allowed")
	}
	if !c.allow(context.Background(), "2.2.2.2") {
		t.Fatal("second IP should be allowed")
	}
	if c.allow(context.Background(), "1.1.1.1") {
		t.Fatal("first IP should be blocked after limit")
	}
}

func TestRedisNamespacesLimitersByName(t *testing.T) {
	client := newTestRedis(t)
	a := newRedisChecker(client, "auth_magic", 1, time.Minute)
	b := newRedisChecker(client, "pairing", 1, time.Minute)
	if !a.allow(context.Background(), "1.2.3.4") {
		t.Fatal("auth_magic first request should be allowed")
	}
	// Same IP on a different limiter must not share the auth_magic counter.
	if !b.allow(context.Background(), "1.2.3.4") {
		t.Fatal("pairing first request should be allowed independently")
	}
	if a.allow(context.Background(), "1.2.3.4") {
		t.Fatal("auth_magic second request should be blocked")
	}
}

func TestRedisExpiresAfterWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	c := newRedisChecker(client, "auth_magic", 1, time.Second)
	if !c.allow(context.Background(), "1.2.3.4") {
		t.Fatal("first request should be allowed")
	}
	if c.allow(context.Background(), "1.2.3.4") {
		t.Fatal("second request should be blocked within the window")
	}
	// miniredis does not advance TTLs on its own; fast-forward past the window.
	mr.FastForward(2 * time.Second)
	if !c.allow(context.Background(), "1.2.3.4") {
		t.Fatal("request after the window should be allowed")
	}
}

func TestRedisSetsExpiryOnFirstHit(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	c := newRedisChecker(client, "auth_magic", 5, time.Minute)
	c.allow(context.Background(), "1.2.3.4")
	ttl := mr.TTL("ratelimit:auth_magic:1.2.3.4")
	if ttl <= 0 {
		t.Fatalf("expected a positive TTL on the counter, got %v", ttl)
	}
}

func TestRedisFailsOpenWhenUnreachable(t *testing.T) {
	mr := miniredis.RunT(t)
	// MaxRetries -1 disables go-redis backoff so the down-Redis case resolves
	// immediately instead of retrying for seconds.
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	// Close the backing server so every command errors.
	mr.Close()

	c := newRedisChecker(client, "auth_magic", 1, time.Minute)
	for i := range 5 {
		if !c.allow(context.Background(), "1.2.3.4") {
			t.Fatalf("request %d should fail open (be allowed) when Redis is down", i+1)
		}
	}
}

// hangAfterHandshakeServer accepts connections, lets go-redis finish its RESP2
// handshake (by rejecting HELLO), then never replies to the first real command.
// It models a reachable-but-hung Redis on a warm connection. It returns the
// listener address.
func hangAfterHandshakeServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				buf := make([]byte, 4096)
				// Read HELLO and reject it so the client falls back to RESP2
				// with no further init traffic.
				_, _ = conn.Read(buf)
				_, _ = conn.Write([]byte("-ERR unknown command 'HELLO'\r\n"))
				// Read the first real command and never reply.
				_, _ = conn.Read(buf)
				<-t.Context().Done()
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func TestRedisFailsOpenOnSlowRedis(t *testing.T) {
	addr := hangAfterHandshakeServer(t)
	// ContextTimeoutEnabled mirrors signaling.NewRateLimitRedisClient: without it
	// go-redis discards the per-call deadline and the check would block on the
	// read timeout instead of failing open. MaxRetries -1 avoids backoff.
	client := redis.NewClient(&redis.Options{
		Addr:                  addr,
		MaxRetries:            -1,
		ContextTimeoutEnabled: true,
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          5 * time.Second,
	})
	t.Cleanup(func() { _ = client.Close() })

	c := newRedisChecker(client, "auth_magic", 1, time.Minute)

	start := time.Now()
	allowed := c.allow(context.Background(), "1.2.3.4")
	elapsed := time.Since(start)

	if !allowed {
		t.Fatal("a hung Redis should fail open (be allowed)")
	}
	// The per-call redisCheckTimeout, not the 5s read timeout, must bound the
	// call. Allow generous slack for CI scheduling.
	if elapsed >= time.Second {
		t.Fatalf("allow should return near redisCheckTimeout (%v), took %v", redisCheckTimeout, elapsed)
	}
}

func TestRedisLogFailureThrottles(t *testing.T) {
	c := newRedisChecker(newTestRedis(t), "auth_magic", 1, time.Minute)
	base := time.Unix(1_000_000, 0)
	cur := base
	c.now = func() time.Time { return cur }

	// The first failure logs immediately with nothing suppressed yet.
	if ok, suppressed := c.claimLog(); !ok || suppressed != 0 {
		t.Fatalf("first claim should log with 0 suppressed, got ok=%v suppressed=%d", ok, suppressed)
	}
	// Further failures inside the interval are swallowed and counted.
	for i := range 5 {
		if ok, _ := c.claimLog(); ok {
			t.Fatalf("claim %d within interval should be suppressed", i)
		}
	}
	// Once the interval elapses, the next failure logs and reports the count.
	cur = base.Add(redisLogInterval + time.Second)
	ok, suppressed := c.claimLog()
	if !ok {
		t.Fatal("claim after interval should log")
	}
	if suppressed != 5 {
		t.Fatalf("expected 5 suppressed occurrences reported, got %d", suppressed)
	}
	// The counter resets after an emit: a fresh in-interval failure counts from 0.
	if ok, _ := c.claimLog(); ok {
		t.Fatal("claim right after an emit should be suppressed")
	}
	cur = cur.Add(redisLogInterval + time.Second)
	if ok, suppressed := c.claimLog(); !ok || suppressed != 1 {
		t.Fatalf("next emit should report the single suppressed occurrence, got ok=%v suppressed=%d", ok, suppressed)
	}
}

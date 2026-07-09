// server/internal/ratelimit/ratelimit_test.go
package ratelimit

import (
	"context"
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

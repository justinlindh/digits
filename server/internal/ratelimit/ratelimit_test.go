// server/internal/ratelimit/ratelimit_test.go
package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterAllowsUnderLimit(t *testing.T) {
	lim := New(5, time.Minute, 1)
	for i := 0; i < 5; i++ {
		if !lim.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
}

func TestLimiterBlocksOverLimit(t *testing.T) {
	lim := New(3, time.Minute, 1)
	for i := 0; i < 3; i++ {
		lim.Allow("1.2.3.4")
	}
	if lim.Allow("1.2.3.4") {
		t.Fatal("4th request should be blocked")
	}
}

func TestLimiterTracksIPsSeparately(t *testing.T) {
	lim := New(1, time.Minute, 1)
	if !lim.Allow("1.1.1.1") {
		t.Fatal("first IP should be allowed")
	}
	if !lim.Allow("2.2.2.2") {
		t.Fatal("second IP should be allowed")
	}
	if lim.Allow("1.1.1.1") {
		t.Fatal("first IP should be blocked after limit")
	}
}

func TestLimiterRefillsAfterWindow(t *testing.T) {
	lim := New(1, 50*time.Millisecond, 1)
	lim.Allow("1.2.3.4")
	if lim.Allow("1.2.3.4") {
		t.Fatal("should be blocked immediately")
	}
	time.Sleep(60 * time.Millisecond)
	if !lim.Allow("1.2.3.4") {
		t.Fatal("should be allowed after window")
	}
}

func TestMiddleware429(t *testing.T) {
	lim := New(1, time.Minute, 1)
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
	lim := New(1, time.Minute, 1)
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

func TestEvictExpiredRemovesStaleEntries(t *testing.T) {
	lim := New(1, 50*time.Millisecond, 1)
	lim.Allow("stale-ip")
	time.Sleep(60 * time.Millisecond)
	lim.evictExpired()
	lim.mu.Lock()
	_, exists := lim.buckets["stale-ip"]
	lim.mu.Unlock()
	if exists {
		t.Fatal("stale entry should have been cleaned up")
	}
}

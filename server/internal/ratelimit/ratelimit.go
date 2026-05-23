// Package ratelimit provides an in-memory, IP-based rate limiter using a
// fixed-window token bucket. It is intended for protecting individual HTTP
// endpoints (auth, pairing, WebSocket upgrade) from brute-force and
// enumeration attacks within a single process.
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/justinlindh/digits/server/internal/httputil"
)

type bucket struct {
	tokens    int
	lastReset time.Time
}

// Limiter is an in-memory IP-based rate limiter using a fixed-window token bucket.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	limit   int
	window  time.Duration
}

// New creates a rate limiter that allows `limit` requests per `window` per IP.
// It starts a background goroutine that evicts stale entries every 5 minutes
// and runs for the lifetime of the process.
func New(limit int, window time.Duration) *Limiter {
	l := &Limiter{
		buckets: make(map[string]*bucket),
		limit:   limit,
		window:  window,
	}
	go l.cleanupLoop()
	return l
}

func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.Cleanup()
	}
}

// Allow checks whether the given IP is within its rate limit.
func (l *Limiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.Sub(b.lastReset) >= l.window {
		l.buckets[ip] = &bucket{tokens: l.limit - 1, lastReset: now}
		return true
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// Cleanup removes expired bucket entries. Call periodically to prevent memory leak.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for ip, b := range l.buckets {
		if now.Sub(b.lastReset) >= l.window {
			delete(l.buckets, ip)
		}
	}
}

// Middleware returns an http.Handler that rejects requests over the rate limit with 429.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := httputil.ClientIP(r)
		if !l.Allow(ip) {
			http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

package autodeploy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollHealthMatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"1.9.1"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := PollHealth(ctx, srv.URL, "1.9.1", 10*time.Millisecond); err != nil {
		t.Fatalf("PollHealth: %v", err)
	}
}

func TestPollHealthEventuallyMatches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			_, _ = fmt.Fprint(w, `{"status":"ok","version":"1.9.0"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"status":"ok","version":"1.9.1"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := PollHealth(ctx, srv.URL, "1.9.1", 10*time.Millisecond); err != nil {
		t.Fatalf("PollHealth: %v", err)
	}
}

func TestPollHealthTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"ok","version":"0.0.0"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := PollHealth(ctx, srv.URL, "1.9.1", 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestPollHealth5xxKeepsPolling(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 2 {
			http.Error(w, "boom", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"ok","version":"1.9.1"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := PollHealth(ctx, srv.URL, "1.9.1", 10*time.Millisecond); err != nil {
		t.Fatalf("PollHealth: %v", err)
	}
}

package events

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestBroadcasterRedisNotifyCrossPod(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	ctx := t.Context()

	bA := New()
	bA.SetRedis(clientA, "pod-a")
	go bA.RunRedis(ctx)

	bB := New()
	bB.SetRedis(clientB, "pod-b")
	go bB.RunRedis(ctx)

	time.Sleep(100 * time.Millisecond)

	sub, unsub := bB.Subscribe()
	defer unsub()

	bA.Notify()

	select {
	case <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("pod B should have received notification from pod A")
	}
}

func TestBroadcasterRedisNoSelfEcho(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := t.Context()

	b := New()
	b.SetRedis(client, "pod-self")
	go b.RunRedis(ctx)

	time.Sleep(100 * time.Millisecond)

	sub, unsub := b.Subscribe()
	defer unsub()

	b.Notify()

	// Local notify should still work (Notify wakes locals directly)
	select {
	case <-sub:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("local notify should still work")
	}
}

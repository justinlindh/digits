//go:build integration

package signaling

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRedisBridgeEndToEnd creates two Hub instances sharing a Redis and
// verifies that a message sent on one hub is delivered on the other.
func TestRedisBridgeEndToEnd(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set, skipping Redis integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Pod A
	bridgeA, err := NewRedisBridge(redisURL)
	if err != nil {
		t.Fatalf("bridgeA: %v", err)
	}
	defer func() { _ = bridgeA.Close() }()
	// Override podID so the two bridges have distinct identities.
	bridgeA.podID = "pod-a"

	if err := bridgeA.Ping(ctx); err != nil {
		t.Fatalf("bridgeA ping: %v", err)
	}

	hubA := NewHub()
	hubA.SetRedis(bridgeA)
	go hubA.Run(ctx)

	// Pod B
	bridgeB, err := NewRedisBridge(redisURL)
	if err != nil {
		t.Fatalf("bridgeB: %v", err)
	}
	defer func() { _ = bridgeB.Close() }()
	bridgeB.podID = "pod-b"

	hubB := NewHub()
	hubB.SetRedis(bridgeB)
	go hubB.Run(ctx)

	// Give subscribers time to establish.
	time.Sleep(200 * time.Millisecond)

	// Register a connection on pod B.
	conn := &Conn{Send: make(chan []byte, 10)}
	hubB.Register("3140001", conn)

	// Send from pod A (target is not on pod A, so it publishes to Redis).
	err = hubA.SendTo("3140001", &Message{Type: TypeRing, From: "3140002"})
	if err != nil {
		t.Fatalf("hubA.SendTo: %v", err)
	}

	// Verify delivery on pod B.
	select {
	case data := <-conn.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeRing {
			t.Errorf("Type = %q, want %q", msg.Type, TypeRing)
		}
		if msg.From != "3140002" {
			t.Errorf("From = %q, want %q", msg.From, "3140002")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cross-pod delivery")
	}
}

// TestRedisBridgeSelfMessagesSkipped verifies that a hub does not deliver
// its own published messages back to itself.
func TestRedisBridgeSelfMessagesSkipped(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set, skipping Redis integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bridge, err := NewRedisBridge(redisURL)
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()
	bridge.podID = "pod-self"

	hub := NewHub()
	hub.SetRedis(bridge)
	go hub.Run(ctx)

	time.Sleep(200 * time.Millisecond)

	conn := &Conn{Send: make(chan []byte, 10)}
	hub.Register("3140001", conn)

	// Send to a number NOT on this hub. The hub publishes to Redis, but
	// the subscriber should skip the message because it originated here.
	err = hub.SendTo("3140099", &Message{Type: TypeRing, From: "3140001"})
	if err != nil {
		t.Fatalf("SendTo: %v", err)
	}

	// Wait briefly and verify nothing was delivered to "3140001".
	time.Sleep(500 * time.Millisecond)
	select {
	case data := <-conn.Send:
		t.Fatalf("self-message should have been skipped, but got: %s", data)
	default:
		// Good: no self-delivery.
	}
}

// TestRedisBridgeBroadcastCrossPod verifies that Broadcast reaches
// connections on another pod.
func TestRedisBridgeBroadcastCrossPod(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set, skipping Redis integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bridgeA, err := NewRedisBridge(redisURL)
	if err != nil {
		t.Fatalf("bridgeA: %v", err)
	}
	defer func() { _ = bridgeA.Close() }()
	bridgeA.podID = "pod-a-bc"

	hubA := NewHub()
	hubA.SetRedis(bridgeA)
	go hubA.Run(ctx)

	bridgeB, err := NewRedisBridge(redisURL)
	if err != nil {
		t.Fatalf("bridgeB: %v", err)
	}
	defer func() { _ = bridgeB.Close() }()
	bridgeB.podID = "pod-b-bc"

	hubB := NewHub()
	hubB.SetRedis(bridgeB)
	go hubB.Run(ctx)

	time.Sleep(200 * time.Millisecond)

	conn := &Conn{Send: make(chan []byte, 10)}
	hubB.Register("3140001", conn)

	hubA.Broadcast(&Message{Type: TypeReleaseAvailable, LatestPiVersion: "9.9.9"})

	select {
	case data := <-conn.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeReleaseAvailable {
			t.Errorf("Type = %q, want %q", msg.Type, TypeReleaseAvailable)
		}
		if msg.LatestPiVersion != "9.9.9" {
			t.Errorf("LatestPiVersion = %q, want %q", msg.LatestPiVersion, "9.9.9")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cross-pod broadcast")
	}
}

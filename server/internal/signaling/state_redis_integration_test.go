//go:build integration

package signaling

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestDeviceStateIntegrationTwoPods(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set")
	}

	ctx := context.Background()
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer func() { _ = client.Close() }()

	t.Cleanup(func() {
		iter := client.Scan(ctx, 0, "digits:device:test-*", 100).Iterator()
		for iter.Next(ctx) {
			_ = client.Del(ctx, iter.Val())
		}
		_ = client.Del(ctx, "digits:counter:online-devices")
	})

	dsA := NewDeviceState(client, "pod-a")
	dsA.SetOnline(ctx, "test-5551234", DevicePresence{
		HardwareID:      "hw-int-1",
		PiVersion:       "2.0.0",
		FirmwareVersion: "1.0.0",
		RemoteAddr:      "192.168.1.10",
	})

	dsB := NewDeviceState(client, "pod-b")
	if !dsB.IsOnline(ctx, "test-5551234") {
		t.Fatal("pod B should see device as online")
	}

	info := dsB.DeviceInfo(ctx, "test-5551234")
	if info == nil {
		t.Fatal("pod B should see device info")
	}
	if info.PiVersion != "2.0.0" {
		t.Errorf("PiVersion = %q, want %q", info.PiVersion, "2.0.0")
	}

	dsA.SetOffline(ctx, "test-5551234")
	if dsB.IsOnline(ctx, "test-5551234") {
		t.Fatal("pod B should see device as offline after unregister")
	}
}

func TestDeviceStateTTLExpiryIntegration(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set")
	}

	ctx := context.Background()
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer func() { _ = client.Close() }()

	t.Cleanup(func() {
		_ = client.Del(ctx, "digits:device:test-ttl-5551234")
	})

	ds := NewDeviceState(client, "pod-ttl")
	ds.SetOnline(ctx, "test-ttl-5551234", DevicePresence{})

	ttl, err := client.TTL(ctx, "digits:device:test-ttl-5551234").Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl < 80*time.Second || ttl > 91*time.Second {
		t.Errorf("TTL = %v, expected ~90s", ttl)
	}
}

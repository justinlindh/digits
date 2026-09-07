//go:build integration

package signaling

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisPresenceGenerationFenceIntegration(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	hardwareID := "renumber-" + uuid.NewString()
	oldNumber := "7651001"
	newNumber := "7651002"
	t.Cleanup(func() {
		client.Del(ctx, deviceKeyPrefix+hardwareID, deviceKeyPrefix+hardwareID+":generation", lineDevicesPrefix+oldNumber, lineDevicesPrefix+newNumber)
	})
	ds := NewDeviceState(client, "integration-pod")
	oldGeneration, err := ds.ClaimGeneration(ctx, hardwareID)
	if err != nil {
		t.Fatal(err)
	}
	newGeneration, err := ds.ClaimGeneration(ctx, hardwareID)
	if err != nil {
		t.Fatal(err)
	}
	ds.SetOnline(ctx, newNumber, DevicePresence{PodID: ds.PodID(), HardwareID: hardwareID, ConnectionID: "new", PiVersion: "new", PresenceGeneration: newGeneration})
	ds.SetOffline(ctx, newNumber, hardwareID, "new")
	ds.SetOnline(ctx, oldNumber, DevicePresence{PodID: ds.PodID(), HardwareID: hardwareID, ConnectionID: "old", PresenceGeneration: oldGeneration})
	ds.TouchLastSeen(ctx, oldNumber, hardwareID, "old")
	ds.UpdateDeviceInfo(ctx, hardwareID, "old", DevicePresence{PiVersion: "stale"})
	if ds.IsHardwareOnline(ctx, hardwareID) {
		t.Fatal("stale generation recreated deleted Redis presence")
	}
	if ds.IsOnline(ctx, oldNumber) || ds.IsOnline(ctx, newNumber) {
		t.Fatal("stale generation left line membership online")
	}
}

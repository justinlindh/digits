package signaling

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestDeviceState(t *testing.T) (*DeviceState, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ds := NewDeviceState(client, "test-pod")
	return ds, mr
}

func TestDeviceStateSetOnlineAndIsOnline(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()

	if ds.IsOnline(ctx, "5551234") {
		t.Fatal("expected device to be offline before SetOnline")
	}

	ds.SetOnline(ctx, "5551234", DevicePresence{
		PodID:           "pod-1",
		HardwareID:      "hw-abc",
		PiVersion:       "1.0.0",
		PiCommit:        "abc123",
		FirmwareVersion: "0.5.0",
		FirmwareCommit:  "def456",
		RemoteAddr:      "192.168.1.10",
	})

	if !ds.IsOnline(ctx, "5551234") {
		t.Fatal("expected device to be online after SetOnline")
	}
}

func TestDeviceStateSetOffline(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()

	ds.SetOnline(ctx, "5551234", DevicePresence{
		PodID:      "pod-1",
		HardwareID: "hw-abc",
	})

	ds.SetOffline(ctx, "5551234")

	if ds.IsOnline(ctx, "5551234") {
		t.Fatal("expected device to be offline after SetOffline")
	}
}

func TestDeviceStateOnlineNumbers(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()

	ds.SetOnline(ctx, "5551111", DevicePresence{PodID: "p", HardwareID: "h1"})
	ds.SetOnline(ctx, "5552222", DevicePresence{PodID: "p", HardwareID: "h2"})
	ds.SetOnline(ctx, "unpaired:test-hw-abc", DevicePresence{PodID: "p", HardwareID: "h3"})

	numbers := ds.OnlineNumbers(ctx)

	got := make(map[string]bool)
	for _, n := range numbers {
		got[n] = true
	}

	if !got["5551111"] || !got["5552222"] {
		t.Fatalf("expected both numbers, got %v", numbers)
	}
	if got["unpaired:test-hw-abc"] {
		t.Fatal("unpaired device should be excluded from OnlineNumbers")
	}
}

func TestDeviceStateDeviceInfo(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()

	ds.SetOnline(ctx, "5551234", DevicePresence{
		PodID:           "pod-1",
		HardwareID:      "hw-abc",
		PiVersion:       "1.2.0",
		PiCommit:        "aaa111",
		FirmwareVersion: "0.8.0",
		FirmwareCommit:  "bbb222",
		RemoteAddr:      "10.0.0.5",
	})

	info := ds.DeviceInfo(ctx, "5551234")
	if info == nil {
		t.Fatal("expected non-nil DeviceInfoSnapshot")
	}
	if info.PiVersion != "1.2.0" {
		t.Errorf("PiVersion = %q, want %q", info.PiVersion, "1.2.0")
	}
	if info.PiCommit != "aaa111" {
		t.Errorf("PiCommit = %q, want %q", info.PiCommit, "aaa111")
	}
	if info.FirmwareVersion != "0.8.0" {
		t.Errorf("FirmwareVersion = %q, want %q", info.FirmwareVersion, "0.8.0")
	}
	if info.FirmwareCommit != "bbb222" {
		t.Errorf("FirmwareCommit = %q, want %q", info.FirmwareCommit, "bbb222")
	}
	if info.RemoteAddr != "10.0.0.5" {
		t.Errorf("RemoteAddr = %q, want %q", info.RemoteAddr, "10.0.0.5")
	}
}

func TestDeviceStateDeviceInfoOffline(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()

	info := ds.DeviceInfo(ctx, "5559999")
	if info != nil {
		t.Fatalf("expected nil for offline device, got %+v", info)
	}
}

func TestDeviceStateUpdateDeviceInfo(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()

	ds.SetOnline(ctx, "5551234", DevicePresence{
		PodID:           "pod-1",
		HardwareID:      "hw-abc",
		PiVersion:       "1.0.0",
		FirmwareVersion: "0.5.0",
	})

	ds.UpdateDeviceInfo(ctx, "5551234", DevicePresence{
		PiVersion:  "1.1.0",
		RemoteAddr: "192.168.1.50",
	})

	info := ds.DeviceInfo(ctx, "5551234")
	if info == nil {
		t.Fatal("expected non-nil DeviceInfoSnapshot")
	}
	if info.PiVersion != "1.1.0" {
		t.Errorf("PiVersion = %q, want %q", info.PiVersion, "1.1.0")
	}
	if info.FirmwareVersion != "0.5.0" {
		t.Errorf("FirmwareVersion should remain %q, got %q", "0.5.0", info.FirmwareVersion)
	}
	if info.RemoteAddr != "192.168.1.50" {
		t.Errorf("RemoteAddr = %q, want %q", info.RemoteAddr, "192.168.1.50")
	}
}

func TestDeviceStateTouchLastSeen(t *testing.T) {
	ds, mr := newTestDeviceState(t)
	ctx := context.Background()

	ds.SetOnline(ctx, "5551234", DevicePresence{
		PodID:      "pod-1",
		HardwareID: "hw-abc",
	})

	mr.FastForward(60 * time.Second)

	ds.TouchLastSeen(ctx, "5551234")

	ts := ds.LastSeenAt(ctx, "5551234")
	if ts == nil {
		t.Fatal("expected non-nil LastSeenAt after TouchLastSeen")
	}

	now := time.Now()
	diff := now.Sub(*ts)
	if diff < 0 {
		diff = -diff
	}
	if diff > 5*time.Second {
		t.Errorf("LastSeenAt too far from now: %v", diff)
	}

	ttl := mr.TTL("digits:device:5551234")
	if ttl < 80*time.Second || ttl > 91*time.Second {
		t.Errorf("TTL after TouchLastSeen = %v, want ~90s", ttl)
	}
}

func TestDeviceStateUpdateStatus(t *testing.T) {
	ds, _ := newTestDeviceState(t)
	ctx := context.Background()

	ds.SetUpdateStatus(ctx, "5551234", "downloading", "50%")

	snap := ds.GetUpdateStatus(ctx, "5551234")
	if snap == nil {
		t.Fatal("expected non-nil UpdateStatusSnapshot")
	}
	if snap.Status != "downloading" {
		t.Errorf("Status = %q, want %q", snap.Status, "downloading")
	}
	if snap.Detail != "50%" {
		t.Errorf("Detail = %q, want %q", snap.Detail, "50%")
	}
	if snap.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}

	ds.ClearUpdateStatus(ctx, "5551234")

	snap = ds.GetUpdateStatus(ctx, "5551234")
	if snap != nil {
		t.Fatalf("expected nil after ClearUpdateStatus, got %+v", snap)
	}
}

func TestDeviceStateTTLExpiry(t *testing.T) {
	ds, mr := newTestDeviceState(t)
	ctx := context.Background()

	ds.SetOnline(ctx, "5551234", DevicePresence{
		PodID:      "pod-1",
		HardwareID: "hw-abc",
	})

	if !ds.IsOnline(ctx, "5551234") {
		t.Fatal("expected online before expiry")
	}

	mr.FastForward(91 * time.Second)

	if ds.IsOnline(ctx, "5551234") {
		t.Fatal("expected offline after TTL expiry")
	}
}

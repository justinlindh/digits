package signaling

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	deviceKeyPrefix    = "digits:device:"
	lineDevicesPrefix  = "digits:line-devices:"
	updateStatusPrefix = "digits:update-status:"

	deviceTTL       = 90 * time.Second
	updateStatusTTL = time.Hour
)

// DevicePresence is the cluster-wide view of a single connected device,
// stored in Redis so every pod can answer presence queries without local
// connection state.
type DevicePresence struct {
	PodID           string
	HardwareID      string
	PiVersion       string
	PiCommit        string
	FirmwareVersion string
	FirmwareCommit  string
	RemoteAddr      string
	DevMode         bool
}

// DeviceState persists per-device presence records in Redis so that any pod
// can answer questions about online devices regardless of which pod holds the
// WebSocket connection.
type DeviceState struct {
	client redis.UniversalClient
	podID  string
}

// NewDeviceState returns a DeviceState backed by the given Redis client.
// podID identifies this process instance in the cluster; it is stored with
// each presence record so callers can distinguish local vs remote devices.
func NewDeviceState(client redis.UniversalClient, podID string) *DeviceState {
	return &DeviceState{client: client, podID: podID}
}

func (s *DeviceState) SetOnline(ctx context.Context, number string, p DevicePresence) {
	if p.HardwareID == "" {
		return
	}
	devKey := deviceKeyPrefix + p.HardwareID
	setKey := lineDevicesPrefix + number
	now := strconv.FormatInt(time.Now().Unix(), 10)

	fields := map[string]any{
		"pod_id":      p.PodID,
		"number":      number,
		"hardware_id": p.HardwareID,
		"pi_version":  p.PiVersion,
		"pi_commit":   p.PiCommit,
		"fw_version":  p.FirmwareVersion,
		"fw_commit":   p.FirmwareCommit,
		"remote_addr": p.RemoteAddr,
		"dev_mode":    p.DevMode,
		"last_seen":   now,
	}

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, devKey, fields)
	pipe.Expire(ctx, devKey, deviceTTL)
	pipe.SAdd(ctx, setKey, p.HardwareID)
	pipe.Expire(ctx, setKey, deviceTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: SetOnline failed", "number", number, "hardware_id", p.HardwareID, "err", err)
	}
}

func (s *DeviceState) SetOffline(ctx context.Context, number, hardwareID string) {
	if hardwareID == "" {
		return
	}
	devKey := deviceKeyPrefix + hardwareID
	setKey := lineDevicesPrefix + number

	pipe := s.client.Pipeline()
	pipe.Del(ctx, devKey)
	pipe.SRem(ctx, setKey, hardwareID)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: SetOffline failed", "number", number, "hardware_id", hardwareID, "err", err)
	}
}

func (s *DeviceState) IsOnline(ctx context.Context, number string) bool {
	key := lineDevicesPrefix + number
	n, err := s.client.SCard(ctx, key).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: IsOnline failed", "number", number, "err", err)
		return false
	}
	return n > 0
}

// IsHardwareOnline reports whether the device with this hardware id is
// connected to any pod. The per-device presence hash exists exactly while the
// device is connected (SetOnline writes it, SetOffline deletes it, heartbeats
// refresh its TTL), so its existence is the cross-pod online signal.
func (s *DeviceState) IsHardwareOnline(ctx context.Context, hardwareID string) bool {
	n, err := s.client.Exists(ctx, deviceKeyPrefix+hardwareID).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: IsHardwareOnline failed", "hardware_id", hardwareID, "err", err)
		return false
	}
	return n > 0
}

func (s *DeviceState) OnlineNumbers(ctx context.Context) []string {
	var numbers []string
	pattern := lineDevicesPrefix + "*"
	prefixLen := len(lineDevicesPrefix)

	iter := s.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		number := key[prefixLen:]
		if strings.HasPrefix(number, UnpairedPrefix) {
			continue
		}
		numbers = append(numbers, number)
	}
	if err := iter.Err(); err != nil {
		slog.ErrorContext(ctx, "redis: OnlineNumbers scan failed", "err", err)
	}
	return numbers
}

func (s *DeviceState) AllDeviceInfo(ctx context.Context, number string) []DeviceInfoSnapshot {
	setKey := lineDevicesPrefix + number
	hwIDs, err := s.client.SMembers(ctx, setKey).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: AllDeviceInfo SMembers failed", "number", number, "err", err)
		return nil
	}
	if len(hwIDs) == 0 {
		return nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(hwIDs))
	for i, hwID := range hwIDs {
		cmds[i] = pipe.HGetAll(ctx, deviceKeyPrefix+hwID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: AllDeviceInfo pipeline failed", "number", number, "err", err)
		return nil
	}

	var stale []string
	var snapshots []DeviceInfoSnapshot
	for i, cmd := range cmds {
		vals, err := cmd.Result()
		if err != nil || len(vals) == 0 {
			stale = append(stale, hwIDs[i])
			continue
		}
		snapshots = append(snapshots, DeviceInfoSnapshot{
			HardwareID:      vals["hardware_id"],
			PiVersion:       vals["pi_version"],
			PiCommit:        vals["pi_commit"],
			FirmwareVersion: vals["fw_version"],
			FirmwareCommit:  vals["fw_commit"],
			RemoteAddr:      vals["remote_addr"],
			DevMode:         vals["dev_mode"] == "1",
		})
	}

	if len(stale) > 0 {
		staleIfaces := make([]any, len(stale))
		for i, id := range stale {
			staleIfaces[i] = id
		}
		if err := s.client.SRem(ctx, setKey, staleIfaces...).Err(); err != nil {
			slog.ErrorContext(ctx, "redis: AllDeviceInfo stale cleanup failed", "number", number, "err", err)
		}
	}

	return snapshots
}

func (s *DeviceState) UpdateDeviceInfo(ctx context.Context, hardwareID string, p DevicePresence) {
	if hardwareID == "" {
		return
	}
	key := deviceKeyPrefix + hardwareID
	fields := make(map[string]any)

	if p.PodID != "" {
		fields["pod_id"] = p.PodID
	}
	if p.PiVersion != "" {
		fields["pi_version"] = p.PiVersion
	}
	if p.PiCommit != "" {
		fields["pi_commit"] = p.PiCommit
	}
	if p.FirmwareVersion != "" {
		fields["fw_version"] = p.FirmwareVersion
	}
	if p.FirmwareCommit != "" {
		fields["fw_commit"] = p.FirmwareCommit
	}
	if p.RemoteAddr != "" {
		fields["remote_addr"] = p.RemoteAddr
	}
	fields["dev_mode"] = p.DevMode

	if err := s.client.HSet(ctx, key, fields).Err(); err != nil {
		slog.ErrorContext(ctx, "redis: UpdateDeviceInfo failed", "hardware_id", hardwareID, "err", err)
	}
}

func (s *DeviceState) TouchLastSeen(ctx context.Context, number, hardwareID string) {
	if hardwareID == "" {
		return
	}
	devKey := deviceKeyPrefix + hardwareID
	now := strconv.FormatInt(time.Now().Unix(), 10)

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, devKey, "last_seen", now)
	pipe.Expire(ctx, devKey, deviceTTL)
	if number != "" {
		pipe.Expire(ctx, lineDevicesPrefix+number, deviceTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: TouchLastSeen failed", "hardware_id", hardwareID, "err", err)
	}
}

func (s *DeviceState) LastSeenAt(ctx context.Context, number string) *time.Time {
	setKey := lineDevicesPrefix + number
	hwIDs, err := s.client.SMembers(ctx, setKey).Result()
	if err != nil || len(hwIDs) == 0 {
		if err != nil {
			slog.ErrorContext(ctx, "redis: LastSeenAt SMembers failed", "number", number, "err", err)
		}
		return nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(hwIDs))
	for i, hwID := range hwIDs {
		cmds[i] = pipe.HGet(ctx, deviceKeyPrefix+hwID, "last_seen")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		slog.ErrorContext(ctx, "redis: LastSeenAt pipeline failed", "number", number, "err", err)
		return nil
	}

	var latest time.Time
	for _, cmd := range cmds {
		val, err := cmd.Result()
		if err != nil {
			continue
		}
		unix, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			continue
		}
		t := time.Unix(unix, 0)
		if t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}

func (s *DeviceState) SetUpdateStatus(ctx context.Context, hardwareID, status, detail string) {
	key := updateStatusPrefix + hardwareID
	now := strconv.FormatInt(time.Now().Unix(), 10)

	fields := map[string]any{
		"status":     status,
		"detail":     detail,
		"updated_at": now,
	}

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, updateStatusTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: SetUpdateStatus failed", "hardware_id", hardwareID, "err", err)
	}
}

func (s *DeviceState) GetUpdateStatus(ctx context.Context, hardwareID string) *UpdateStatusSnapshot {
	key := updateStatusPrefix + hardwareID
	vals, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: GetUpdateStatus failed", "hardware_id", hardwareID, "err", err)
		return nil
	}
	if len(vals) == 0 {
		return nil
	}

	var updatedAt time.Time
	if raw, ok := vals["updated_at"]; ok {
		if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
			updatedAt = time.Unix(unix, 0)
		}
	}

	return &UpdateStatusSnapshot{
		Status:    vals["status"],
		Detail:    vals["detail"],
		UpdatedAt: updatedAt,
	}
}

func (s *DeviceState) ClearUpdateStatus(ctx context.Context, hardwareID string) {
	key := updateStatusPrefix + hardwareID
	if err := s.client.Del(ctx, key).Err(); err != nil {
		slog.ErrorContext(ctx, "redis: ClearUpdateStatus failed", "hardware_id", hardwareID, "err", err)
	}
}

// PodID returns the pod identifier this state instance was created with.
func (s *DeviceState) PodID() string {
	return s.podID
}

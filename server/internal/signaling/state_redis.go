package signaling

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	deviceKeyPrefix    = "digits:device:"
	updateStatusPrefix = "digits:update-status:"

	deviceTTL       = 90 * time.Second
	updateStatusTTL = 1 * time.Hour
)

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

type DeviceState struct {
	client redis.UniversalClient
	podID  string
}

func NewDeviceState(client redis.UniversalClient, podID string) *DeviceState {
	return &DeviceState{client: client, podID: podID}
}

func (s *DeviceState) SetOnline(ctx context.Context, number string, p DevicePresence) {
	key := deviceKeyPrefix + number
	now := strconv.FormatInt(time.Now().Unix(), 10)

	fields := map[string]interface{}{
		"pod_id":      p.PodID,
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
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, deviceTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("redis: SetOnline failed", "number", number, "err", err)
	}
}

func (s *DeviceState) SetOffline(ctx context.Context, number string) {
	key := deviceKeyPrefix + number
	if err := s.client.Del(ctx, key).Err(); err != nil {
		slog.Error("redis: SetOffline failed", "number", number, "err", err)
	}
}

func (s *DeviceState) IsOnline(ctx context.Context, number string) bool {
	key := deviceKeyPrefix + number
	n, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		slog.Error("redis: IsOnline failed", "number", number, "err", err)
		return false
	}
	return n > 0
}

func (s *DeviceState) OnlineNumbers(ctx context.Context) []string {
	var numbers []string
	pattern := deviceKeyPrefix + "*"
	prefixLen := len(deviceKeyPrefix)

	iter := s.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		number := key[prefixLen:]
		if strings.HasPrefix(number, "unpaired:") {
			continue
		}
		numbers = append(numbers, number)
	}
	if err := iter.Err(); err != nil {
		slog.Error("redis: OnlineNumbers scan failed", "err", err)
	}
	return numbers
}

func (s *DeviceState) DeviceInfo(ctx context.Context, number string) *DeviceInfoSnapshot {
	key := deviceKeyPrefix + number
	vals, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		slog.Error("redis: DeviceInfo failed", "number", number, "err", err)
		return nil
	}
	if len(vals) == 0 {
		return nil
	}
	return &DeviceInfoSnapshot{
		PiVersion:       vals["pi_version"],
		PiCommit:        vals["pi_commit"],
		FirmwareVersion: vals["fw_version"],
		FirmwareCommit:  vals["fw_commit"],
		RemoteAddr:      vals["remote_addr"],
		DevMode:         vals["dev_mode"] == "1",
	}
}

func (s *DeviceState) UpdateDeviceInfo(ctx context.Context, number string, p DevicePresence) {
	key := deviceKeyPrefix + number
	fields := make(map[string]interface{})

	if p.PodID != "" {
		fields["pod_id"] = p.PodID
	}
	if p.HardwareID != "" {
		fields["hardware_id"] = p.HardwareID
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
		slog.Error("redis: UpdateDeviceInfo failed", "number", number, "err", err)
	}
}

func (s *DeviceState) TouchLastSeen(ctx context.Context, number string) {
	key := deviceKeyPrefix + number
	now := strconv.FormatInt(time.Now().Unix(), 10)

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, key, "last_seen", now)
	pipe.Expire(ctx, key, deviceTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("redis: TouchLastSeen failed", "number", number, "err", err)
	}
}

func (s *DeviceState) LastSeenAt(ctx context.Context, number string) *time.Time {
	key := deviceKeyPrefix + number
	val, err := s.client.HGet(ctx, key, "last_seen").Result()
	if err != nil {
		if err != redis.Nil {
			slog.Error("redis: LastSeenAt failed", "number", number, "err", err)
		}
		return nil
	}
	unix, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		slog.Error("redis: LastSeenAt parse failed", "number", number, "val", val, "err", err)
		return nil
	}
	t := time.Unix(unix, 0)
	return &t
}

func (s *DeviceState) SetUpdateStatus(ctx context.Context, number, status, detail string) {
	key := updateStatusPrefix + number
	now := strconv.FormatInt(time.Now().Unix(), 10)

	fields := map[string]interface{}{
		"status":     status,
		"detail":     detail,
		"updated_at": now,
	}

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, updateStatusTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("redis: SetUpdateStatus failed", "number", number, "err", err)
	}
}

func (s *DeviceState) GetUpdateStatus(ctx context.Context, number string) *UpdateStatusSnapshot {
	key := updateStatusPrefix + number
	vals, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		slog.Error("redis: GetUpdateStatus failed", "number", number, "err", err)
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

func (s *DeviceState) ClearUpdateStatus(ctx context.Context, number string) {
	key := updateStatusPrefix + number
	if err := s.client.Del(ctx, key).Err(); err != nil {
		slog.Error("redis: ClearUpdateStatus failed", "number", number, "err", err)
	}
}


// PodID returns the pod identifier this state instance was created with.
func (s *DeviceState) PodID() string {
	return s.podID
}

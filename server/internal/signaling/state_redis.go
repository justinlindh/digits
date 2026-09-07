package signaling

import (
	"context"
	"errors"
	"fmt"
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
	LineID             int64
	ConnectionID       string
	PresenceGeneration int64
	PodID              string
	HardwareID         string
	PiVersion          string
	PiCommit           string
	FirmwareVersion    string
	FirmwareCommit     string
	RemoteAddr         string
	DevMode            bool
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

var claimGenerationScript = redis.NewScript(`return redis.call('INCR', KEYS[1])`)

func (s *DeviceState) ClaimGeneration(ctx context.Context, hardwareID string) (int64, error) {
	if hardwareID == "" {
		return 0, fmt.Errorf("hardware ID required")
	}
	generation, err := claimGenerationScript.Run(ctx, s.client, []string{deviceKeyPrefix + hardwareID + ":generation"}).Int64()
	if err != nil {
		slog.ErrorContext(ctx, "redis: ClaimGeneration failed", "hardware_id", hardwareID, "err", err)
		return 0, err
	}
	return generation, nil
}

var setOnlineScript = redis.NewScript(`
local old_generation = tonumber(redis.call('HGET', KEYS[1], 'generation') or '-1')
local authoritative_generation = tonumber(redis.call('GET', KEYS[4]) or '0')
local generation = tonumber(ARGV[13]) or 0
if generation < old_generation or generation < authoritative_generation then
	return 0
end
local old_number = redis.call('HGET', KEYS[1], 'number')
if old_number ~= false and old_number ~= '' and old_number ~= ARGV[1] then
	redis.call('SREM', KEYS[3] .. old_number, ARGV[3])
end
redis.call('HSET', KEYS[1],
	'pod_id', ARGV[2], 'number', ARGV[1], 'hardware_id', ARGV[3],
	'connection_id', ARGV[4], 'pi_version', ARGV[5], 'pi_commit', ARGV[6],
	'fw_version', ARGV[7], 'fw_commit', ARGV[8], 'remote_addr', ARGV[9],
	'dev_mode', ARGV[10], 'last_seen', ARGV[11], 'generation', ARGV[13],
	'line_id', ARGV[14])
redis.call('EXPIRE', KEYS[1], ARGV[12])
redis.call('SADD', KEYS[2], ARGV[3])
redis.call('EXPIRE', KEYS[2], ARGV[12])
return 1`)

func (s *DeviceState) SetOnline(ctx context.Context, number string, p DevicePresence) {
	if p.HardwareID == "" {
		return
	}
	devKey := deviceKeyPrefix + p.HardwareID
	setKey := lineDevicesPrefix + number
	args := []any{number, p.PodID, p.HardwareID, p.ConnectionID, p.PiVersion, p.PiCommit,
		p.FirmwareVersion, p.FirmwareCommit, p.RemoteAddr, p.DevMode, time.Now().Unix(), int(deviceTTL.Seconds()), p.PresenceGeneration, p.LineID}
	if err := setOnlineScript.Run(ctx, s.client, []string{devKey, setKey, lineDevicesPrefix, deviceKeyPrefix + p.HardwareID + ":generation"}, args...).Err(); err != nil {
		slog.ErrorContext(ctx, "redis: SetOnline failed", "number", number, "hardware_id", p.HardwareID, "err", err)
	}
}

// setOfflineScript deletes a device presence record only if this pod still
// owns it (or no owner is recorded). A device that reconnects to another pod
// while the old pod's read loop is still unwinding has its presence hash
// rewritten with the new pod_id before the old pod's Unregister runs
// SetOffline; an unconditional delete here would erase the live record and
// make the device look offline cluster-wide until its next SetOnline.
var setOfflineScript = redis.NewScript(`
local pod = redis.call('HGET', KEYS[1], 'pod_id')
local connection = redis.call('HGET', KEYS[1], 'connection_id')
local number = redis.call('HGET', KEYS[1], 'number')
local connection_matches = ARGV[3] == '' or connection == false or connection == '' or connection == ARGV[3]
if (pod == false or pod == '' or pod == ARGV[1]) and connection_matches then
	redis.call('DEL', KEYS[1])
	redis.call('SREM', KEYS[2], ARGV[2])
	if number ~= false and number ~= '' and number ~= ARGV[4] then
		redis.call('SREM', KEYS[3] .. number, ARGV[2])
	end
	return 1
end
return 0`)

func (s *DeviceState) SetOffline(ctx context.Context, number, hardwareID, connectionID string) {
	if hardwareID == "" {
		return
	}
	id := connectionID
	devKey := deviceKeyPrefix + hardwareID
	setKey := lineDevicesPrefix + number

	if err := setOfflineScript.Run(ctx, s.client, []string{devKey, setKey, lineDevicesPrefix}, s.podID, hardwareID, id, number).Err(); err != nil {
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

func (s *DeviceState) LineIdentityOnline(ctx context.Context, number string, lineID int64, allowLegacy bool) bool {
	hardwareIDs, err := s.client.SMembers(ctx, lineDevicesPrefix+number).Result()
	if err != nil || len(hardwareIDs) == 0 {
		return false
	}
	pipe := s.client.Pipeline()
	lineCommands := make([]*redis.StringCmd, len(hardwareIDs))
	numberCommands := make([]*redis.StringCmd, len(hardwareIDs))
	for i, hardwareID := range hardwareIDs {
		key := deviceKeyPrefix + hardwareID
		lineCommands[i] = pipe.HGet(ctx, key, "line_id")
		numberCommands[i] = pipe.HGet(ctx, key, "number")
	}
	_, _ = pipe.Exec(ctx)
	wantLineID := strconv.FormatInt(lineID, 10)
	for i := range hardwareIDs {
		storedLineID := lineCommands[i].Val()
		if (storedLineID == wantLineID || (allowLegacy && (storedLineID == "" || storedLineID == "0"))) && numberCommands[i].Val() == number {
			return true
		}
	}
	return false
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

// HardwareOnlineOnLine reports whether the device with this hardware id is
// currently connected and registered on number. Stricter than
// IsHardwareOnline: a device that came back on a different line (renumber
// mid-window) does not count.
func (s *DeviceState) HardwareOnlineOnLine(ctx context.Context, number, hardwareID string) bool {
	got, err := s.client.HGet(ctx, deviceKeyPrefix+hardwareID, "number").Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.ErrorContext(ctx, "redis: HardwareOnlineOnLine failed", "hardware_id", hardwareID, "err", err)
		}
		return false
	}
	return got == number
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
	return s.allDeviceInfo(ctx, number, 0, true, false)
}

func (s *DeviceState) AllDeviceInfoForIdentity(ctx context.Context, number string, lineID int64, allowLegacy bool) []DeviceInfoSnapshot {
	return s.allDeviceInfo(ctx, number, lineID, allowLegacy, true)
}

func (s *DeviceState) allDeviceInfo(ctx context.Context, number string, lineID int64, allowLegacy, enforceIdentity bool) []DeviceInfoSnapshot {
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
		if vals["number"] != number || (enforceIdentity && vals["line_id"] != strconv.FormatInt(lineID, 10) && (!allowLegacy || (vals["line_id"] != "" && vals["line_id"] != "0"))) {
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

var updateDeviceInfoScript = redis.NewScript(`
local connection = redis.call('HGET', KEYS[1], 'connection_id')
if connection == false or connection == '' or connection ~= ARGV[1] then
	return 0
end
for i = 2, #ARGV, 2 do
	redis.call('HSET', KEYS[1], ARGV[i], ARGV[i + 1])
end
return 1`)

func (s *DeviceState) UpdateDeviceInfo(ctx context.Context, hardwareID, connectionID string, p DevicePresence) {
	if hardwareID == "" || connectionID == "" {
		return
	}
	args := []any{connectionID}
	appendField := func(name, value string) {
		if value != "" {
			args = append(args, name, value)
		}
	}
	appendField("pod_id", p.PodID)
	appendField("pi_version", p.PiVersion)
	appendField("pi_commit", p.PiCommit)
	appendField("fw_version", p.FirmwareVersion)
	appendField("fw_commit", p.FirmwareCommit)
	appendField("remote_addr", p.RemoteAddr)
	args = append(args, "dev_mode", strconv.FormatBool(p.DevMode))

	if err := updateDeviceInfoScript.Run(ctx, s.client, []string{deviceKeyPrefix + hardwareID}, args...).Err(); err != nil {
		slog.ErrorContext(ctx, "redis: UpdateDeviceInfo failed", "hardware_id", hardwareID, "err", err)
	}
}

var touchLastSeenScript = redis.NewScript(`
local connection = redis.call('HGET', KEYS[1], 'connection_id')
local number = redis.call('HGET', KEYS[1], 'number')
if connection == false or connection == '' or connection ~= ARGV[1] or number ~= ARGV[2] then
	return 0
end
redis.call('HSET', KEYS[1], 'last_seen', ARGV[3])
redis.call('EXPIRE', KEYS[1], ARGV[4])
redis.call('EXPIRE', KEYS[2], ARGV[4])
return 1`)

func (s *DeviceState) TouchLastSeen(ctx context.Context, number, hardwareID, connectionID string) {
	if hardwareID == "" || connectionID == "" || number == "" {
		return
	}
	if err := touchLastSeenScript.Run(ctx, s.client,
		[]string{deviceKeyPrefix + hardwareID, lineDevicesPrefix + number},
		connectionID, number, time.Now().Unix(), int(deviceTTL.Seconds())).Err(); err != nil {
		slog.ErrorContext(ctx, "redis: TouchLastSeen failed", "hardware_id", hardwareID, "err", err)
	}
}

func (s *DeviceState) LastSeenAt(ctx context.Context, number string) *time.Time {
	return s.lastSeenAt(ctx, number, 0, true, false)
}

func (s *DeviceState) LastSeenAtForIdentity(ctx context.Context, number string, lineID int64, allowLegacy bool) *time.Time {
	return s.lastSeenAt(ctx, number, lineID, allowLegacy, true)
}

func (s *DeviceState) lastSeenAt(ctx context.Context, number string, lineID int64, allowLegacy, enforceIdentity bool) *time.Time {
	setKey := lineDevicesPrefix + number
	hwIDs, err := s.client.SMembers(ctx, setKey).Result()
	if err != nil || len(hwIDs) == 0 {
		if err != nil {
			slog.ErrorContext(ctx, "redis: LastSeenAt SMembers failed", "number", number, "err", err)
		}
		return nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(hwIDs))
	for i, hwID := range hwIDs {
		cmds[i] = pipe.HGetAll(ctx, deviceKeyPrefix+hwID)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		slog.ErrorContext(ctx, "redis: LastSeenAt pipeline failed", "number", number, "err", err)
		return nil
	}

	wantLineID := strconv.FormatInt(lineID, 10)
	var latest time.Time
	for _, cmd := range cmds {
		vals, err := cmd.Result()
		if err != nil || vals["number"] != number {
			continue
		}
		storedLineID := vals["line_id"]
		if enforceIdentity && storedLineID != wantLineID && (!allowLegacy || (storedLineID != "" && storedLineID != "0")) {
			continue
		}
		unix, err := strconv.ParseInt(vals["last_seen"], 10, 64)
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

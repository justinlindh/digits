package calls

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	callKeyPrefix      = "digits:call:"
	activeCallsCounter = "digits:counter:active-calls"
)

type callEntry struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	StartedAt time.Time `json:"started_at"`
}

type CallState struct {
	client redis.UniversalClient
}

func NewCallState(client redis.UniversalClient) *CallState {
	return &CallState{client: client}
}

func (s *CallState) OnCallInitiated(ctx context.Context, callID int64, caller, callee string) {
	now := time.Now()

	callerEntry, err := json.Marshal(callEntry{ID: callID, Role: "caller", StartedAt: now})
	if err != nil {
		slog.Error("redis: marshal caller entry failed", "err", err)
		return
	}
	calleeEntry, err := json.Marshal(callEntry{ID: callID, Role: "callee", StartedAt: now})
	if err != nil {
		slog.Error("redis: marshal callee entry failed", "err", err)
		return
	}

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, callKeyPrefix+caller, callee, string(callerEntry))
	pipe.HSet(ctx, callKeyPrefix+callee, caller, string(calleeEntry))
	pipe.Incr(ctx, activeCallsCounter)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("redis: OnCallInitiated failed", "callID", callID, "err", err)
	}
}

func (s *CallState) OnCallEnded(ctx context.Context, caller, callee string) {
	pipe := s.client.Pipeline()
	pipe.HDel(ctx, callKeyPrefix+caller, callee)
	pipe.HDel(ctx, callKeyPrefix+callee, caller)
	pipe.Decr(ctx, activeCallsCounter)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("redis: OnCallEnded failed", "caller", caller, "callee", callee, "err", err)
		return
	}

	// Clean up empty hash keys
	s.deleteIfEmpty(ctx, callKeyPrefix+caller)
	s.deleteIfEmpty(ctx, callKeyPrefix+callee)
}

func (s *CallState) ClearByNumber(ctx context.Context, number string) {
	key := callKeyPrefix + number
	entries, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		slog.Error("redis: ClearByNumber HGetAll failed", "number", number, "err", err)
		return
	}
	if len(entries) == 0 {
		return
	}

	pipe := s.client.Pipeline()
	pipe.Del(ctx, key)
	for peer := range entries {
		pipe.HDel(ctx, callKeyPrefix+peer, number)
		pipe.Decr(ctx, activeCallsCounter)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("redis: ClearByNumber pipeline failed", "number", number, "err", err)
		return
	}

	// Clean up peer keys that may now be empty
	for peer := range entries {
		s.deleteIfEmpty(ctx, callKeyPrefix+peer)
	}
}

func (s *CallState) Busy(ctx context.Context, number string) bool {
	n, err := s.client.Exists(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.Error("redis: Busy failed", "number", number, "err", err)
		return false
	}
	return n > 0
}

func (s *CallState) PeerOf(ctx context.Context, number string) string {
	entries, err := s.client.HGetAll(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.Error("redis: PeerOf failed", "number", number, "err", err)
		return ""
	}
	for peer := range entries {
		return peer
	}
	return ""
}

func (s *CallState) AllPeersOf(ctx context.Context, number string) []string {
	keys, err := s.client.HKeys(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.Error("redis: AllPeersOf failed", "number", number, "err", err)
		return nil
	}
	return keys
}

func (s *CallState) InCall(ctx context.Context, a, b string) bool {
	exists, err := s.client.HExists(ctx, callKeyPrefix+a, b).Result()
	if err != nil {
		slog.Error("redis: InCall failed", "a", a, "b", b, "err", err)
		return false
	}
	return exists
}

func (s *CallState) CallIDFor(ctx context.Context, number string) (int64, bool) {
	entries, err := s.client.HGetAll(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.Error("redis: CallIDFor failed", "number", number, "err", err)
		return 0, false
	}
	for _, raw := range entries {
		var e callEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		return e.ID, true
	}
	return 0, false
}

func (s *CallState) CallIDForPair(ctx context.Context, a, b string) int64 {
	raw, err := s.client.HGet(ctx, callKeyPrefix+a, b).Result()
	if err == nil {
		var e callEntry
		if json.Unmarshal([]byte(raw), &e) == nil {
			return e.ID
		}
	}

	raw, err = s.client.HGet(ctx, callKeyPrefix+b, a).Result()
	if err == nil {
		var e callEntry
		if json.Unmarshal([]byte(raw), &e) == nil {
			return e.ID
		}
	}

	return 0
}

func (s *CallState) CanAddAsHost(ctx context.Context, number string) bool {
	entries, err := s.client.HGetAll(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.Error("redis: CanAddAsHost failed", "number", number, "err", err)
		return false
	}
	if len(entries) != 1 {
		return false
	}
	for _, raw := range entries {
		var e callEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return false
		}
		return e.Role == "caller"
	}
	return false
}

func (s *CallState) Active(ctx context.Context) []activeCall {
	seen := make(map[int64]bool)
	var calls []activeCall

	iter := s.client.Scan(ctx, 0, callKeyPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		number := key[len(callKeyPrefix):]

		entries, err := s.client.HGetAll(ctx, key).Result()
		if err != nil {
			continue
		}

		for peer, raw := range entries {
			var e callEntry
			if err := json.Unmarshal([]byte(raw), &e); err != nil {
				continue
			}
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true

			var caller, callee string
			if e.Role == "caller" {
				caller = number
				callee = peer
			} else {
				caller = peer
				callee = number
			}

			calls = append(calls, activeCall{
				ID:        e.ID,
				Caller:    caller,
				Callee:    callee,
				StartedAt: e.StartedAt,
			})
		}
	}
	if err := iter.Err(); err != nil {
		slog.Error("redis: Active scan failed", "err", err)
	}
	return calls
}

func (s *CallState) deleteIfEmpty(ctx context.Context, key string) {
	n, err := s.client.HLen(ctx, key).Result()
	if err != nil {
		return
	}
	if n == 0 {
		_ = s.client.Del(ctx, key).Err()
	}
}

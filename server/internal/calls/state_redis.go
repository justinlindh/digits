package calls

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	callKeyPrefix = "digits:call:"
	// Safety-net TTL for call keys. Normal end/disconnect paths delete
	// keys explicitly; this only fires if a pod crashes mid-call.
	callTTL = 30 * time.Minute
)

// callEntry.Role values for a 2-party call. Written by OnCallInitiated and
// read back by Active/CanAddAsHost, so the writer and readers must agree.
const (
	roleCaller = "caller"
	roleCallee = "callee"
)

type callEntry struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	StartedAt time.Time `json:"started_at"`
}

// CallState mirrors active-call membership in Redis so that every pod in a
// cluster can determine which calls a phone is currently participating in.
type CallState struct {
	client redis.UniversalClient
}

// NewCallState returns a CallState backed by the given Redis client.
func NewCallState(client redis.UniversalClient) *CallState {
	return &CallState{client: client}
}

// OnCallInitiated records caller and callee as participants in callID, each
// pointing at the other with a safety-net TTL. Redis errors are logged and
// swallowed; the call still proceeds in memory on the originating pod.
func (s *CallState) OnCallInitiated(ctx context.Context, callID int64, caller, callee string) {
	now := time.Now()

	callerEntry, err := json.Marshal(callEntry{ID: callID, Role: roleCaller, StartedAt: now})
	if err != nil {
		slog.ErrorContext(ctx, "redis: marshal caller entry failed", "err", err)
		return
	}
	calleeEntry, err := json.Marshal(callEntry{ID: callID, Role: roleCallee, StartedAt: now})
	if err != nil {
		slog.ErrorContext(ctx, "redis: marshal callee entry failed", "err", err)
		return
	}

	callerKey := callKeyPrefix + caller
	calleeKey := callKeyPrefix + callee
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, callerKey, callee, string(callerEntry))
	pipe.HSet(ctx, calleeKey, caller, string(calleeEntry))
	pipe.Expire(ctx, callerKey, callTTL)
	pipe.Expire(ctx, calleeKey, callTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: OnCallInitiated failed", "callID", callID, "err", err)
	}
}

// OnCallEnded removes the membership link between caller and callee and prunes
// either hash key if it is left empty. Redis errors are logged and swallowed.
func (s *CallState) OnCallEnded(ctx context.Context, caller, callee string) {
	pipe := s.client.Pipeline()
	pipe.HDel(ctx, callKeyPrefix+caller, callee)
	pipe.HDel(ctx, callKeyPrefix+callee, caller)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: OnCallEnded failed", "caller", caller, "callee", callee, "err", err)
		return
	}

	// Clean up empty hash keys
	s.deleteIfEmpty(ctx, callKeyPrefix+caller)
	s.deleteIfEmpty(ctx, callKeyPrefix+callee)
}

// ClearByNumber removes number from every call it participates in, including
// the back-references held by its peers, and prunes any keys left empty. Used
// to reset a phone's call state on disconnect. Redis errors are logged and
// swallowed.
func (s *CallState) ClearByNumber(ctx context.Context, number string) {
	key := callKeyPrefix + number
	entries, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: ClearByNumber HGetAll failed", "number", number, "err", err)
		return
	}
	if len(entries) == 0 {
		return
	}

	pipe := s.client.Pipeline()
	pipe.Del(ctx, key)
	for peer := range entries {
		pipe.HDel(ctx, callKeyPrefix+peer, number)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: ClearByNumber pipeline failed", "number", number, "err", err)
		return
	}

	// Clean up peer keys that may now be empty
	for peer := range entries {
		s.deleteIfEmpty(ctx, callKeyPrefix+peer)
	}
}

// Busy reports whether number is currently in any call. On a Redis error it
// logs and returns false, so an outage fails open rather than blocking calls.
func (s *CallState) Busy(ctx context.Context, number string) bool {
	n, err := s.client.Exists(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: Busy failed", "number", number, "err", err)
		return false
	}
	return n > 0
}

// PeerOf returns one peer number sharing a call with number, or "" if there is
// none. With multiple peers (conference) the choice is arbitrary; use AllPeersOf
// when every peer matters. Redis errors are logged and return "".
func (s *CallState) PeerOf(ctx context.Context, number string) string {
	entries, err := s.client.HGetAll(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: PeerOf failed", "number", number, "err", err)
		return ""
	}
	for peer := range entries {
		return peer
	}
	return ""
}

// AllPeersOf returns every number sharing a call with number. Redis errors are
// logged and return nil.
func (s *CallState) AllPeersOf(ctx context.Context, number string) []string {
	keys, err := s.client.HKeys(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: AllPeersOf failed", "number", number, "err", err)
		return nil
	}
	return keys
}

// InCall reports whether a and b are participants in the same call. On a Redis
// error it logs and returns false.
func (s *CallState) InCall(ctx context.Context, a, b string) bool {
	exists, err := s.client.HExists(ctx, callKeyPrefix+a, b).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: InCall failed", "a", a, "b", b, "err", err)
		return false
	}
	return exists
}

// CallIDFor returns the ID of a call number is participating in and whether one
// was found. With multiple peers any one call ID may be returned. Redis errors
// are logged and return (0, false).
func (s *CallState) CallIDFor(ctx context.Context, number string) (int64, bool) {
	entries, err := s.client.HGetAll(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: CallIDFor failed", "number", number, "err", err)
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

// CallIDForPair returns the ID of the call shared by a and b, checking the link
// in both directions, or 0 if they are not in a call together or on any error.
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

// CanAddAsHost reports whether number may host a conference: it must be in
// exactly one call and be the caller of it. Redis errors are logged and return
// false.
func (s *CallState) CanAddAsHost(ctx context.Context, number string) bool {
	entries, err := s.client.HGetAll(ctx, callKeyPrefix+number).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: CanAddAsHost failed", "number", number, "err", err)
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
		return e.Role == roleCaller
	}
	return false
}

// Active scans all call keys and returns one ActiveCall per distinct call ID,
// de-duplicating the two-sided membership entries. Keys that error mid-scan are
// skipped; a scan error is logged and the partial result is returned.
func (s *CallState) Active(ctx context.Context) []ActiveCall {
	seen := make(map[int64]bool)
	var calls []ActiveCall

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
			if e.Role == roleCaller {
				caller = number
				callee = peer
			} else {
				caller = peer
				callee = number
			}

			calls = append(calls, ActiveCall{
				ID:        e.ID,
				Caller:    caller,
				Callee:    callee,
				StartedAt: e.StartedAt,
			})
		}
	}
	if err := iter.Err(); err != nil {
		slog.ErrorContext(ctx, "redis: Active scan failed", "err", err)
	}
	return calls
}

var deleteIfEmptyScript = redis.NewScript(`
if redis.call('HLEN', KEYS[1]) == 0 then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func (s *CallState) deleteIfEmpty(ctx context.Context, key string) {
	_ = deleteIfEmptyScript.Run(ctx, s.client, []string{key}).Err()
}

package calls

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	confDetailPrefix = "digits:conf:"
	confMemberPrefix = "digits:conference-member:"
	// Safety-net TTL for conference keys. Normal end/drop paths delete
	// keys explicitly; this only fires if a pod crashes mid-conference.
	confTTL = 30 * time.Minute
)

type redisConference struct {
	ID                uuid.UUID `json:"id"`
	Host              string    `json:"host"`
	OriginatingCallID int64     `json:"originating_call_id"`
	Members           []string  `json:"members"`
	CreatedAt         time.Time `json:"created_at"`
}

// ConfState manages conference membership state in Redis.
type ConfState struct {
	client redis.UniversalClient
}

// NewConfState returns a ConfState backed by the given Redis client.
func NewConfState(client redis.UniversalClient) *ConfState {
	return &ConfState{client: client}
}

// Create stores a new conference in Redis, writing the detail key and a member
// key for each participant.
func (cs *ConfState) Create(ctx context.Context, confID uuid.UUID, host string, originatingCallID int64, members []string) {
	rc := redisConference{
		ID:                confID,
		Host:              host,
		OriginatingCallID: originatingCallID,
		Members:           members,
		CreatedAt:         time.Now(),
	}
	data, err := json.Marshal(rc)
	if err != nil {
		slog.ErrorContext(ctx, "redis: marshal conference failed", "confID", confID, "err", err)
		return
	}

	pipe := cs.client.Pipeline()
	pipe.Set(ctx, confDetailPrefix+confID.String(), string(data), confTTL)
	for _, m := range members {
		pipe.Set(ctx, confMemberPrefix+m, confID.String(), confTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.ErrorContext(ctx, "redis: ConfState.Create failed", "confID", confID, "err", err)
	}
}

// IsBusy returns true if the phone number is a member of any active conference.
func (cs *ConfState) IsBusy(ctx context.Context, phone string) bool {
	n, err := cs.client.Exists(ctx, confMemberPrefix+phone).Result()
	if err != nil {
		slog.ErrorContext(ctx, "redis: ConfState.IsBusy failed", "phone", phone, "err", err)
		return false
	}
	return n > 0
}

// ConferenceByPhone looks up the active conference for a phone number, or
// returns nil if the phone is not in any conference.
func (cs *ConfState) ConferenceByPhone(ctx context.Context, phone string) *Conference {
	idStr, err := cs.client.Get(ctx, confMemberPrefix+phone).Result()
	if err != nil {
		return nil
	}
	confID, err := uuid.Parse(idStr)
	if err != nil {
		slog.ErrorContext(ctx, "redis: ConfState.ConferenceByPhone bad UUID", "raw", idStr, "err", err)
		return nil
	}
	return cs.loadConference(ctx, confID)
}

// Contains checks whether both phoneA and phoneB are members of the given
// conference.
func (cs *ConfState) Contains(ctx context.Context, confID uuid.UUID, phoneA, phoneB string) bool {
	data, err := cs.client.Get(ctx, confDetailPrefix+confID.String()).Result()
	if err != nil {
		return false
	}
	var rc redisConference
	if err := json.Unmarshal([]byte(data), &rc); err != nil {
		slog.ErrorContext(ctx, "redis: ConfState.Contains unmarshal failed", "confID", confID, "err", err)
		return false
	}
	hasA, hasB := false, false
	for _, m := range rc.Members {
		if m == phoneA {
			hasA = true
		}
		if m == phoneB {
			hasB = true
		}
	}
	return hasA && hasB
}

// RemoveMember deletes a single member's key from Redis.
func (cs *ConfState) RemoveMember(ctx context.Context, confID uuid.UUID, phone string) {
	if err := cs.client.Del(ctx, confMemberPrefix+phone).Err(); err != nil {
		slog.ErrorContext(ctx, "redis: ConfState.RemoveMember failed", "confID", confID, "phone", phone, "err", err)
	}
}

// End removes the conference detail key and all member keys.
func (cs *ConfState) End(ctx context.Context, confID uuid.UUID, members []string) {
	keys := make([]string, 0, 1+len(members))
	keys = append(keys, confDetailPrefix+confID.String())
	for _, m := range members {
		keys = append(keys, confMemberPrefix+m)
	}
	if err := cs.client.Del(ctx, keys...).Err(); err != nil {
		slog.ErrorContext(ctx, "redis: ConfState.End failed", "confID", confID, "err", err)
	}
}

func (cs *ConfState) loadConference(ctx context.Context, confID uuid.UUID) *Conference {
	data, err := cs.client.Get(ctx, confDetailPrefix+confID.String()).Result()
	if err != nil {
		return nil
	}
	var rc redisConference
	if err := json.Unmarshal([]byte(data), &rc); err != nil {
		slog.ErrorContext(ctx, "redis: ConfState.loadConference unmarshal failed", "confID", confID, "err", err)
		return nil
	}

	conf := &Conference{
		ID:                rc.ID,
		Host:              rc.Host,
		OriginatingCallID: rc.OriginatingCallID,
		Members:           make(map[string]*ConferenceMember, len(rc.Members)),
		State:             ConferenceStateActive,
		CreatedAt:         rc.CreatedAt,
	}
	for _, m := range rc.Members {
		role := ConferenceRoleAdded
		if m == rc.Host {
			role = ConferenceRoleHost
		}
		conf.Members[m] = &ConferenceMember{
			Phone:    m,
			Role:     role,
			JoinedAt: rc.CreatedAt,
		}
	}
	return conf
}

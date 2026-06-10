package calls

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// linkHealthChannel is the shared Redis pub/sub channel for cross-pod
// link-health fan-out. Every pod publishes its locally ingested samples and
// locally triggered lifecycle events here and applies everyone else's, so
// the in-memory rings and live SSE subscribers stay complete on every
// replica regardless of which pod each phone's WebSocket landed on.
const linkHealthChannel = "digits:linkhealth"

// redisPublisher is the subset of redis.UniversalClient the HealthStore
// uses. Narrowed for testability.
type redisPublisher interface {
	Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}

// Wire event kinds. Distinct from EventKind: EndedKind is never sent on the
// wire; it is produced locally by applying an evict.
const (
	wireKindSample uint8 = iota
	wireKindDisconnect
	wireKindEvict
)

// healthEnvelope is the JSON wire format for one cross-pod link-health
// event. Exactly one of CallID / ConfID is set, mirroring SessionKey.
type healthEnvelope struct {
	PodID   string      `json:"pod"`
	Kind    uint8       `json:"kind"`
	CallID  int64       `json:"call_id,omitempty"`
	ConfID  string      `json:"conf_id,omitempty"`
	From    string      `json:"from,omitempty"`
	Peer    string      `json:"peer,omitempty"`
	EndedBy string      `json:"ended_by,omitempty"`
	Sample  *wireSample `json:"sample,omitempty"`
}

// wireSample is Sample with a unix-millisecond timestamp for compact JSON.
type wireSample struct {
	TS       int64    `json:"ts"`
	LossPct  *float32 `json:"loss,omitempty"`
	JitterMs *float32 `json:"jitter,omitempty"`
	RttMs    *float32 `json:"rtt,omitempty"`
	ConnType string   `json:"conn,omitempty"`
	BytesIn  *int64   `json:"bytes_in,omitempty"`
	BytesOut *int64   `json:"bytes_out,omitempty"`
}

func toWireSample(s Sample) *wireSample {
	return &wireSample{
		TS:       s.TS.UnixMilli(),
		LossPct:  s.LossPct,
		JitterMs: s.JitterMs,
		RttMs:    s.RttMs,
		ConnType: s.ConnType,
		BytesIn:  s.BytesIn,
		BytesOut: s.BytesOut,
	}
}

func (w *wireSample) toSample() Sample {
	return Sample{
		TS:       time.UnixMilli(w.TS),
		LossPct:  w.LossPct,
		JitterMs: w.JitterMs,
		RttMs:    w.RttMs,
		ConnType: w.ConnType,
		BytesIn:  w.BytesIn,
		BytesOut: w.BytesOut,
	}
}

// sessionKeyFromEnvelope rebuilds the SessionKey. ok is false when the
// envelope carries neither a call id nor a parseable conference id.
func sessionKeyFromEnvelope(env *healthEnvelope) (SessionKey, bool) {
	if env.CallID != 0 {
		return SessionKey{CallID: env.CallID}, true
	}
	if env.ConfID != "" {
		id, err := uuid.Parse(env.ConfID)
		if err != nil {
			return SessionKey{}, false
		}
		return SessionKey{ConfID: id}, true
	}
	return SessionKey{}, false
}

func envelopeForKey(key SessionKey) healthEnvelope {
	if key.IsConf() {
		return healthEnvelope{ConfID: key.ConfID.String()}
	}
	return healthEnvelope{CallID: key.CallID}
}

// SetRedis configures Redis pub/sub for cross-pod link-health fan-out.
// Pass nil to disable (single-instance mode). podID identifies this
// process so RunRedis can skip its own publications.
func (s *HealthStore) SetRedis(client redisPublisher, podID string) {
	s.rmu.Lock()
	s.client = client
	s.podID = podID
	s.rmu.Unlock()
}

// RunRedis subscribes to the link-health channel and applies events from
// other pods to the local store. Returns when ctx is cancelled or the
// subscription channel closes. No-op if SetRedis was not called.
func (s *HealthStore) RunRedis(ctx context.Context) {
	s.rmu.Lock()
	client := s.client
	podID := s.podID
	s.rmu.Unlock()
	if client == nil {
		return
	}

	sub := client.Subscribe(ctx, linkHealthChannel)
	defer func() { _ = sub.Close() }()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var env healthEnvelope
			if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
				slog.Warn("link_health: bad redis envelope", "err", err)
				continue
			}
			if env.PodID == podID {
				continue
			}
			s.applyRemote(&env)
		}
	}
}

// applyRemote dispatches one cross-pod event into the local store. It calls
// the pod-local session operations directly, never the publishing public
// methods, so there is no echo loop.
func (s *HealthStore) applyRemote(env *healthEnvelope) {
	key, ok := sessionKeyFromEnvelope(env)
	if !ok {
		slog.Warn("link_health: redis envelope without session key", "kind", env.Kind)
		return
	}
	switch env.Kind {
	case wireKindSample:
		if env.Sample == nil {
			return
		}
		s.recordSession(key, env.From, env.Peer, env.Sample.toSample(), true)
	case wireKindDisconnect:
		s.notifyDisconnectedSession(key, env.EndedBy)
	case wireKindEvict:
		s.evictSession(key)
	default:
		slog.Warn("link_health: unknown redis event kind", "kind", env.Kind)
	}
}

// publishSample fans a locally ingested sample out to the other pods.
// Best-effort: a publish failure loses cross-pod liveness for that sample
// but never the local ring or the DB flush.
func (s *HealthStore) publishSample(key SessionKey, from, peer string, sample Sample) {
	env := envelopeForKey(key)
	env.Kind = wireKindSample
	env.From = from
	env.Peer = peer
	env.Sample = toWireSample(sample)
	s.publish(&env)
}

// publishLifecycle fans a locally triggered evict or disconnect out to the
// other pods so their subscribers observe the call end too.
func (s *HealthStore) publishLifecycle(kind uint8, key SessionKey, endedBy string) {
	env := envelopeForKey(key)
	env.Kind = kind
	env.EndedBy = endedBy
	s.publish(&env)
}

func (s *HealthStore) publish(env *healthEnvelope) {
	s.rmu.Lock()
	client := s.client
	podID := s.podID
	s.rmu.Unlock()
	if client == nil {
		return
	}
	env.PodID = podID
	payload, err := json.Marshal(env)
	if err != nil {
		slog.Error("link_health: marshal redis envelope failed", "err", err)
		return
	}
	if err := client.Publish(context.Background(), linkHealthChannel, payload).Err(); err != nil {
		slog.Error("link_health: redis publish failed", "err", err)
	}
}

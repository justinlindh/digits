package signaling

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// fakeRedis is a test double for redisPubSub that records published
// envelopes and allows tests to simulate incoming messages.
type fakeRedis struct {
	mu        sync.Mutex
	published []*Envelope
	incoming  chan *Envelope
}

var _ redisPubSub = (*fakeRedis)(nil)

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		incoming: make(chan *Envelope, 64),
	}
}

func (f *fakeRedis) Publish(_ context.Context, env *Envelope) {
	f.mu.Lock()
	f.published = append(f.published, env)
	f.mu.Unlock()
}

func (f *fakeRedis) Subscribe(ctx context.Context) <-chan *Envelope {
	ch := make(chan *Envelope, 64)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case env, ok := <-f.incoming:
				if !ok {
					return
				}
				ch <- env
			}
		}
	}()
	return ch
}

func (f *fakeRedis) publishedEnvelopes() []*Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*Envelope, len(f.published))
	copy(out, f.published)
	return out
}

// --- Envelope tests ---

func TestEnvelopeMarshalRoundTrip(t *testing.T) {
	orig := &Envelope{
		PodID:      "pod-abc",
		TargetType: "number",
		Target:     "3140001",
		Message:    &Message{Type: TypeRing, From: "3140002"},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Envelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PodID != orig.PodID {
		t.Errorf("PodID = %q, want %q", got.PodID, orig.PodID)
	}
	if got.TargetType != orig.TargetType {
		t.Errorf("TargetType = %q, want %q", got.TargetType, orig.TargetType)
	}
	if got.Target != orig.Target {
		t.Errorf("Target = %q, want %q", got.Target, orig.Target)
	}
	if got.Message == nil || got.Message.Type != TypeRing {
		t.Errorf("Message.Type = %v, want %q", got.Message, TypeRing)
	}
}

// --- SendTo with Redis fallback ---

func TestSendToPublishesToRedisWhenNotLocal(t *testing.T) {
	hub := NewHub()
	fake := newFakeRedis()
	hub.redis = fake

	err := hub.SendTo("3140099", &Message{Type: TypeRing, From: "3140001"})
	if err != nil {
		t.Fatalf("SendTo should return nil with Redis configured: %v", err)
	}

	envs := fake.publishedEnvelopes()
	if len(envs) != 1 {
		t.Fatalf("expected 1 published envelope, got %d", len(envs))
	}
	if envs[0].TargetType != "number" {
		t.Errorf("TargetType = %q, want %q", envs[0].TargetType, "number")
	}
	if envs[0].Target != "3140099" {
		t.Errorf("Target = %q, want %q", envs[0].Target, "3140099")
	}
}

func TestSendToLocalFastPathSkipsRedis(t *testing.T) {
	hub := NewHub()
	fake := newFakeRedis()
	hub.redis = fake

	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	err := hub.SendTo("3140001", &Message{Type: TypeRing, From: "3140002"})
	if err != nil {
		t.Fatalf("SendTo local should succeed: %v", err)
	}

	if len(fake.publishedEnvelopes()) != 0 {
		t.Errorf("expected 0 published envelopes (local fast path), got %d", len(fake.publishedEnvelopes()))
	}

	select {
	case <-conn.Send:
	default:
		t.Error("local connection should have received the message")
	}
}

func TestSendToWithoutRedisReturnsError(t *testing.T) {
	hub := NewHub()
	err := hub.SendTo("3140099", &Message{Type: TypeRing})
	if err == nil {
		t.Fatal("expected error when target not local and no Redis")
	}
}

// --- SendToHardware with Redis fallback ---

func TestSendToHardwarePublishesToRedis(t *testing.T) {
	hub := NewHub()
	fake := newFakeRedis()
	hub.redis = fake

	err := hub.SendToHardware("hw-unknown", &Message{Type: TypePaired})
	if err != nil {
		t.Fatalf("expected nil (optimistic delivery), got %v", err)
	}
	envs := fake.publishedEnvelopes()
	if len(envs) != 1 {
		t.Fatalf("expected 1 published envelope, got %d", len(envs))
	}
	if envs[0].TargetType != "hardware" {
		t.Errorf("TargetType = %q, want %q", envs[0].TargetType, "hardware")
	}
}

func TestSendToHardwareLocalSkipsRedis(t *testing.T) {
	hub := NewHub()
	fake := newFakeRedis()
	hub.redis = fake

	conn := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-abc"}
	_ = hub.Register("3140001", conn)

	err := hub.SendToHardware("hw-abc", &Message{Type: TypePaired})
	if err != nil {
		t.Fatalf("local delivery should succeed: %v", err)
	}
	if len(fake.publishedEnvelopes()) != 0 {
		t.Errorf("local fast path should not publish to Redis")
	}
}

// --- Broadcast with Redis ---

func TestBroadcastPublishesToRedis(t *testing.T) {
	hub := NewHub()
	fake := newFakeRedis()
	hub.redis = fake

	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	hub.Broadcast(&Message{Type: TypeReleaseAvailable, LatestPiVersion: "3.0.0"})

	select {
	case <-conn.Send:
	default:
		t.Error("local connection should have received broadcast")
	}

	envs := fake.publishedEnvelopes()
	if len(envs) != 1 {
		t.Fatalf("expected 1 published envelope, got %d", len(envs))
	}
	if envs[0].TargetType != "broadcast" {
		t.Errorf("TargetType = %q, want %q", envs[0].TargetType, "broadcast")
	}
}

// --- deliverFromRedis ---

func TestDeliverFromRedisToLocalConnection(t *testing.T) {
	hub := NewHub()
	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	hub.deliverFromRedis(&Envelope{
		PodID:      "other-pod",
		TargetType: "number",
		Target:     "3140001",
		Message:    &Message{Type: TypeRing, From: "3140002"},
	})

	select {
	case data := <-conn.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeRing {
			t.Errorf("Type = %q, want %q", msg.Type, TypeRing)
		}
	default:
		t.Error("expected message on local connection")
	}
}

func TestDeliverFromRedisSkipsMissingTarget(t *testing.T) {
	hub := NewHub()
	// No connection for "3140099"; should not panic.
	hub.deliverFromRedis(&Envelope{
		PodID:      "other-pod",
		TargetType: "number",
		Target:     "3140099",
		Message:    &Message{Type: TypeRing},
	})
}

func TestDeliverFromRedisHardware(t *testing.T) {
	hub := NewHub()
	conn := &Conn{Send: make(chan []byte, 10), HardwareID: "hw-abc"}
	_ = hub.Register("3140001", conn)

	hub.deliverFromRedis(&Envelope{
		PodID:      "other-pod",
		TargetType: "hardware",
		Target:     "hw-abc",
		Message:    &Message{Type: TypePaired},
	})

	select {
	case data := <-conn.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypePaired {
			t.Errorf("Type = %q, want %q", msg.Type, TypePaired)
		}
	default:
		t.Error("expected message on hardware connection")
	}
}

func TestDeliverFromRedisBroadcast(t *testing.T) {
	hub := NewHub()
	c1 := &Conn{Send: make(chan []byte, 10)}
	c2 := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", c1)
	_ = hub.Register("3140002", c2)

	hub.deliverFromRedis(&Envelope{
		PodID:      "other-pod",
		TargetType: "broadcast",
		Message:    &Message{Type: TypeReleaseAvailable, LatestPiVersion: "2.0.0"},
	})

	for _, tc := range []struct {
		name string
		conn *Conn
	}{
		{"c1", c1},
		{"c2", c2},
	} {
		select {
		case data := <-tc.conn.Send:
			msg, err := ParseMessage(data)
			if err != nil {
				t.Fatalf("%s: parse: %v", tc.name, err)
			}
			if msg.Type != TypeReleaseAvailable {
				t.Errorf("%s: Type = %q, want %q", tc.name, msg.Type, TypeReleaseAvailable)
			}
		default:
			t.Errorf("%s: did not receive broadcast", tc.name)
		}
	}
}

func TestDeliverFromRedisNilMessage(t *testing.T) {
	hub := NewHub()
	// Should not panic.
	hub.deliverFromRedis(&Envelope{PodID: "other-pod", TargetType: "number", Target: "3140001"})
}

// --- Run ---

func TestRunReturnsImmediatelyWithoutRedis(t *testing.T) {
	hub := NewHub()
	done := make(chan struct{})
	go func() {
		hub.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run should return immediately when redis is nil")
	}
}

func TestRunDeliversFromSubscription(t *testing.T) {
	hub := NewHub()
	fake := newFakeRedis()
	hub.redis = fake

	conn := &Conn{Send: make(chan []byte, 10)}
	_ = hub.Register("3140001", conn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()

	// Simulate an incoming message from another pod.
	fake.incoming <- &Envelope{
		PodID:      "other-pod",
		TargetType: "number",
		Target:     "3140001",
		Message:    &Message{Type: TypeRing, From: "3140002"},
	}

	select {
	case data := <-conn.Send:
		msg, err := ParseMessage(data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if msg.Type != TypeRing {
			t.Errorf("Type = %q, want %q", msg.Type, TypeRing)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delivery from Run")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

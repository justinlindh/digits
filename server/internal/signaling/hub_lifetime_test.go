package signaling

import (
	"sync"
	"testing"
)

func TestHardwareDeliveryConcurrentLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remote    bool
		replacing bool
	}{
		{name: "local/disconnect"},
		{name: "local/replacement", replacing: true},
		{name: "redis/disconnect", remote: true},
		{name: "redis/replacement", remote: true, replacing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub()
			var wg sync.WaitGroup
			start := make(chan struct{})
			panics := make(chan any, 1)

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() {
					if p := recover(); p != nil {
						panics <- p
					}
				}()
				<-start
				for range 20_000 {
					msg := &Message{Type: TypeRingTest}
					if tc.remote {
						hub.deliverFromRedis(&Envelope{
							TargetType: "hardware",
							Target:     "hw-lifetime",
							Message:    msg,
						})
					} else {
						_ = hub.SendToHardware("hw-lifetime", msg)
					}
				}
			}()

			close(start)
			conn := &Conn{
				HardwareID: "hw-lifetime",
				Send:       make(chan []byte, 1),
			}
			if err := hub.Register("3140001", conn); err != nil {
				t.Fatal(err)
			}
			for range 20_000 {
				previous := conn
				conn = &Conn{
					HardwareID: "hw-lifetime",
					Send:       make(chan []byte, 1),
				}
				if err := hub.Register("3140001", conn); err != nil {
					t.Fatal(err)
				}
				if tc.replacing {
					hub.Unregister("3140001", previous)
				} else {
					hub.Unregister("3140001", conn)
				}
			}
			hub.Unregister("3140001", conn)
			wg.Wait()

			select {
			case p := <-panics:
				t.Fatalf("delivery panicked during connection lifecycle: %v", p)
			default:
			}
		})
	}
}

func TestHardwareDeliveryFullQueueSemantics(t *testing.T) {
	t.Run("local send does not fall back to Redis", func(t *testing.T) {
		hub := NewHub()
		redis := newFakeRedis()
		hub.SetRedis(redis)
		conn := &Conn{
			HardwareID: "hw-full",
			Send:       make(chan []byte, 1),
		}
		conn.Send <- []byte("already queued")
		if err := hub.Register("3140001", conn); err != nil {
			t.Fatal(err)
		}

		err := hub.SendToHardware("hw-full", &Message{Type: TypeRingTest})
		if err == nil {
			t.Fatal("expected an error for a full local send queue")
		}
		if got := len(redis.publishedEnvelopes()); got != 0 {
			t.Fatalf("full local queue published %d Redis envelopes, want 0", got)
		}
	})

	t.Run("Redis delivery drops without replacing queued data", func(t *testing.T) {
		hub := NewHub()
		conn := &Conn{
			HardwareID: "hw-full",
			Send:       make(chan []byte, 1),
		}
		want := []byte("already queued")
		conn.Send <- want
		if err := hub.Register("3140001", conn); err != nil {
			t.Fatal(err)
		}

		hub.deliverFromRedis(&Envelope{
			TargetType: "hardware",
			Target:     "hw-full",
			Message:    &Message{Type: TypeRingTest},
		})
		if got := <-conn.Send; string(got) != string(want) {
			t.Fatalf("queued data = %q, want %q", got, want)
		}
	})

	t.Run("missing local connection falls back to Redis", func(t *testing.T) {
		hub := NewHub()
		redis := newFakeRedis()
		hub.SetRedis(redis)

		if err := hub.SendToHardware("hw-remote", &Message{Type: TypeRingTest}); err != nil {
			t.Fatalf("Redis fallback failed: %v", err)
		}
		envelopes := redis.publishedEnvelopes()
		if len(envelopes) != 1 {
			t.Fatalf("published %d Redis envelopes, want 1", len(envelopes))
		}
		if got := envelopes[0]; got.TargetType != "hardware" || got.Target != "hw-remote" {
			t.Fatalf("published target = %q/%q, want hardware/hw-remote", got.TargetType, got.Target)
		}
	})
}

package signaling

import (
	"errors"
	"sync"
	"testing"
	"time"
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

func TestRenumberDoesNotMoveLiveConnectionIdentity(t *testing.T) {
	h := NewHub()
	conn := &Conn{LineID: 17, HardwareID: "hw-renumber", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", conn); err != nil {
		t.Fatal(err)
	}

	h.SetLineResolver(nil, func(int64) (string, error) { return "3140002", nil })
	h.RenumberLine(17)

	if conn.Number != "3140001" {
		t.Fatalf("connection identity changed to %q", conn.Number)
	}
	h.Unregister(conn.Number, conn)
	if got := h.ConnectionCount("3140002"); got != 0 {
		t.Fatalf("disconnect left %d ghost connection(s) on new number", got)
	}
}

func TestSendToFiltersStaleConnectionAfterOldNumberReuse(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(func(string) (int64, error) { return 22, nil }, nil)
	stale := &Conn{LineID: 11, HardwareID: "old-line", Send: make(chan []byte, 1)}
	current := &Conn{LineID: 22, HardwareID: "new-line", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", stale); err != nil {
		t.Fatal(err)
	}
	if err := h.Register("3140001", current); err != nil {
		t.Fatal(err)
	}
	if err := h.SendTo("3140001", &Message{Type: TypeRing}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stale.Send:
		t.Fatal("stale line identity received reused-number traffic")
	default:
	}
	select {
	case <-current.Send:
	default:
		t.Fatal("current line identity did not receive traffic")
	}
}

func TestRenumberResolverFailureStillRetiresSocket(t *testing.T) {
	h := NewHub()
	h.SetLineResolver(nil, func(int64) (string, error) { return "", errors.New("database unavailable") })
	c := &Conn{LineID: 19, HardwareID: "hw-resolver", Send: make(chan []byte, 1)}
	if err := h.Register("3140019", c); err != nil {
		t.Fatal(err)
	}
	h.RenumberLine(19)
	if h.ConnIsCurrent(c) {
		t.Fatal("resolver failure left stale socket registered")
	}
}

func TestLocalLineDeliveryRejectsLegacyConnectionsAfterIdentityCutover(t *testing.T) {
	for _, tc := range []struct {
		name string
		send func(*Hub, string, *Message) error
	}{
		{name: "best effort", send: func(h *Hub, number string, msg *Message) error { return h.SendTo(number, msg) }},
		{name: "timeout", send: func(h *Hub, number string, msg *Message) error {
			return h.SendToWithTimeout(number, msg, time.Millisecond)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHub()
			h.SetLineResolver(func(string) (int64, error) { return 22, nil }, nil)
			h.SetLegacyIdentityAllowedResolver(func() (bool, error) { return false, nil })
			legacy := &Conn{HardwareID: "legacy", Send: make(chan []byte, 1)}
			if err := h.Register("3140022", legacy); err != nil {
				t.Fatal(err)
			}
			err := tc.send(h, "3140022", &Message{Type: TypeRing})
			if !errors.Is(err, ErrNotConnected) {
				t.Fatalf("send error = %v, want ErrNotConnected", err)
			}
			select {
			case <-legacy.Send:
				t.Fatal("legacy zero-ID connection received post-cutover traffic")
			default:
			}
		})
	}
}

func TestDelayedRenumberEventPreservesAlreadyCurrentConnection(t *testing.T) {
	h := NewHub()
	oldConn := &Conn{LineID: 17, HardwareID: "old-socket", Send: make(chan []byte, 1)}
	currentConn := &Conn{LineID: 17, HardwareID: "current-socket", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", oldConn); err != nil {
		t.Fatal(err)
	}
	if err := h.Register("3140002", currentConn); err != nil {
		t.Fatal(err)
	}
	h.deliverLineRenumber(17, "3140002")
	if h.ConnIsCurrent(oldConn) {
		t.Fatal("delayed renumber left stale old-number socket current")
	}
	if !h.ConnIsCurrent(currentConn) {
		t.Fatal("delayed renumber retired already-current replacement socket")
	}
	select {
	case <-currentConn.Send:
		t.Fatal("already-current socket received stale renumber control")
	default:
	}
}

func TestCrossNumberReconnectClearsOldVoicemailState(t *testing.T) {
	h := NewHub()
	oldConn := &Conn{HardwareID: "moving-handset", Send: make(chan []byte, 1)}
	if err := h.Register("3140001", oldConn); err != nil {
		t.Fatal(err)
	}
	h.SetVoicemailUnheard("3140001", oldConn.HardwareID, 3)
	newConn := &Conn{HardwareID: oldConn.HardwareID, Send: make(chan []byte, 1)}
	if err := h.Register("3140002", newConn); err != nil {
		t.Fatal(err)
	}
	if got := h.LineVoicemailUnheard("3140001"); got != 0 {
		t.Fatalf("cross-number reconnect left old voicemail count %d", got)
	}
}

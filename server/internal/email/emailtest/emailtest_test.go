package emailtest

import (
	"testing"

	"github.com/justinlindh/digits/server/internal/email"
)

func TestSenderImplementsSender(t *testing.T) {
	var _ email.Sender = NewSender()
}

func TestSenderCapturesFields(t *testing.T) {
	s := NewSender()
	if err := s.Send("to@example.com", "Hello", "<b>Hi</b>"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := s.Sent[0]
	if got.To != "to@example.com" {
		t.Errorf("To = %q", got.To)
	}
	if got.Subject != "Hello" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Body != "<b>Hi</b>" {
		t.Errorf("Body = %q", got.Body)
	}
}

func TestSenderMultiple(t *testing.T) {
	s := NewSender()
	_ = s.Send("a@example.com", "S1", "B1")
	_ = s.Send("b@example.com", "S2", "B2")
	if len(s.Sent) != 2 {
		t.Errorf("expected 2 sent, got %d", len(s.Sent))
	}
}

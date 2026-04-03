package email

import "testing"

func TestNewSMTPSender(t *testing.T) {
	s := NewSMTPSender("smtp.example.com", "587", "user", "pass", "noreply@example.com")
	if s.from != "noreply@example.com" {
		t.Errorf("from = %q", s.from)
	}
}

func TestNoopSender(t *testing.T) {
	s := NewNoopSender()
	err := s.Send("test@example.com", "Subject", "<p>Body</p>")
	if err != nil {
		t.Errorf("NoopSender.Send: %v", err)
	}
	if len(s.Sent) != 1 {
		t.Errorf("expected 1 sent, got %d", len(s.Sent))
	}
}

func TestNoopSenderCapturesFields(t *testing.T) {
	s := NewNoopSender()
	_ = s.Send("to@example.com", "Hello", "<b>Hi</b>")
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

func TestNoopSenderMultiple(t *testing.T) {
	s := NewNoopSender()
	_ = s.Send("a@example.com", "S1", "B1")
	_ = s.Send("b@example.com", "S2", "B2")
	if len(s.Sent) != 2 {
		t.Errorf("expected 2 sent, got %d", len(s.Sent))
	}
}

func TestSMTPSenderImplementsSender(t *testing.T) {
	var _ Sender = NewSMTPSender("host", "587", "u", "p", "f@example.com")
}

func TestNoopSenderImplementsSender(t *testing.T) {
	var _ Sender = NewNoopSender()
}

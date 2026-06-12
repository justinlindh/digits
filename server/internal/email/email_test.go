package email

import "testing"

func TestNewSMTPSender(t *testing.T) {
	s := NewSMTPSender("smtp.example.com", "587", "user", "pass", "noreply@example.com")
	if s.from != "noreply@example.com" {
		t.Errorf("from = %q", s.from)
	}
}

func TestSMTPSenderImplementsSender(t *testing.T) {
	var _ Sender = NewSMTPSender("host", "587", "u", "p", "f@example.com")
}

func TestLogSenderImplementsSender(t *testing.T) {
	var _ Sender = NewLogSender()
}

func TestLogSenderSendReturnsNil(t *testing.T) {
	s := NewLogSender()
	if err := s.Send("to@example.com", "subj", "body"); err != nil {
		t.Errorf("LogSender.Send: %v", err)
	}
}

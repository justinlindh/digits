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

func TestSMTPSenderRejectsHeaderInjection(t *testing.T) {
	// The CRLF guard runs before any network or address parsing, so these
	// cases exercise the injection check without contacting an SMTP server.
	tests := []struct {
		name    string
		to      string
		subject string
		from    string
	}{
		{name: "newline in to", to: "victim@example.com\r\nBcc: evil@example.com", subject: "s", from: "noreply@example.com"},
		{name: "bare LF in subject", to: "victim@example.com", subject: "hello\ninjected", from: "noreply@example.com"},
		{name: "CR in from", to: "victim@example.com", subject: "s", from: "noreply@example.com\rX: y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSMTPSender("smtp.example.com", "587", "user", "pass", tt.from)
			err := s.Send(tt.to, tt.subject, "body")
			if err == nil {
				t.Fatal("Send accepted header with embedded CR/LF; want rejection")
			}
			if err.Error() != "invalid email header value" {
				t.Errorf("err = %q, want %q", err.Error(), "invalid email header value")
			}
		})
	}
}

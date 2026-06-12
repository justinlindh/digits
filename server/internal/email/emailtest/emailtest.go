// Package emailtest provides an email.Sender fake for tests. It lives in its
// own package so the capture buffer is not compiled into production binaries.
package emailtest

// Sender captures emails in memory for test inspection. Not safe for
// production: Sent grows unboundedly. Use email.LogSender as the production
// fallback when SMTP is not configured.
type Sender struct {
	Sent []SentEmail
}

// SentEmail holds the details of a captured email.
type SentEmail struct {
	To      string
	Subject string
	Body    string
}

// NewSender returns a Sender with an empty Sent slice.
func NewSender() *Sender {
	return &Sender{}
}

func (s *Sender) Send(to, subject, htmlBody string) error {
	s.Sent = append(s.Sent, SentEmail{To: to, Subject: subject, Body: htmlBody})
	return nil
}

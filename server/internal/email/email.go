// Package email provides a Sender interface and two implementations:
// SMTPSender for production mail delivery and LogSender for development
// environments that log the link to stdout instead of sending a message.
package email

import (
	"errors"
	"log/slog"
	"mime"
	"net/mail"
	"net/smtp"
	"strings"
)

// Sender is the interface for sending emails.
type Sender interface {
	Send(to, subject, htmlBody string) error
}

// Normalize canonicalizes an address for storage, lookup, and comparison by
// trimming surrounding whitespace and lowercasing. Every email-keyed path
// (magic-link login, Google login, household invites) funnels through this so
// "John@Example.com" and "john@example.com" resolve to the same account
// instead of silently diverging (the users.email UNIQUE constraint is
// case-sensitive). Keeping it here means the login and invite flows cannot
// drift out of lockstep.
func Normalize(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// SMTPSender sends email via SMTP.
type SMTPSender struct {
	host string
	port string
	user string
	pass string
	from string
}

// NewSMTPSender returns an SMTPSender that authenticates with the given
// credentials and uses from as the envelope sender address.
func NewSMTPSender(host, port, user, pass, from string) *SMTPSender {
	return &SMTPSender{host: host, port: port, user: user, pass: pass, from: from}
}

func (s *SMTPSender) Send(to, subject, htmlBody string) error {
	// Prevent header injection via CRLF in header values.
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(subject, "\r\n") || strings.ContainsAny(s.from, "\r\n") {
		return errors.New("invalid email header value")
	}

	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	fromAddr, err := mail.ParseAddress(s.from)
	if err != nil {
		return err
	}

	// Disable SendGrid click tracking and open tracking via SMTPAPI header.
	// Without this, SendGrid rewrites URLs in emails through its tracking
	// servers, which can cause TLS certificate errors on custom subdomains.
	headers := []string{
		"From: " + fromAddr.String(),
		"To: " + toAddr.String(),
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		`X-SMTPAPI: {"filters":{"clicktrack":{"settings":{"enable":0}},"opentrack":{"settings":{"enable":0}}}}`,
	}
	msg := strings.Join(headers, "\r\n") + "\r\n\r\n" + htmlBody
	var auth smtp.Auth
	if s.user != "" {
		auth = smtp.PlainAuth("", s.user, s.pass, s.host)
	}
	return smtp.SendMail(s.host+":"+s.port, auth, fromAddr.Address, []string{toAddr.Address}, []byte(msg))
}

// LogSender writes each outbound email to the structured logger and drops
// it. Intended as the production fallback when SMTP is intentionally
// unconfigured (single-tenant deployments, local demos), so the operator
// sees magic-link URLs in journalctl rather than silently losing them.
// Bodies can be long; only the subject and recipient are logged.
type LogSender struct{}

// NewLogSender returns a LogSender that writes to the structured logger.
func NewLogSender() *LogSender {
	return &LogSender{}
}

func (s *LogSender) Send(to, subject, _ string) error {
	slog.Warn("email dropped (no SMTP configured)", "to", to, "subject", subject)
	return nil
}

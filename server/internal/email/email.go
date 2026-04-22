package email

import (
	"errors"
	"mime"
	"net/mail"
	"net/smtp"
	"strings"
)

// Sender is the interface for sending emails.
type Sender interface {
	Send(to, subject, htmlBody string) error
}

// SMTPSender sends email via SMTP.
type SMTPSender struct {
	host string
	port string
	user string
	pass string
	from string
}

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

// NoopSender captures emails for testing.
type NoopSender struct {
	Sent []SentEmail
}

// SentEmail holds the details of a captured email.
type SentEmail struct {
	To      string
	Subject string
	Body    string
}

func NewNoopSender() *NoopSender {
	return &NoopSender{}
}

func (s *NoopSender) Send(to, subject, htmlBody string) error {
	s.Sent = append(s.Sent, SentEmail{To: to, Subject: subject, Body: htmlBody})
	return nil
}

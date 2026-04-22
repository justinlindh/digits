package autodeploy

import (
	"fmt"
	"net/smtp"
	"strings"
)

type Dialer interface {
	Send(host, port, user, pass, from string, to []string, msg []byte) error
}

type smtpDialer struct{}

func (smtpDialer) Send(host, port, user, pass, from string, to []string, msg []byte) error {
	addr := host + ":" + port
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	return smtp.SendMail(addr, auth, from, to, msg)
}

type Mailer struct {
	host, port, user, pass, from string
	d                            Dialer
}

func NewMailer(host, port, user, pass, from string, d Dialer) *Mailer {
	if d == nil {
		d = smtpDialer{}
	}
	return &Mailer{host: host, port: port, user: user, pass: pass, from: from, d: d}
}

type EmailInput struct {
	To      string
	Subject string
	Body    string
}

func (m *Mailer) Send(in EmailInput) error {
	msg := buildMessage(m.from, in.To, in.Subject, in.Body)
	return m.d.Send(m.host, m.port, m.user, m.pass, m.from, []string{in.To}, msg)
}

func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

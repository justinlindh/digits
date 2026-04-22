package autodeploy

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type fakeDialer struct {
	sent []fakeMail
	fail error
}

type fakeMail struct {
	from string
	to   []string
	msg  []byte
}

func (d *fakeDialer) Send(host, port, user, pass, from string, to []string, msg []byte) error {
	if d.fail != nil {
		return d.fail
	}
	d.sent = append(d.sent, fakeMail{from: from, to: to, msg: bytes.Clone(msg)})
	return nil
}

func TestMailerSendFailure(t *testing.T) {
	d := &fakeDialer{}
	m := NewMailer("smtp.example", "587", "u", "p", "from@example", d)
	err := m.Send(EmailInput{
		To:      "you@example.com",
		Subject: "[digits-prod] FAILED deploying server/v1.9.1",
		Body:    "reverted to v1.9.0\nlast error: healthcheck timeout",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.sent) != 1 {
		t.Fatalf("sent=%d", len(d.sent))
	}
	got := d.sent[0]
	if got.from != "from@example" {
		t.Errorf("from=%q", got.from)
	}
	msg := string(got.msg)
	for _, want := range []string{
		"From: from@example",
		"To: you@example.com",
		"Subject: [digits-prod] FAILED deploying server/v1.9.1",
		"reverted to v1.9.0",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("msg missing %q; got:\n%s", want, msg)
		}
	}
}

func TestMailerDialerError(t *testing.T) {
	d := &fakeDialer{fail: errors.New("connection refused")}
	m := NewMailer("smtp.example", "587", "u", "p", "from@example", d)
	err := m.Send(EmailInput{To: "you@example.com", Subject: "x", Body: "y"})
	if err == nil {
		t.Fatal("expected error")
	}
}

package mail

import (
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	from := &mail.Address{Name: "Track-a-doodle", Address: "noreply@example.com"}
	to := &mail.Address{Address: "åsa@example.com"}
	date := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	data, err := Format(from, to, "Bekräfta ditt konto", "Hej!\nLänk: https://example.com/verify#token=abc", date)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("not a parseable message: %v\n%s", err, data)
	}
	if got := msg.Header.Get("Subject"); !strings.HasPrefix(got, "=?utf-8?q?") {
		t.Errorf("subject not encoded: %q", got)
	}
	if got := msg.Header.Get("Date"); got != "Sun, 08 Nov 2026 12:00:00 +0000" {
		t.Errorf("Date = %q", got)
	}
	if got := msg.Header.Get("Message-ID"); !strings.HasSuffix(got, "@example.com>") {
		t.Errorf("Message-ID = %q", got)
	}
	if !strings.Contains(string(data), "\r\n\r\nHej!\r\n") {
		t.Errorf("body lines don't end in CRLF:\n%q", data)
	}
}

func TestFormatRejectsHeaderInjection(t *testing.T) {
	from := &mail.Address{Address: "a@example.com"}
	to := &mail.Address{Address: "b@example.com"}
	if _, err := Format(from, to, "hi\r\nBcc: c@example.com", "", time.Now()); err == nil {
		t.Error("a subject with CRLF was accepted")
	}
}

func TestNewSMTPSenderValidates(t *testing.T) {
	ok := SMTPConfig{Addr: "mailpit:1025", TLS: TLSNone, From: "Track-a-doodle <noreply@localhost>"}
	if _, err := NewSMTPSender(ok); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, cfg := range map[string]SMTPConfig{
		"no port":  {Addr: "mailpit", TLS: TLSNone, From: ok.From},
		"bad from": {Addr: ok.Addr, TLS: TLSNone, From: "not an address"},
		"bad tls":  {Addr: ok.Addr, TLS: "ssl", From: ok.From},
	} {
		if _, err := NewSMTPSender(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

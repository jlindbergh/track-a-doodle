// Package mail sends plain-text emails.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Message is a plain-text email to one recipient.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender sends emails.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// TLSMode is how the SMTP connection is secured.
type TLSMode string

const (
	// TLSNone sends in the clear. Only for a local catcher such as Mailpit.
	TLSNone TLSMode = "none"
	// TLSStartTLS upgrades a plain connection (usually port 587). The server
	// must support it.
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit connects with TLS from the start (usually port 465).
	TLSImplicit TLSMode = "tls"
)

// ParseTLSMode parses the SMTP_TLS setting.
func ParseTLSMode(s string) (TLSMode, error) {
	switch m := TLSMode(s); m {
	case TLSNone, TLSStartTLS, TLSImplicit:
		return m, nil
	}
	return "", fmt.Errorf("unknown SMTP TLS mode %q: want none, starttls or tls", s)
}

// SMTPConfig configures an SMTPSender.
type SMTPConfig struct {
	// Addr is host:port.
	Addr string
	TLS  TLSMode
	// Username and Password are optional; with them, PLAIN auth is used.
	Username string
	Password string
	// From is the sender, e.g. "Track-a-doodle <noreply@example.com>".
	From string
}

// SMTPSender sends emails through an SMTP server, one connection per email.
type SMTPSender struct {
	cfg  SMTPConfig
	from *mail.Address
	host string
}

// sendTimeout bounds one email when the context has no earlier deadline.
const sendTimeout = 30 * time.Second

// NewSMTPSender checks cfg and returns a sender.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("SMTP address %q: %w", cfg.Addr, err)
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("sender address %q: %w", cfg.From, err)
	}
	if _, err := ParseTLSMode(string(cfg.TLS)); err != nil {
		return nil, err
	}
	return &SMTPSender{cfg: cfg, from: from, host: host}, nil
}

// Send sends m.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("recipient: %w", err)
	}
	data, err := Format(s.from, to, m.Subject, m.Body, time.Now())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	conn, err := s.dial(ctx)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer conn.Close()
	// net/smtp has no context support; the deadline bounds the whole exchange.
	if dl, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(dl); err != nil {
			return err
		}
	}

	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	defer c.Close()
	if s.cfg.TLS == TLSStartTLS {
		if err := c.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if s.cfg.Username != "" {
		// PlainAuth refuses to send credentials over an unencrypted
		// connection to anything but localhost.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.host)); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("SMTP write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP end of data: %w", err)
	}
	return c.Quit()
}

func (s *SMTPSender) dial(ctx context.Context) (net.Conn, error) {
	if s.cfg.TLS == TLSImplicit {
		d := &tls.Dialer{Config: &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}}
		return d.DialContext(ctx, "tcp", s.cfg.Addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", s.cfg.Addr)
}

// Format returns the message as sent over SMTP: headers and a
// quoted-printable UTF-8 body, with CRLF line endings.
func Format(from, to *mail.Address, subject, body string, date time.Time) ([]byte, error) {
	if strings.ContainsAny(subject, "\r\n") {
		return nil, errors.New("subject contains a line break")
	}
	domain := "localhost"
	if at := strings.LastIndex(from.Address, "@"); at >= 0 {
		domain = from.Address[at+1:]
	}

	var b bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", date.Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<%s@%s>", rand.Text(), domain))
	header("MIME-Version", "1.0")
	header("Content-Type", `text/plain; charset="utf-8"`)
	header("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if _, err := qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Recorder is a Sender that keeps messages instead of sending them, for tests.
type Recorder struct {
	mu   sync.Mutex
	sent []Message
	// Err, if set, is returned by Send and nothing is recorded.
	Err error
}

// Send records m.
func (r *Recorder) Send(_ context.Context, m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.sent = append(r.sent, m)
	return nil
}

// Sent returns the recorded messages.
func (r *Recorder) Sent() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.sent...)
}

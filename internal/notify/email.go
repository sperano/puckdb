// Package notify sends plain-text notification emails over SMTP submission.
package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const (
	charset       = "utf-8"
	headerNewline = "\r\n"
	// messageIDRandomBytes is the random part of a Message-ID.
	messageIDRandomBytes = 16
)

// SMTPConfig is where and as whom emails are submitted. With an empty
// Username the email is sent without authentication (an internal relay).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	// Timeout bounds the whole SMTP conversation.
	Timeout time.Duration
}

// Email is one plain-text message.
type Email struct {
	From    string
	To      []string
	Subject string
	Body    string
	Date    time.Time
}

// ParseRecipients splits a comma-separated recipient list, dropping blanks.
func ParseRecipients(list string) []string {
	var to []string
	for addr := range strings.SplitSeq(list, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			to = append(to, addr)
		}
	}
	return to
}

// Validate reports a configuration that cannot send: no host, port or
// timeout.
func (c SMTPConfig) Validate() error {
	if c.Host == "" {
		return errors.New("smtp host is empty")
	}
	if c.Port <= 0 {
		return fmt.Errorf("smtp port %d is not positive", c.Port)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("smtp timeout %s is not positive", c.Timeout)
	}
	return nil
}

// envelope holds the parsed sender and recipients of an email.
type envelope struct {
	from *mail.Address
	to   []*mail.Address
}

func (e Email) envelope() (envelope, error) {
	from, err := mail.ParseAddress(e.From)
	if err != nil {
		return envelope{}, fmt.Errorf("sender %q: %w", e.From, err)
	}
	if len(e.To) == 0 {
		return envelope{}, errors.New("no recipient")
	}
	env := envelope{from: from, to: make([]*mail.Address, 0, len(e.To))}
	for _, addr := range e.To {
		parsed, err := mail.ParseAddress(addr)
		if err != nil {
			return envelope{}, fmt.Errorf("recipient %q: %w", addr, err)
		}
		env.to = append(env.to, parsed)
	}
	return env, nil
}

// Validate reports an invalid sender or recipient, or no recipient.
func (e Email) Validate() error {
	_, err := e.envelope()
	return err
}

// Message renders the email as an RFC 5322 message. It rejects invalid
// addresses and a subject with line breaks (header injection).
func (e Email) Message() ([]byte, error) {
	env, err := e.envelope()
	if err != nil {
		return nil, err
	}
	return e.render(env)
}

func (e Email) render(env envelope) ([]byte, error) {
	if strings.ContainsAny(e.Subject, "\r\n") {
		return nil, errors.New("subject contains a line break")
	}
	to := make([]string, 0, len(env.to))
	for _, addr := range env.to {
		to = append(to, addr.String())
	}

	messageID, err := newMessageID(env.from, e.Date)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	header := func(name, value string) { b.WriteString(name + ": " + value + headerNewline) }
	header("From", env.from.String())
	header("To", strings.Join(to, ", "))
	header("Subject", mime.QEncoding.Encode(charset, e.Subject))
	header("Date", e.Date.Format(time.RFC1123Z))
	header("Message-ID", messageID)
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset="+charset)
	header("Content-Transfer-Encoding", "8bit")
	b.WriteString(headerNewline)
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(e.Body, "\r\n", "\n"), "\n", headerNewline))
	return b.Bytes(), nil
}

// newMessageID returns a unique Message-ID in the sender's domain. Some
// receivers reject mail without one, and a relay does not always add it.
func newMessageID(from *mail.Address, date time.Time) (string, error) {
	random := make([]byte, messageIDRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("message id: %w", err)
	}
	_, domain, _ := strings.Cut(from.Address, "@")
	return fmt.Sprintf("<%d.%s@%s>", date.UnixNano(), hex.EncodeToString(random), domain), nil
}

// Send submits the email. STARTTLS is used whenever the server offers it;
// authentication is refused over an unencrypted connection except to
// localhost (net/smtp.PlainAuth).
func Send(ctx context.Context, cfg SMTPConfig, e Email) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	env, err := e.envelope()
	if err != nil {
		return err
	}
	msg, err := e.render(env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connect to smtp server %s: %w", addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set smtp deadline: %w", err)
		}
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp greeting from %s: %w", addr, err)
	}
	defer client.Close()
	if err := submit(client, cfg, env, msg); err != nil {
		return fmt.Errorf("send email via %s: %w", addr, err)
	}
	return nil
}

// submit runs the SMTP conversation on a connected client.
func submit(client *smtp.Client, cfg SMTPConfig, env envelope, msg []byte) error {
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := client.Mail(env.from.Address); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, rcpt := range env.to {
		if err := client.Rcpt(rcpt.Address); err != nil {
			return fmt.Errorf("rcpt to %s: %w", rcpt.Address, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("end message: %w", err)
	}
	return client.Quit()
}

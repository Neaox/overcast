package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// dialTimeout is the maximum time allowed for a TCP handshake to the SMTP
// server. 5 seconds is generous for localhost (should complete in <1ms) but
// tight enough to prevent resource-leaking hangs when the server is
// overloaded or has stopped accepting.
const dialTimeout = 5 * time.Second

// smtpTimeout is the per-command deadline for the SMTP session. Each
// command (EHLO, MAIL, RCPT, DATA, QUIT) must complete within this window.
// 30 seconds matches the mock server's per-command deadline.
const smtpTimeout = 30 * time.Second

// Mailer is the interface used by SNS, SES, and Cognito handlers to send
// email notifications. Both the built-in mock server and external SMTP
// relays are accessed through this interface — callers never need to know
// which backend is in use.
//
// Every method accepts a context. When the context is cancelled (e.g. the
// HTTP client disconnects), implementations must return promptly rather
// than leaking a goroutine and an ephemeral port.
type Mailer interface {
	// Send delivers an email. from is the envelope sender; to is the list of
	// recipient addresses; subject and body are the message content;
	// html is an optional HTML alternative (empty string = plain text only).
	Send(ctx context.Context, from string, to []string, subject, body, html string) error

	// SendRaw delivers an already-assembled RFC 2822 MIME message. The msg
	// bytes are passed verbatim to the SMTP DATA command. Use this for
	// SES SendRawEmail where the caller supplies the full message.
	SendRaw(ctx context.Context, from string, to []string, msg []byte) error
}

// SMSSender is the interface used by SNS and Cognito handlers to send SMS
// notifications in local development. The only implementation is MockSMSSender,
// which captures messages into a MailStore so they are visible in the inbox UI.
type SMSSender interface {
	// SendSMS delivers an SMS message. source identifies the service name
	// (e.g. "sns", "cognito") for display; sender is the originator phone
	// number or sender ID (may be empty); to is the destination phone number;
	// body is the message text. groupID and groupTopic link this message to
	// an SNS fan-out batch; pass "" for standalone messages.
	SendSMS(source, sender, to, body, groupID, groupTopic string) error
}

// MockSMSSender captures outbound SMS messages directly into a MailStore.
// It is used when no real SMS gateway is configured, which is the default.
type MockSMSSender struct {
	store     *MailStore
	OnMessage func(*CapturedMessage) // optional callback invoked after each capture
}

// NewMockSMSSender returns an SMSSender that captures messages into store.
func NewMockSMSSender(store *MailStore) *MockSMSSender {
	return &MockSMSSender{store: store}
}

// SendSMS implements SMSSender by storing the message in the capture store.
func (s *MockSMSSender) SendSMS(source, sender, to, body, groupID, groupTopic string) error {
	m := NewSMSMessage(source, sender, to, body, groupID, groupTopic)
	s.store.Add(m)
	if s.OnMessage != nil {
		s.OnMessage(m)
	}
	return nil
}

// OutboundCapture is a shared capture handle used by SNS to record webhook and
// mobile-push deliveries in the inbox. A single implementation (MockOutboundCapture)
// writes directly into a MailStore; future implementations could forward to real
// endpoints.
type OutboundCapture interface {
	// CaptureWebhook records an SNS http/https subscription delivery.
	// endpoint is the destination URL; body is the full JSON notification payload.
	// groupID and groupTopic link this delivery to an SNS fan-out batch.
	CaptureWebhook(source, endpoint, body, groupID, groupTopic string) error

	// CapturePush records an SNS application (mobile push) subscription delivery.
	// endpoint is the device ARN; body is the notification payload JSON.
	// groupID and groupTopic link this delivery to an SNS fan-out batch.
	CapturePush(source, endpoint, body, groupID, groupTopic string) error
}

// MockOutboundCapture stores webhook and push deliveries in a MailStore.
type MockOutboundCapture struct {
	store     *MailStore
	OnMessage func(*CapturedMessage) // optional callback invoked after each capture
}

// NewMockOutboundCapture returns an OutboundCapture that stores deliveries into store.
func NewMockOutboundCapture(store *MailStore, onMessage func(*CapturedMessage)) *MockOutboundCapture {
	return &MockOutboundCapture{store: store, OnMessage: onMessage}
}

// CaptureWebhook implements OutboundCapture.
func (c *MockOutboundCapture) CaptureWebhook(source, endpoint, body, groupID, groupTopic string) error {
	m := NewWebhookMessage(source, endpoint, body, groupID, groupTopic)
	c.store.Add(m)
	if c.OnMessage != nil {
		c.OnMessage(m)
	}
	return nil
}

// CapturePush implements OutboundCapture.
func (c *MockOutboundCapture) CapturePush(source, endpoint, body, groupID, groupTopic string) error {
	m := NewPushMessage(source, endpoint, body, groupID, groupTopic)
	c.store.Add(m)
	if c.OnMessage != nil {
		c.OnMessage(m)
	}
	return nil
}

// Config holds the parameters needed to connect to an SMTP server.
type Config struct {
	// Host is the SMTP server hostname or IP address.
	Host string

	// Port is the SMTP server port (e.g. 25, 465, 587, 1025).
	Port int

	// Username and Password are used for SMTP AUTH PLAIN. Leave empty to skip AUTH.
	Username string
	Password string

	// TLS controls whether to use implicit TLS (port 465 convention).
	// For STARTTLS on submission ports (587), set TLS=false — the client
	// upgrades automatically when the server advertises STARTTLS.
	TLS bool
}

// Addr returns "host:port".
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// NetMailer sends mail via a standard SMTP server. Unlike Go's smtp.SendMail
// (which uses net.Dial with no timeout), NetMailer uses DialTimeout so a
// stalled or unreachable server does not leak goroutines and ephemeral ports.
type NetMailer struct {
	cfg Config
}

// NewMailer returns a NetMailer configured to send through cfg.
func NewMailer(cfg Config) *NetMailer {
	return &NetMailer{cfg: cfg}
}

// Send implements Mailer.
func (m *NetMailer) Send(ctx context.Context, from string, to []string, subject, body, html string) error {
	msg := BuildMessage(from, to, subject, body, html, nil)

	if m.cfg.TLS {
		return m.sendTLS(ctx, from, to, msg)
	}
	return m.sendPlain(ctx, from, to, msg)
}

// SendRaw implements Mailer.
func (m *NetMailer) SendRaw(ctx context.Context, from string, to []string, msg []byte) error {
	if m.cfg.TLS {
		return m.sendTLS(ctx, from, to, msg)
	}
	return m.sendPlain(ctx, from, to, msg)
}

func (m *NetMailer) sendPlain(ctx context.Context, from string, to []string, msg []byte) error {
	addr := m.cfg.Addr()

	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: dial: %w", err)
	}
	defer conn.Close()

	host, _, _ := net.SplitHostPort(addr)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp: new client: %w", err)
	}
	defer client.Quit() //nolint:errcheck

	// Apply a session-level deadline so individual commands time out.
	deadline := time.Now().Add(smtpTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	if m.cfg.Username != "" {
		auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	for _, r := range to {
		if err := client.Rcpt(r); err != nil {
			return fmt.Errorf("smtp: RCPT TO %q: %w", r, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: DATA close: %w", err)
	}
	return nil
}

func (m *NetMailer) sendTLS(ctx context.Context, from string, to []string, msg []byte) error {
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: dialTimeout}}
	conn, err := dialer.DialContext(ctx, "tcp", m.cfg.Addr())
	if err != nil {
		return fmt.Errorf("smtp: tls dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: new client: %w", err)
	}
	defer client.Quit() //nolint:errcheck

	deadline := time.Now().Add(smtpTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	if m.cfg.Username != "" {
		auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	for _, r := range to {
		if err := client.Rcpt(r); err != nil {
			return fmt.Errorf("smtp: RCPT TO %q: %w", r, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp: write body: %w", err)
	}
	return w.Close()
}

// BuildMessage constructs a minimal RFC 2822 message. When html is non-empty
// a multipart/alternative message is produced; otherwise plain text only.
// extraHeaders is an optional map of additional header fields to inject before
// the body (e.g. X-Overcast-Group-Id for SNS fan-out threading). Pass nil to
// omit extra headers.
func BuildMessage(from string, to []string, subject, body, html string, extraHeaders map[string]string) []byte {
	var sb strings.Builder

	if html == "" {
		// Plain text only.
		sb.WriteString("From: " + from + "\r\n")
		sb.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
		sb.WriteString("Subject: " + subject + "\r\n")
		for k, v := range extraHeaders {
			sb.WriteString(k + ": " + v + "\r\n")
		}
		sb.WriteString("MIME-Version: 1.0\r\n")
		sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		sb.WriteString("\r\n")
		sb.WriteString(body)
		return []byte(sb.String())
	}

	// Multipart/alternative with plain + HTML.
	boundary := "----=_Part_boundary_overcast"
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	for k, v := range extraHeaders {
		sb.WriteString(k + ": " + v + "\r\n")
	}
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + `"` + "\r\n")
	sb.WriteString("\r\n")

	sb.WriteString("--" + boundary + "\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body + "\r\n")

	sb.WriteString("--" + boundary + "\r\n")
	sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(html + "\r\n")

	sb.WriteString("--" + boundary + "--\r\n")
	return []byte(sb.String())
}

package smtp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/overcast-sh/overcast/internal/smtp"
)

// TestUnavailable_everySendFailsWithTheReason: when the capture server could
// not bind, a send is answered with why — and what to change — rather than
// parked waiting for a server that is never coming.
func TestUnavailable_everySendFailsWithTheReason(t *testing.T) {
	// Given: the mock server failed to bind
	reason := errors.New("smtp: mock server is not listening (set OVERCAST_SMTP_PORT to a free port)")
	m := smtp.Unavailable(reason)

	// When: SES, SNS or Cognito try to send
	sendErr := m.Send(context.Background(), "a@example.com", []string{"b@example.com"}, "subject", "body", "")
	rawErr := m.SendRaw(context.Background(), "a@example.com", []string{"b@example.com"}, []byte("raw"))

	// Then: both fail promptly with that reason
	if !errors.Is(sendErr, reason) {
		t.Fatalf("Send error = %v, want %v", sendErr, reason)
	}
	if !errors.Is(rawErr, reason) {
		t.Fatalf("SendRaw error = %v, want %v", rawErr, reason)
	}
}

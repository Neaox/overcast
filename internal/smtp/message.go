// Package smtp provides a minimal SMTP server for capturing outbound emails
// in local development, and a Mailer interface for sending emails that works
// with both the built-in capture server and an external SMTP relay.
//
// It also provides an SMSSender interface for capturing outbound SMS messages
// into the same MailStore so they appear in the inbox alongside emails.
//
// Architecture:
//
//	SNS handler (email/email-json subscriber)
//	    └─ Mailer.Send(...)
//	           │
//	    ┌──────┴──────┐
//	    │             │
//	  NetMailer    NetMailer
//	  → localhost   → external relay
//	  :1025          (host:port + auth)
//	    │
//	    ▼
//	  Server (RFC 5321 TCP listener)
//	    └─ MailStore (ring buffer, queryable via HTTP)
//
//	SNS handler (sms subscriber) / Cognito (SMS codes)
//	    └─ SMSSender.Send(...)
//	           │
//	    MockSMSSender → MailStore (ring buffer, queryable via HTTP)
package smtp

import (
	"time"

	"github.com/google/uuid"
)

// MessageKind distinguishes the transport type of a captured message.
type MessageKind string

const (
	// KindEmail is an SMTP-delivered email message.
	KindEmail MessageKind = "email"
	// KindSMS is an SMS message captured by the mock sender.
	KindSMS MessageKind = "sms"
	// KindWebhook is an SNS http/https subscription delivery captured in the inbox.
	KindWebhook MessageKind = "webhook"
	// KindPush is an SNS application (mobile push) delivery captured in the inbox.
	KindPush MessageKind = "push"
)

// CapturedMessage holds a single message captured by the mock SMTP server or
// mock SMS sender. The Kind field distinguishes email from SMS.
type CapturedMessage struct {
	// ID is a unique identifier assigned at capture time.
	ID string `json:"id"`

	// Kind is the transport type: "email" or "sms".
	Kind MessageKind `json:"kind"`

	// Source is the name of the service that sent the message
	// (e.g. "sns", "ses", "cognito"). Empty when unknown.
	Source string `json:"source,omitempty"`

	// From is the envelope sender address (MAIL FROM for email, or the
	// originator phone number / sender ID for SMS).
	From string `json:"from"`

	// To is the list of recipient addresses (email) or phone numbers (SMS).
	To []string `json:"to"`

	// Subject is the value of the Subject header. Always empty for SMS.
	Subject string `json:"subject,omitempty"`

	// TextBody is the plain-text body.
	TextBody string `json:"textBody"`

	// HTMLBody is the HTML body (text/html part), if present. Always empty for SMS.
	HTMLBody string `json:"htmlBody,omitempty"`

	// ReceivedAt is the UTC timestamp when the message was captured.
	ReceivedAt time.Time `json:"receivedAt"`

	// Raw is the complete RFC 5321 DATA payload, verbatim. Empty for SMS.
	Raw string `json:"raw,omitempty"`

	// GroupID ties all deliveries for a single SNS Publish call together so
	// the inbox UI can show them as a thread. It is the SNS MessageId when
	// set by the SNS fan-out; empty for standalone messages (SES, Cognito).
	GroupID string `json:"groupId,omitempty"`

	// GroupTopic is the short topic name associated with GroupID, shown as
	// the thread title in the inbox list.
	GroupTopic string `json:"groupTopic,omitempty"`
}

// newCapturedMessage parses a raw SMTP DATA payload and returns a CapturedMessage.
// It extracts Subject, and separates plain-text and HTML body parts where
// present. For non-MIME messages the whole body becomes TextBody.
func newCapturedMessage(from string, to []string, raw string) *CapturedMessage {
	subject, textBody, htmlBody := parseDataPayload(raw)
	groupID := parseOvercastHeader(raw, "X-Overcast-Group-Id")
	groupTopic := parseOvercastHeader(raw, "X-Overcast-Group-Topic")
	return &CapturedMessage{
		ID:         uuid.New().String(),
		Kind:       KindEmail,
		From:       from,
		To:         append([]string(nil), to...),
		Subject:    subject,
		TextBody:   textBody,
		HTMLBody:   htmlBody,
		ReceivedAt: time.Now().UTC(),
		Raw:        raw,
		GroupID:    groupID,
		GroupTopic: groupTopic,
	}
}

// NewSMSMessage builds a CapturedMessage for an outbound SMS. source is the
// service name (e.g. "sns", "cognito") and sender is the originator ID or
// phone number ("" is acceptable when unavailable). groupID and groupTopic
// link this message to an SNS fan-out batch; pass "" for standalone messages.
func NewSMSMessage(source, sender, to, body, groupID, groupTopic string) *CapturedMessage {
	return &CapturedMessage{
		ID:         uuid.New().String(),
		Kind:       KindSMS,
		Source:     source,
		From:       sender,
		To:         []string{to},
		TextBody:   body,
		ReceivedAt: time.Now().UTC(),
		GroupID:    groupID,
		GroupTopic: groupTopic,
	}
}

// NewWebhookMessage builds a CapturedMessage for an SNS http/https subscription
// delivery. endpoint is the destination URL; body is the JSON notification payload.
// groupID and groupTopic link this delivery to an SNS fan-out batch.
func NewWebhookMessage(source, endpoint, body, groupID, groupTopic string) *CapturedMessage {
	return &CapturedMessage{
		ID:         uuid.New().String(),
		Kind:       KindWebhook,
		Source:     source,
		From:       source,
		To:         []string{endpoint},
		TextBody:   body,
		ReceivedAt: time.Now().UTC(),
		GroupID:    groupID,
		GroupTopic: groupTopic,
	}
}

// NewPushMessage builds a CapturedMessage for an SNS application (mobile push)
// subscription delivery. endpoint is the device ARN; body is the notification payload.
// groupID and groupTopic link this delivery to an SNS fan-out batch.
func NewPushMessage(source, endpoint, body, groupID, groupTopic string) *CapturedMessage {
	return &CapturedMessage{
		ID:         uuid.New().String(),
		Kind:       KindPush,
		Source:     source,
		From:       source,
		To:         []string{endpoint},
		TextBody:   body,
		ReceivedAt: time.Now().UTC(),
		GroupID:    groupID,
		GroupTopic: groupTopic,
	}
}

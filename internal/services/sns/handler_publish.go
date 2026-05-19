package sns

// handler_publish.go contains the SNS Publish and PublishBatch handlers.
// Messages are delivered asynchronously to all subscribers — fan-out runs in a
// goroutine so the HTTP response is returned before delivery completes, matching
// the behaviour of real SNS.
//
// Wire protocol: AWS Query (form-encoded POST body, XML responses).

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/smtp"
)

// snsNotificationEnvelope is the message body delivered to SQS queues.
// Format matches the real AWS SNS notification envelope so downstream
// consumers that parse SNS notifications work unchanged.
type snsNotificationEnvelope struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Subject          string `json:"Subject,omitempty"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	UnsubscribeURL   string `json:"UnsubscribeURL"`
}

// ---- XML response types ----------------------------------------------------

type xmlPublishResponse struct {
	XMLName          xml.Name                  `xml:"PublishResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlPublishResult          `xml:"PublishResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}
type xmlPublishResult struct {
	MessageId string `xml:"MessageId"`
}

// ---- Handlers --------------------------------------------------------------

// Publish handles SNS Publish. Delivers message to all active SQS subscribers.
func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	topicArn, ok := h.requireForm(w, r, "TopicArn")
	if !ok {
		return
	}
	message, ok := h.requireForm(w, r, "Message")
	if !ok {
		return
	}
	subject := r.FormValue("Subject")

	topic, aerr := h.snsStore.getTopicByARN(r.Context(), topicArn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Parse MessageAttributes from form: MessageAttributes.entry.N.Name / .Value.DataType / .Value.StringValue
	msgAttrs := parseMessageAttributes(r)

	msgID := uuid.New().String()
	envelope := snsNotificationEnvelope{
		Type:             "Notification",
		MessageId:        msgID,
		TopicArn:         topic.ARN,
		Subject:          subject,
		Message:          message,
		Timestamp:        h.clk.Now().UTC().Format(time.RFC3339Nano),
		SignatureVersion: "1",
		Signature:        "EXAMPLE",
		SigningCertURL:   "EXAMPLE",
		// UnsubscribeURL is set per-subscription in fanOut.
	}

	// Fan-out to all subscribers — runs asynchronously after the response is sent.
	subs, aerr := h.snsStore.listSubscriptionsByTopic(r.Context(), topic.Name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Notify the UI that this topic received a publish, before fan-out begins.
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:   events.SNSMessagePublished,
			Time:   h.clk.Now(),
			Source: "sns",
			Payload: events.SNSPublishPayload{
				TopicName: topic.Name,
				MessageID: msgID,
			},
		})
	}

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.fanOut(context.WithoutCancel(r.Context()), topic.Name, msgID, subject, message, envelope, subs, msgAttrs)
	}()

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlPublishResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlPublishResult{MessageId: msgID},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ---- XML response types for PublishBatch -----------------------------------

type xmlPublishBatchResponse struct {
	XMLName          xml.Name                  `xml:"PublishBatchResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlPublishBatchResult     `xml:"PublishBatchResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlPublishBatchResult struct {
	Successful []xmlPublishBatchSuccess `xml:"Successful>member"`
	Failed     []xmlPublishBatchFailed  `xml:"Failed>member"`
}

type xmlPublishBatchSuccess struct {
	Id        string `xml:"Id"`
	MessageId string `xml:"MessageId"`
}

type xmlPublishBatchFailed struct {
	Id      string `xml:"Id"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// PublishBatch handles SNS PublishBatch.
// Publishes up to 10 messages in a single request.
// AWS docs: https://docs.aws.amazon.com/sns/latest/api/API_PublishBatch.html
func (h *Handler) PublishBatch(w http.ResponseWriter, r *http.Request) {
	topicArn, ok := h.requireForm(w, r, "TopicArn")
	if !ok {
		return
	}

	topic, aerr := h.snsStore.getTopicByARN(r.Context(), topicArn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	// Collect subscriber list once for all entries.
	subs, _ := h.snsStore.listSubscriptionsByTopic(r.Context(), topic.Name)

	// Parse member.N entries from the form-encoded body.
	// Form keys: PublishBatchRequestEntries.member.N.{Id,Message,Subject}
	var successful []xmlPublishBatchSuccess
	var failed []xmlPublishBatchFailed

	for n := 1; n <= 10; n++ {
		prefix := fmt.Sprintf("PublishBatchRequestEntries.member.%d.", n)
		entryID := r.FormValue(prefix + "Id")
		if entryID == "" {
			break // no more entries
		}
		message := r.FormValue(prefix + "Message")
		subject := r.FormValue(prefix + "Subject")

		msgID := uuid.New().String()
		envelope := snsNotificationEnvelope{
			Type:             "Notification",
			MessageId:        msgID,
			TopicArn:         topic.ARN,
			Subject:          subject,
			Message:          message,
			Timestamp:        h.clk.Now().UTC().Format(time.RFC3339Nano),
			SignatureVersion: "1",
			Signature:        "EXAMPLE",
			SigningCertURL:   "EXAMPLE",
			// UnsubscribeURL is set per-subscription in fanOut.
		}
		// Verify we can marshal the base envelope before dispatching.
		if _, err := json.Marshal(envelope); err != nil {
			failed = append(failed, xmlPublishBatchFailed{
				Id:      entryID,
				Code:    "InternalError",
				Message: err.Error(),
			})
			continue
		}

		// Deliver to all subscribers — runs asynchronously after the response is sent.
		h.wg.Add(1)
		envCopy := envelope
		go func() {
			defer h.wg.Done()
			h.fanOut(context.WithoutCancel(r.Context()), topic.Name, msgID, subject, message, envCopy, subs, nil)
		}()

		successful = append(successful, xmlPublishBatchSuccess{Id: entryID, MessageId: msgID})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlPublishBatchResponse{
		Xmlns: snsXMLNS,
		Result: xmlPublishBatchResult{
			Successful: successful,
			Failed:     failed,
		},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// unsubscribeURL returns the URL a subscriber can GET to remove their subscription.
func (h *Handler) unsubscribeURL(subARN string) string {
	q := url.Values{
		"Action":          {"Unsubscribe"},
		"SubscriptionArn": {subARN},
	}
	return fmt.Sprintf("%s/?%s", h.cfg.ExternalBaseURL(), q.Encode())
}

// fanOut delivers a single SNS notification to all active subscribers of a topic.
// env is the base notification envelope; UnsubscribeURL is filled per-subscription.
// msgAttrs is the map of message attributes from the Publish call (may be nil).
// It handles sqs, email, email-json, sms, http/https, and application protocols
// and respects FilterPolicy.
func (h *Handler) fanOut(ctx context.Context, topicName, msgID, subject, plainMessage string, env snsNotificationEnvelope, subs []*Subscription, msgAttrs map[string]string) {
	for _, sub := range subs {
		// Apply FilterPolicy if set.
		if fp, ok := sub.Attributes["FilterPolicy"]; ok && fp != "" {
			if !messageMatchesFilterPolicy(fp, msgAttrs) {
				continue
			}
		}
		// Build the per-subscription envelope with the correct UnsubscribeURL.
		subEnv := env
		subEnv.UnsubscribeURL = h.unsubscribeURL(sub.SubscriptionARN)
		jsonBytes, err := json.Marshal(subEnv)
		if err != nil {
			h.log.Error("SNS fan-out: failed to marshal envelope",
				zap.String("subscription", sub.SubscriptionARN), zap.Error(err))
			continue
		}
		jsonBody := string(jsonBytes)
		switch strings.ToLower(sub.Protocol) {
		case "sqs":
			if h.enqueuer == nil || sub.QueueName == "" {
				continue
			}
			if err := h.enqueuer.EnqueueRaw(ctx, sub.QueueName, jsonBody); err != nil {
				h.log.Error("SNS fan-out: failed to deliver to SQS queue",
					zap.String("queue", sub.QueueName), zap.Error(err))
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSMessageDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSNotificationPayload{
						TopicName: topicName,
						QueueName: sub.QueueName,
						MessageID: msgID,
					},
				})
			}

		case "email", "email-json":
			if h.mailer == nil {
				continue
			}
			from := h.cfg.SMTPFrom
			to := []string{sub.Endpoint}
			var emailBody string
			if strings.EqualFold(sub.Protocol, "email-json") {
				// email-json: full SNS envelope as the body.
				emailBody = jsonBody
			} else {
				// email: human-readable plain-text — just the message.
				emailBody = plainMessage
			}
			raw := smtp.BuildMessage(from, to, subject, emailBody, "", map[string]string{
				"X-Overcast-Group-Id":    msgID,
				"X-Overcast-Group-Topic": topicName,
			})
			if err := h.mailer.SendRaw(context.Background(), from, to, raw); err != nil {
				h.log.Error("SNS fan-out: failed to deliver email",
					zap.String("to", sub.Endpoint), zap.Error(err))
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSEmailDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSEmailPayload{
						TopicName: topicName,
						To:        to,
						Subject:   subject,
						MessageID: msgID,
					},
				})
			}
		case "sms":
			if h.smsSender == nil {
				continue
			}
			// SNS sms-protocol endpoint is the destination phone number.
			if err := h.smsSender.SendSMS("sns", h.cfg.SMTPFrom, sub.Endpoint, plainMessage, msgID, topicName); err != nil {
				h.log.Error("SNS fan-out: failed to capture SMS",
					zap.String("to", sub.Endpoint), zap.Error(err))
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSSMSDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSSMSPayload{
						TopicName: topicName,
						To:        sub.Endpoint,
						MessageID: msgID,
					},
				})
			}

		case "http", "https":
			if h.outbound == nil {
				continue
			}
			if err := h.outbound.CaptureWebhook("sns", sub.Endpoint, jsonBody, msgID, topicName); err != nil {
				h.log.Error("SNS fan-out: failed to capture webhook delivery",
					zap.String("endpoint", sub.Endpoint), zap.Error(err))
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSWebhookDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSWebhookPayload{
						TopicName: topicName,
						Endpoint:  sub.Endpoint,
						MessageID: msgID,
					},
				})
			}

		case "application":
			if h.outbound == nil {
				continue
			}
			if err := h.outbound.CapturePush("sns", sub.Endpoint, jsonBody, msgID, topicName); err != nil {
				h.log.Error("SNS fan-out: failed to capture push delivery",
					zap.String("endpoint", sub.Endpoint), zap.Error(err))
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSPushDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSPushPayload{
						TopicName: topicName,
						Endpoint:  sub.Endpoint,
						MessageID: msgID,
					},
				})
			}
		}
	}
}

// setEnqueuer injects an SQS message enqueuer for SNS→SQS delivery.
func (h *Handler) setEnqueuer(eq events.MessageEnqueuer) {
	h.enqueuer = eq
}

// setMailer injects the SMTP mailer for SNS→email delivery.
func (h *Handler) setMailer(m smtp.Mailer) {
	h.mailer = m
}

// setSmsSender injects the SMS sender for SNS→sms delivery captured in the inbox.
func (h *Handler) setSmsSender(ss smtp.SMSSender) {
	h.smsSender = ss
}

// setOutboundCapture injects the outbound capture handle for http/https and
// application (mobile push) subscription deliveries.
func (h *Handler) setOutboundCapture(oc smtp.OutboundCapture) {
	h.outbound = oc
}

// setBus injects the event bus so that deliveries are broadcast for the topology map.
func (h *Handler) setBus(b *events.Bus) {
	h.bus = b
}

// parseMessageAttributes parses MessageAttributes from an SNS Query-protocol form.
// Form encoding: MessageAttributes.entry.N.Name / .Value.DataType / .Value.StringValue
// Returns a map of attribute name → string value (only String and Number types).
func parseMessageAttributes(r *http.Request) map[string]string {
	attrs := make(map[string]string)
	for n := 1; n <= 10; n++ {
		prefix := fmt.Sprintf("MessageAttributes.entry.%d.", n)
		name := r.FormValue(prefix + "Name")
		if name == "" {
			break
		}
		dt := r.FormValue(prefix + "Value.DataType")
		sv := r.FormValue(prefix + "Value.StringValue")
		if strings.HasPrefix(dt, "String") || strings.HasPrefix(dt, "Number") {
			attrs[name] = sv
		}
	}
	return attrs
}

// messageMatchesFilterPolicy checks whether the published message attributes satisfy
// the subscription's filter policy (a JSON object: attrName → [allowedValues…]).
// Returns true when no filter policy is set, or when all policy conditions are met.
//
// AWS simple value matching only: {"attrName": ["val1", "val2"]} — the message
// must have that attribute and its value must be in the allowed list.
func messageMatchesFilterPolicy(filterPolicyJSON string, msgAttrs map[string]string) bool {
	var policy map[string][]string
	if err := json.Unmarshal([]byte(filterPolicyJSON), &policy); err != nil {
		// Unparseable policy — do not filter (permissive).
		return true
	}
	for attr, allowed := range policy {
		val, ok := msgAttrs[attr]
		if !ok {
			return false // required attribute missing
		}
		found := false
		for _, a := range allowed {
			if a == val {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

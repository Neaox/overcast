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

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
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

// snsTimestampFormat is the notification timestamp form real SNS emits:
// RFC3339 with millisecond precision and a literal Z (e.g.
// "2012-04-25T21:49:25.719Z"). The trailing Z is a literal because every
// timestamp is formatted from a UTC time.
const snsTimestampFormat = "2006-01-02T15:04:05.000Z"

// messageAttribute is one SNS message attribute as subscribers see it. The
// field names and casing are AWS's: this is exactly the shape that appears
// under `Sns.MessageAttributes` in the event delivered to a Lambda function.
type messageAttribute struct {
	Type  string `json:"Type"`
	Value string `json:"Value"`
}

// ---- SNS → Lambda event shape ----------------------------------------------
//
// The event delivered to a lambda-protocol subscriber is AWS's SNS event, not
// the notification envelope SQS and HTTP subscribers receive. The two differ in
// more than nesting: the Lambda event spells the two URL fields "SigningCertUrl"
// and "UnsubscribeUrl", where the envelope spells them "SigningCertURL" and
// "UnsubscribeURL". Handlers written against aws-lambda-go's events.SNSEvent
// (or any of the other SDKs' equivalents) depend on that casing, so it is
// reproduced exactly rather than unified with the envelope above.
//
// RawMessageDelivery does not apply here. AWS supports it for SQS, HTTP/S,
// Firehose and platform-application endpoints only; a Lambda subscriber always
// receives the full Records[].Sns event.

type snsLambdaEvent struct {
	Records []snsLambdaEventRecord `json:"Records"`
}

type snsLambdaEventRecord struct {
	EventVersion         string          `json:"EventVersion"`
	EventSubscriptionArn string          `json:"EventSubscriptionArn"`
	EventSource          string          `json:"EventSource"`
	SNS                  snsLambdaEntity `json:"Sns"`
}

type snsLambdaEntity struct {
	Type      string `json:"Type"`
	MessageId string `json:"MessageId"`
	TopicArn  string `json:"TopicArn"`
	// Subject is a pointer so that a message published without one serialises
	// as JSON null, which is what AWS sends.
	Subject           *string                     `json:"Subject"`
	Message           string                      `json:"Message"`
	Timestamp         string                      `json:"Timestamp"`
	SignatureVersion  string                      `json:"SignatureVersion"`
	Signature         string                      `json:"Signature"`
	SigningCertURL    string                      `json:"SigningCertUrl"`
	UnsubscribeURL    string                      `json:"UnsubscribeUrl"`
	MessageAttributes map[string]messageAttribute `json:"MessageAttributes"`
}

// buildLambdaEvent renders the AWS SNS event for one subscription. env is the
// per-subscription notification envelope (UnsubscribeURL already filled in).
func buildLambdaEvent(env snsNotificationEnvelope, subARN string, msgAttrs map[string]messageAttribute) ([]byte, error) {
	var subject *string
	if env.Subject != "" {
		s := env.Subject
		subject = &s
	}
	// AWS always sends the key, as an empty object when there are no attributes.
	attrs := msgAttrs
	if attrs == nil {
		attrs = map[string]messageAttribute{}
	}
	return json.Marshal(snsLambdaEvent{
		Records: []snsLambdaEventRecord{{
			EventVersion:         "1.0",
			EventSubscriptionArn: subARN,
			EventSource:          "aws:sns",
			SNS: snsLambdaEntity{
				Type:              env.Type,
				MessageId:         env.MessageId,
				TopicArn:          env.TopicArn,
				Subject:           subject,
				Message:           env.Message,
				Timestamp:         env.Timestamp,
				SignatureVersion:  env.SignatureVersion,
				Signature:         env.Signature,
				SigningCertURL:    env.SigningCertURL,
				UnsubscribeURL:    env.UnsubscribeURL,
				MessageAttributes: attrs,
			},
		}},
	})
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
		Timestamp:        h.clk.Now().UTC().Format(snsTimestampFormat),
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

	origin := publishOrigin(r.Context())
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.fanOut(context.WithoutCancel(r.Context()), origin, topic.Name, msgID, subject, message, envelope, subs, msgAttrs)
	}()

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlPublishResponse{
		Xmlns:            snsXMLNS,
		Result:           xmlPublishResult{MessageId: msgID},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// PublishToTopic implements events.TopicPublisher: the internal seam another
// service uses to publish a service-originated notification to a topic — S3
// bucket notifications arrive here. It builds the same notification envelope a
// client Publish builds and fans out to every subscriber asynchronously, so
// the caller (the S3 dispatcher goroutine) is not held for delivery.
//
// A non-nil error means the publish was refused (no such topic) and the caller
// still owns the event; per-subscription delivery failures are reported through
// failDelivery exactly as they are for a client Publish.
//
// There is no HTTP request behind this publish, so the origin is empty and the
// per-subscription UnsubscribeURL is minted on the configured base URL. No
// message attributes exist either — matching AWS, where S3 publishes its
// notifications without message attributes, so an attribute-keyed filter
// policy on a subscription does not match.
func (h *Handler) PublishToTopic(ctx context.Context, topicARN, subject, message string) error {
	topic, aerr := h.snsStore.getTopicByARN(ctx, topicARN)
	if aerr != nil {
		return fmt.Errorf("%s: %s", aerr.Code, aerr.Message)
	}
	subs, aerr := h.snsStore.listSubscriptionsByTopic(ctx, topic.Name)
	if aerr != nil {
		return fmt.Errorf("%s: %s", aerr.Code, aerr.Message)
	}

	msgID := uuid.New().String()
	envelope := snsNotificationEnvelope{
		Type:             "Notification",
		MessageId:        msgID,
		TopicArn:         topic.ARN,
		Subject:          subject,
		Message:          message,
		Timestamp:        h.clk.Now().UTC().Format(snsTimestampFormat),
		SignatureVersion: "1",
		Signature:        "EXAMPLE",
		SigningCertURL:   "EXAMPLE",
		// UnsubscribeURL is set per-subscription in fanOut.
	}

	// Notify the UI that this topic received a publish, before fan-out begins —
	// the same event a client Publish raises.
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
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
		h.fanOut(context.WithoutCancel(ctx), "", topic.Name, msgID, subject, message, envelope, subs, nil)
	}()
	return nil
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
	origin := publishOrigin(r.Context())

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
			Timestamp:        h.clk.Now().UTC().Format(snsTimestampFormat),
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
			h.fanOut(context.WithoutCancel(r.Context()), origin, topic.Name, msgID, subject, message, envCopy, subs, nil)
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

// unsubscribeURL returns the URL a subscriber can GET to remove their
// subscription, on the origin the publisher reached Overcast at.
//
// origin is the publishing caller's origin, captured by the Publish handler and
// carried into fan-out. It cannot be read here: delivery runs on a goroutine
// that outlives the request, and by design there may be no request at all
// behind it. Empty origin is that case — an internally generated notification —
// and the minting helper then falls back to the configured base, the only
// answer available with nobody to ask. See
// docs/plans/client-facing-url-minting.md.
func (h *Handler) unsubscribeURL(origin, subARN string) string {
	q := url.Values{
		"Action":          {"Unsubscribe"},
		"SubscriptionArn": {subARN},
	}
	return fmt.Sprintf("%s/?%s", serviceutil.ClientBaseURLFromOrigin(h.cfg, origin), q.Encode())
}

// publishOrigin returns the origin to mint this publish's notification links
// on: the one the middleware stamped for the calling request, or "" when the
// publish has no dialable HTTP caller behind it (a scheduler firing, internal
// dispatch, a request that arrived on a real AWS hostname).
//
// Read it in the handler, while the request is still in scope, and pass the
// result into fan-out. Reading it during delivery would work only for as long
// as every publish path happens to hand fan-out a context descended from its
// request, which is not a property the compiler checks.
func publishOrigin(ctx context.Context) string {
	return middleware.ClientEndpointFromContext(ctx)
}

// fanOut delivers a single SNS notification to all active subscribers of a topic.
// env is the base notification envelope; UnsubscribeURL is filled per-subscription.
// origin is the publishing caller's origin (see publishOrigin), carried in
// because fan-out outlives the request that started it.
// msgAttrs is the map of message attributes from the Publish call (may be nil).
// It handles sqs, lambda, email, email-json, sms, http/https, and application
// protocols and respects FilterPolicy. A protocol with no delivery
// implementation is reported through failDelivery rather than dropped.
func (h *Handler) fanOut(ctx context.Context, origin, topicName, msgID, subject, plainMessage string, env snsNotificationEnvelope, subs []*Subscription, msgAttrs map[string]messageAttribute) {
	log := h.log.WithRecorder(ctx)
	// Filter-policy matching works on plain string values. Derive them at most
	// once per publish rather than once per subscription, and only when some
	// subscription actually carries a policy.
	var attributeFilterValues map[string]string
	var bodyFilterValues map[string]string
	for _, sub := range subs {
		// Apply FilterPolicy if set.
		if fp, ok := sub.Attributes["FilterPolicy"]; ok && fp != "" {
			filterValues := attributeFilterValues
			if strings.EqualFold(sub.Attributes["FilterPolicyScope"], "MessageBody") {
				if bodyFilterValues == nil {
					bodyFilterValues = filterPolicyValuesFromBody(plainMessage)
				}
				filterValues = bodyFilterValues
			} else if attributeFilterValues == nil {
				attributeFilterValues = filterPolicyValues(msgAttrs)
				filterValues = attributeFilterValues
			}
			if !messageMatchesFilterPolicy(fp, filterValues) {
				continue
			}
		}
		// Build the per-subscription envelope with the correct UnsubscribeURL.
		subEnv := env
		subEnv.UnsubscribeURL = h.unsubscribeURL(origin, sub.SubscriptionARN)
		jsonBytes, err := json.Marshal(subEnv)
		if err != nil {
			log.Error("SNS fan-out: failed to marshal envelope",
				zap.String("subscription", sub.SubscriptionARN), zap.Error(err))
			continue
		}
		jsonBody := string(jsonBytes)
		deliveryBody := jsonBody
		if strings.EqualFold(sub.Attributes["RawMessageDelivery"], "true") {
			deliveryBody = plainMessage
		}
		d := delivery{
			sub:       sub,
			envelope:  subEnv,
			jsonBody:  jsonBody,
			msgID:     msgID,
			topicName: topicName,
		}
		switch strings.ToLower(sub.Protocol) {
		case "sqs":
			if h.enqueuer == nil || sub.QueueName == "" {
				continue
			}
			if err := h.enqueuer.EnqueueRaw(ctx, sub.QueueName, deliveryBody); err != nil {
				h.failDelivery(ctx, d, "SQS enqueue failed: "+err.Error())
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSMessageDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSNotificationPayload{
						TopicName:       topicName,
						QueueName:       sub.QueueName,
						MessageID:       msgID,
						SubscriptionARN: sub.SubscriptionARN,
					},
				})
			}

		case "lambda":
			h.deliverToLambda(ctx, d, msgAttrs)

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
				h.failDelivery(ctx, d, "email delivery failed: "+err.Error())
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSEmailDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSEmailPayload{
						TopicName:       topicName,
						To:              to,
						Subject:         subject,
						MessageID:       msgID,
						SubscriptionARN: sub.SubscriptionARN,
					},
				})
			}
		case "sms":
			if h.smsSender == nil {
				continue
			}
			// SNS sms-protocol endpoint is the destination phone number.
			if err := h.smsSender.SendSMS("sns", h.cfg.SMTPFrom, sub.Endpoint, plainMessage, msgID, topicName); err != nil {
				h.failDelivery(ctx, d, "SMS capture failed: "+err.Error())
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSSMSDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSSMSPayload{
						TopicName:       topicName,
						To:              sub.Endpoint,
						MessageID:       msgID,
						SubscriptionARN: sub.SubscriptionARN,
					},
				})
			}

		case "http", "https":
			if h.outbound == nil {
				continue
			}
			if err := h.outbound.CaptureWebhook("sns", sub.Endpoint, deliveryBody, msgID, topicName); err != nil {
				h.failDelivery(ctx, d, "webhook capture failed: "+err.Error())
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSWebhookDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSWebhookPayload{
						TopicName:       topicName,
						Endpoint:        sub.Endpoint,
						MessageID:       msgID,
						SubscriptionARN: sub.SubscriptionARN,
					},
				})
			}

		case "application":
			if h.outbound == nil {
				continue
			}
			if err := h.outbound.CapturePush("sns", sub.Endpoint, jsonBody, msgID, topicName); err != nil {
				h.failDelivery(ctx, d, "push capture failed: "+err.Error())
				continue
			}
			if h.bus != nil {
				h.bus.Publish(ctx, events.Event{
					Type:   events.SNSPushDelivered,
					Time:   h.clk.Now(),
					Source: "sns",
					Payload: events.SNSPushPayload{
						TopicName:       topicName,
						Endpoint:        sub.Endpoint,
						MessageID:       msgID,
						SubscriptionARN: sub.SubscriptionARN,
					},
				})
			}

		default:
			// Every protocol Subscribe accepts has a case above. Anything that
			// reaches here is a subscription this build cannot deliver to, and
			// it must say so — a notification that disappears without a trace is
			// the failure mode the fidelity rule exists to prevent.
			h.failDelivery(ctx, d, "no delivery implementation for protocol "+sub.Protocol)
		}
	}
}

// delivery is everything one subscription's delivery attempt — and any report
// of its failure — needs. Assembled once per subscription in fanOut so the
// delivery helpers do not each take the same five arguments.
type delivery struct {
	sub *Subscription
	// envelope is the notification for this subscription, with its own
	// UnsubscribeURL already filled in.
	envelope snsNotificationEnvelope
	// jsonBody is the marshalled envelope: what SQS/HTTP subscribers receive,
	// and what goes to the dead-letter queue if delivery fails.
	jsonBody  string
	msgID     string
	topicName string
}

// deliverToLambda hands one notification to a lambda-protocol subscriber.
//
// The invoke runs inline. InvokeEvent is an accept, not an execution: it
// validates the function and queues the event on Lambda's own async machinery,
// which is where the cold start is paid and where the shutdown drain waits for
// it. This used to spawn a goroutine because the call ran the function to
// completion on whichever goroutine made it; it no longer does, and spawning
// one now would only add a hop — and would move the accept off the fan-out
// WaitGroup, so a publish could return before its own subscriptions had been
// accepted.
func (h *Handler) deliverToLambda(ctx context.Context, d delivery, msgAttrs map[string]messageAttribute) {
	if h.invoker == nil {
		h.failDelivery(ctx, d, "Lambda delivery is not available on this server")
		return
	}
	if d.sub.Endpoint == "" {
		h.failDelivery(ctx, d, "subscription has no function ARN")
		return
	}
	payload, err := buildLambdaEvent(d.envelope, d.sub.SubscriptionARN, msgAttrs)
	if err != nil {
		h.failDelivery(ctx, d, "building the Lambda event failed: "+err.Error())
		return
	}

	// A returned error means Lambda never took the event, so it is still SNS's
	// to dead-letter. A throttle is not one of those — Lambda retries it
	// internally, exactly as it does for an HTTP Event invoke.
	if err := h.invoker.InvokeEvent(ctx, d.sub.Endpoint, payload); err != nil {
		h.failDelivery(ctx, d, "Lambda invoke failed: "+err.Error())
		return
	}
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:   events.SNSLambdaDelivered,
			Time:   h.clk.Now(),
			Source: "sns",
			Payload: events.SNSLambdaPayload{
				TopicName:       d.topicName,
				FunctionName:    functionNameFromARN(d.sub.Endpoint),
				MessageID:       d.msgID,
				SubscriptionARN: d.sub.SubscriptionARN,
			},
		})
	}
}

// failDelivery records a notification that did not reach its subscriber. It
// logs the failure, redirects the message to the subscription's dead-letter
// queue when its RedrivePolicy names one (as real SNS does), and publishes the
// failure on the event bus so the web UI can show it against the subscription.
//
// Without a RedrivePolicy the message is genuinely lost — that is AWS's
// behaviour too — but it is lost loudly.
func (h *Handler) failDelivery(ctx context.Context, d delivery, reason string) {
	log := h.log.WithRecorder(ctx)
	dlq := deadLetterQueueName(d.sub.Attributes)
	if dlq != "" && h.enqueuer != nil {
		if err := h.enqueuer.EnqueueRaw(ctx, dlq, d.jsonBody); err != nil {
			log.Error("SNS fan-out: failed to dead-letter an undelivered notification",
				zap.String("subscription", d.sub.SubscriptionARN),
				zap.String("dead_letter_queue", dlq),
				zap.Error(err))
			dlq = ""
		}
	} else {
		dlq = ""
	}

	log.Error("SNS fan-out: delivery failed",
		zap.String("subscription", d.sub.SubscriptionARN),
		zap.String("protocol", d.sub.Protocol),
		zap.String("endpoint", d.sub.Endpoint),
		zap.String("message_id", d.msgID),
		zap.String("reason", reason),
		zap.String("dead_letter_queue", dlq))

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:   events.SNSDeliveryFailed,
			Time:   h.clk.Now(),
			Source: "sns",
			Payload: events.SNSDeliveryFailurePayload{
				TopicName:       d.topicName,
				SubscriptionARN: d.sub.SubscriptionARN,
				Protocol:        d.sub.Protocol,
				Endpoint:        d.sub.Endpoint,
				MessageID:       d.msgID,
				Reason:          reason,
				DeadLetterQueue: dlq,
			},
		})
	}
}

// deadLetterQueueName returns the SQS queue named by a subscription's
// RedrivePolicy attribute, or "" when there is none. AWS's shape is
// {"deadLetterTargetArn":"arn:aws:sqs:…"}.
func deadLetterQueueName(attrs map[string]string) string {
	raw, ok := attrs["RedrivePolicy"]
	if !ok || raw == "" {
		return ""
	}
	var policy struct {
		DeadLetterTargetARN string `json:"deadLetterTargetArn"`
	}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return ""
	}
	return queueNameFromARN(policy.DeadLetterTargetARN)
}

// functionNameFromARN extracts the function name from a Lambda ARN, tolerating
// a bare name. ARN format: arn:aws:lambda:<region>:<account>:function:<name>.
func functionNameFromARN(arn string) string {
	if !strings.HasPrefix(arn, "arn:") {
		return arn
	}
	parts := strings.Split(arn, ":")
	if len(parts) >= 7 && parts[5] == "function" {
		return parts[6]
	}
	return arn
}

// setEnqueuer injects an SQS message enqueuer for SNS→SQS delivery, and for
// moving undeliverable notifications to a subscription's dead-letter queue.
func (h *Handler) setEnqueuer(eq events.MessageEnqueuer) {
	h.enqueuer = eq
}

// setLambdaInvoker injects the Lambda invoker for SNS→Lambda delivery.
func (h *Handler) setLambdaInvoker(inv events.FunctionEventInvoker) {
	h.invoker = inv
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
// Form encoding: MessageAttributes.entry.N.Name / .Value.DataType /
// .Value.StringValue (or .Value.BinaryValue for Binary types).
//
// All declared types are kept, including Binary, because subscribers see them:
// the Lambda event's MessageAttributes map carries every attribute AWS was
// given. Filter-policy matching narrows this down separately — see
// filterPolicyValues.
func parseMessageAttributes(r *http.Request) map[string]messageAttribute {
	attrs := make(map[string]messageAttribute)
	for n := 1; n <= 10; n++ {
		prefix := fmt.Sprintf("MessageAttributes.entry.%d.", n)
		name := r.FormValue(prefix + "Name")
		if name == "" {
			break
		}
		dt := r.FormValue(prefix + "Value.DataType")
		value := r.FormValue(prefix + "Value.StringValue")
		if strings.HasPrefix(dt, "Binary") {
			value = r.FormValue(prefix + "Value.BinaryValue")
		}
		attrs[name] = messageAttribute{Type: dt, Value: value}
	}
	return attrs
}

// filterPolicyValues reduces parsed message attributes to the name → value map
// subscription filter policies are evaluated against. Only String and Number
// attributes participate, matching AWS: a filter policy cannot match on a
// Binary attribute. The returned map is never nil, so callers can cache it.
func filterPolicyValues(attrs map[string]messageAttribute) map[string]string {
	out := make(map[string]string, len(attrs))
	for name, attr := range attrs {
		if strings.HasPrefix(attr.Type, "String") || strings.HasPrefix(attr.Type, "Number") {
			out[name] = attr.Value
		}
	}
	return out
}

// filterPolicyValuesFromBody extracts the scalar top-level values that the
// currently supported simple filter policy evaluator can compare. SNS applies
// subscriptions with FilterPolicyScope=MessageBody to the published JSON body
// rather than the message attributes.
func filterPolicyValuesFromBody(message string) map[string]string {
	var body map[string]any
	if err := json.Unmarshal([]byte(message), &body); err != nil {
		return nil
	}
	values := make(map[string]string, len(body))
	for key, value := range body {
		switch typed := value.(type) {
		case string:
			values[key] = typed
		case float64, bool:
			values[key] = fmt.Sprint(typed)
		}
	}
	return values
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

// Package sns provides emulation of the Amazon Simple Notification Service.
//
// Supported operations:
//   - CreateTopic, DeleteTopic, ListTopics, GetTopicAttributes, SetTopicAttributes
//   - Subscribe (sqs protocol), Unsubscribe, ListSubscriptionsByTopic, ListSubscriptions
//   - Publish (fan-out to SQS subscribers), PublishBatch
//
// Admin endpoints (for web console):
//
//	GET /_overcast/sns/topics
//	GET /_overcast/sns/topics/{topicName}/subscriptions
package sns

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/smtp"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "sns"

// Service implements router.Service for SNS.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured SNS Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

// InitSQSDelivery wires the SQS enqueuer so that Publish fan-out works.
// Call this after both SNS and SQS services have been constructed.
func (s *Service) InitSQSDelivery(eq events.MessageEnqueuer) {
	s.handler.setEnqueuer(eq)
}

// InitEmailDelivery wires the SMTP mailer so that email/email-json subscribers
// receive notifications. Call this after the router builds the mailer.
func (s *Service) InitEmailDelivery(m smtp.Mailer) {
	s.handler.setMailer(m)
}

// InitSMSDelivery wires the SMS sender so that sms-protocol subscribers
// receive notifications captured in the inbox. Call this after the router
// builds the SMS sender.
func (s *Service) InitSMSDelivery(ss smtp.SMSSender) {
	s.handler.setSmsSender(ss)
}

// InitOutboundCapture wires the outbound capture handler so that http/https
// and application (mobile push) subscribers are recorded in the inbox.
func (s *Service) InitOutboundCapture(oc smtp.OutboundCapture) {
	s.handler.setOutboundCapture(oc)
}

// InitBus wires the event bus so that SNS→SQS deliveries are visible on the topology map.
// Call this after the bus has been constructed.
func (s *Service) InitBus(b *events.Bus) {
	s.handler.setBus(b)
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// Stop satisfies router.Stopper. It waits for all in-flight fan-out goroutines
// to complete, or until ctx is cancelled, whichever comes first.
func (s *Service) Stop(ctx context.Context) {
	s.handler.Stop(ctx)
}

// RegisterRoutes mounts the SNS admin endpoints for the web console.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Get("/_overcast/sns/topics", s.adminListTopics)
	r.Get("/_overcast/sns/topics/{topicName}/subscriptions", s.adminListSubscriptions)
}

// DispatchQuery satisfies router.QueryDispatcher.
// SNS uses the AWS Query protocol: form-encoded POST body with Action field, XML responses.
// The router calls r.ParseForm() before invoking this method.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "SNS does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	s.handler.dispatch(w, r)
}

// OwnsAction satisfies router.QueryActionOwner.
// Returns true for every Action value SNS handles so the router can
// skip this dispatcher for SES (and any future Query-protocol service).
func (s *Service) OwnsAction(action string) bool {
	return s.handler.OwnsAction(action)
}

// adminListTopics returns all topics for the web console.
func (s *Service) adminListTopics(w http.ResponseWriter, r *http.Request) {
	topics, aerr := s.handler.snsStore.listTopics(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	type topicOut struct {
		Name string `json:"name"`
		ARN  string `json:"arn"`
	}
	out := make([]topicOut, 0, len(topics))
	for _, t := range topics {
		out = append(out, topicOut{Name: t.Name, ARN: t.ARN})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"topics": out})
}

// adminListSubscriptions returns subscriptions for a topic (web console).
func (s *Service) adminListSubscriptions(w http.ResponseWriter, r *http.Request) {
	topicName := chi.URLParam(r, "topicName")
	subs, aerr := s.handler.snsStore.listSubscriptionsByTopic(r.Context(), topicName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	type subOut struct {
		SubscriptionARN string `json:"subscriptionArn"`
		Protocol        string `json:"protocol"`
		Endpoint        string `json:"endpoint"`
		TopicARN        string `json:"topicArn"`
	}
	out := make([]subOut, 0, len(subs))
	for _, sub := range subs {
		out = append(out, subOut{
			SubscriptionARN: sub.SubscriptionARN,
			Protocol:        sub.Protocol,
			Endpoint:        sub.Endpoint,
			TopicARN:        sub.TopicARN,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"subscriptions": out})
}

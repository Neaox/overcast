// Package sqs implements the AWS SQS API emulator.
//
// SQS uses a JSON (or form-encoded) API. Operations are identified by the
// X-Amz-Target header: "AmazonSQS.SendMessage", "AmazonSQS.ReceiveMessage", etc.
// All operations share a single endpoint — routing is by target header, not URL.
//
// Supported (P1):  CreateQueue, GetQueueUrl, SendMessage, ReceiveMessage,
//
//	DeleteMessage, GetQueueAttributes
//
// Supported (P2):  SendMessageBatch, DeleteMessageBatch, SetQueueAttributes,
//
//	PurgeQueue, ListQueues
//
// Unsupported:     See docs/services/sqs.md
package sqs

import (
	"context"
	"crypto/md5"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/events"
	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

const serviceName = "sqs"

// Service implements router.Service for SQS.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured SQS Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

func (s *Service) Name() string { return serviceName }

// TargetPrefix returns the X-Amz-Target prefix for SQS dispatch.
func (s *Service) TargetPrefix() string { return "AmazonSQS." }

// Dispatch routes to the correct SQS handler based on X-Amz-Target.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	// Strip the service prefix: "AmazonSQS.SendMessage" → "SendMessage"
	const prefix = "AmazonSQS."
	if len(target) > len(prefix) {
		target = target[len(prefix):]
	}
	if fn, ok := s.handler.ops[target]; ok {
		fn(w, r)
		return
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "InvalidAction",
		Message:    "The action " + target + " is not valid for this web service.",
		HTTPStatus: http.StatusBadRequest,
	})
}

// RegisterRoutes mounts SQS handlers.
// POST / is handled by the router's target dispatcher (shared with DynamoDB, SNS).
// The queue URL route is SQS-specific and registered here.
func (s *Service) RegisterRoutes(r chi.Router) {
	// Queue URL (used for message operations) looks like:
	//   http://localhost:4566/<accountID>/<queueName>
	// The regex constraint [0-9]+ ensures this route only matches numeric account
	// IDs, preventing it from stealing S3 object-level POST routes whose bucket
	// names are never purely numeric.
	r.Post("/{accountID:[0-9]+}/{queueName}", s.Dispatch)
}

// Enqueuer returns an events.MessageEnqueuer backed by this service's store.
// The router passes this to S3 (and future services) for cross-service
// notification delivery without creating import cycles.
func (s *Service) Enqueuer() events.MessageEnqueuer {
	return &sqsEnqueuer{store: s.handler.store, clk: s.handler.clk, accountID: s.cfg.AccountID}
}

// sqsEnqueuer satisfies events.MessageEnqueuer by writing a raw message
// body directly into the SQS store.
type sqsEnqueuer struct {
	store     *sqsStore
	clk       clock.Clock
	accountID string
}

func (e *sqsEnqueuer) EnqueueRaw(ctx context.Context, queueName string, body string) error {
	msgID := uuid.New().String()
	bodyMD5 := fmt.Sprintf("%x", md5.Sum([]byte(body)))
	now := e.clk.Now()

	msg := &Message{
		MessageID:     msgID,
		ReceiptHandle: encodeReceiptHandle(queueName, msgID),
		Body:          body,
		MD5OfBody:     bodyMD5,
		SentTimestamp: now.UnixMilli(),
		VisibleAfter:  now,
		Attributes: map[string]string{
			"SenderId":                         e.accountID,
			"SentTimestamp":                    fmt.Sprintf("%d", now.UnixMilli()),
			"ApproximateReceiveCount":          "0",
			"ApproximateFirstReceiveTimestamp": "0",
		},
	}

	if aerr := e.store.putMessage(ctx, queueName, msg); aerr != nil {
		return fmt.Errorf("sqs enqueue %s: %s", queueName, aerr.Message)
	}
	return nil
}

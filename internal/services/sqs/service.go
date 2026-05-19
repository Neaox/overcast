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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "sqs"

// Service implements router.Service for SQS.
type Service struct {
	cfg        *config.Config
	store      state.Store
	log        *serviceutil.ServiceLogger
	handler    *Handler
	cancelFunc context.CancelFunc // cancels the watchVisibility goroutine
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

// InitBus wires the event bus so that queue lifecycle events appear on the topology map.
// Call this after the bus has been constructed.
// It also starts a background goroutine that watches for in-flight messages
// whose visibility timeout has expired and emits SQSMessageVisible events.
func (s *Service) InitBus(b *events.Bus) {
	s.handler.bus = b
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFunc = cancel
	go s.watchVisibility(ctx, b)
}

// Stop cancels the background watchVisibility goroutine.
// Implements router.Stopper so the router calls it on shutdown.
func (s *Service) Stop(_ context.Context) {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
}

// watchVisibility runs in a background goroutine and emits SQSMessageVisible
// when an in-flight message's visibility timeout expires.
// It tracks which messages are currently in-flight per queue; when a message
// transitions from in-flight to visible it fires the event.
// The goroutine runs until ctx is cancelled (i.e. for the lifetime of the process).
func (s *Service) watchVisibility(ctx context.Context, bus *events.Bus) {
	// inflight tracks message IDs that are currently in-flight, per queue.
	inflight := make(map[string]map[string]bool)

	ticker := s.handler.clk.Ticker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pairs, err := s.handler.store.scanAllQueues(ctx)
			if err != nil {
				continue
			}
			for _, p := range pairs {
				var q Queue
				if err := json.Unmarshal([]byte(p.Value), &q); err != nil {
					continue
				}
				msgPairs, err := s.handler.store.scanAllMessagesForQueue(ctx, p.Key)
				if err != nil {
					continue
				}

				prev := inflight[p.Key]
				if prev == nil {
					prev = make(map[string]bool)
				}
				next := make(map[string]bool)

				for _, mp := range msgPairs {
					var msg Message
					if err := json.Unmarshal([]byte(mp.Value), &msg); err != nil {
						continue
					}
					// Only messages that have been received (not just delayed) qualify.
					if msg.ApproximateReceiveCount == 0 {
						continue
					}
					if !msg.IsVisible(s.handler.clk) {
						// Still in-flight.
						next[msg.MessageID] = true
					} else if prev[msg.MessageID] {
						// Was in-flight last tick, now visible — timeout expired.
						bus.Publish(ctx, events.Event{
							Type:   events.SQSMessageVisible,
							Time:   s.handler.clk.Now(),
							Source: serviceName,
							Payload: events.SQSMessagePayload{
								QueueName: q.Name,
								MessageID: msg.MessageID,
							},
						})
					}
				}
				inflight[p.Key] = next
			}
		}
	}
}

func (s *Service) Name() string { return serviceName }

// TargetPrefix returns the X-Amz-Target prefix for SQS dispatch.
func (s *Service) TargetPrefix() string { return "AmazonSQS." }

// Dispatch routes to the correct SQS handler based on X-Amz-Target.
//
// When the protocol-detection middleware has stashed a codec and operation
// name in the request context AND the
// operation has been migrated to the typed dispatcher, the typed path is
// taken. Otherwise the legacy http.HandlerFunc registry runs unchanged.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	// Typed-dispatch fast path.
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "SQS does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if top, ok := s.handler.typedOp[opName]; ok {
			top.Invoke(w, r, c)
			return
		}
	}

	// Query protocol fallback: the Protocol middleware identifies form-encoded
	// requests as QueryXML but cannot extract the operation name without
	// parsing the body (identifyQuery returns opName="" when Action= is in
	// the body rather than the URL query string). When this request arrived
	// via the queue-URL route (/{accountID}/{queueName}), Dispatch is
	// responsible for finishing the dispatch — delegate to DispatchQuery,
	// which handles form parsing and JSON translation.
	if c, _ := codec.FromContext(r.Context()); c != nil && c.Name() == codec.NameAWSQuery {
		if err := r.ParseForm(); err == nil {
			s.DispatchQuery(w, r)
			return
		}
	}

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
	// GET: non-AWS peek endpoint — read-only, no state changes, all messages visible.
	r.Get("/{accountID:[0-9]+}/{queueName}", s.handler.PeekMessages)
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
	bodyMD5 := md5Hex([]byte(body))
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

// Receiver returns an events.MessageReceiver backed by this service's store.
// The Lambda event source mapping poller uses this to receive and delete
// messages directly without going through the HTTP layer.
func (s *Service) Receiver() events.MessageReceiver {
	return &sqsReceiver{handler: s.handler}
}

// sqsReceiver satisfies events.MessageReceiver for the Lambda ESM SQS poller.
type sqsReceiver struct {
	handler *Handler
}

func (r *sqsReceiver) ReceiveMessages(ctx context.Context, queueName string, maxCount, visibilitySeconds int) ([]events.ReceivedMessage, error) {
	h := r.handler
	allMessages, aerr := h.store.listMessages(ctx, queueName)
	if aerr != nil {
		// Queue not found is non-fatal for the poller.
		return nil, nil //nolint:nilerr
	}

	// Load queue once for DLQ checks.
	q, aerr := h.store.getQueue(ctx, queueName)
	if aerr != nil {
		return nil, nil //nolint:nilerr
	}
	rp, _ := parseRedrivePolicy(q.Attributes)

	var out []events.ReceivedMessage
	for _, msg := range allMessages {
		if len(out) >= maxCount {
			break
		}
		if !msg.IsVisible(h.clk) {
			continue
		}
		newHandle := encodeReceiptHandle(queueName, msg.MessageID)
		msg.ReceiptHandle = newHandle
		msg.VisibleAfter = h.clk.Now().Add(time.Duration(visibilitySeconds) * time.Second)
		msg.ApproximateReceiveCount++
		msg.Attributes["ApproximateReceiveCount"] = strconv.Itoa(msg.ApproximateReceiveCount)
		if msg.ApproximateReceiveCount == 1 {
			msg.Attributes["ApproximateFirstReceiveTimestamp"] = strconv.FormatInt(h.clk.Now().UnixMilli(), 10)
		}

		// DLQ check: mirror the same policy as the HTTP ReceiveMessage handler.
		if rp != nil && msg.ApproximateReceiveCount >= rp.MaxReceiveCount {
			_ = h.moveToDLQ(ctx, queueName, q, rp, msg)
			continue // do not deliver this message to Lambda
		}

		if aerr := h.store.putMessage(ctx, queueName, msg); aerr != nil {
			return nil, fmt.Errorf("sqs receive %s: %s", queueName, aerr.Message)
		}
		out = append(out, events.ReceivedMessage{
			MessageID:     msg.MessageID,
			ReceiptHandle: newHandle,
			Body:          msg.Body,
			Attributes:    msg.Attributes,
			MD5OfBody:     msg.MD5OfBody,
		})
	}
	return out, nil
}

func (r *sqsReceiver) DeleteMessages(ctx context.Context, queueName string, receiptHandles []string) error {
	for _, handle := range receiptHandles {
		_, msgID, err := decodeReceiptHandle(handle)
		if err != nil {
			continue // stale or invalid handle — skip
		}
		_ = r.handler.store.deleteMessage(ctx, queueName, msgID)
	}
	return nil
}

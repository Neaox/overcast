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

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const serviceName = "sqs"

// awsapiService is SQS's key in the generated AWS model corpus.
// serviceutil.MustAWSService validates it at package initialisation, so a
// key the models do not carry fails immediately rather than silently
// answering every unimplemented operation with a 400.
var awsapiService = serviceutil.MustAWSService(serviceName)

// Service implements router.Service for SQS.
type Service struct {
	cfg               *config.Config
	store             state.Store
	log               *serviceutil.ServiceLogger
	handler           *Handler
	cancelFunc        context.CancelFunc // cancels the watchVisibility goroutine
	metricsCancelFunc context.CancelFunc // cancels the gauge-sampler goroutine (metrics_sqs.go)
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

// InitMetrics wires the shared service-metrics recorder
// (docs/plans/service-metrics-platform.md phase 2) so every message
// operation records its AWS/SQS outcome metrics (metrics_sqs.go), and starts
// the periodic gauge sampler that publishes
// ApproximateNumberOfMessagesVisible/NotVisible/Delayed and
// ApproximateAgeOfOldestMessage for every existing queue once a minute — the
// same fact AWS samples whether or not the queue has traffic. Called once
// from router.New, after metrics.NewRecorder; a Service without it (unit
// tests, or OVERCAST_SERVICE_METRICS=disabled) simply never records or
// samples anything, matching Lambda's InitMetrics contract.
func (s *Service) InitMetrics(m metricsRecorder) {
	s.handler.metrics = m
	if m == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.metricsCancelFunc = cancel
	go s.sampleQueueGauges(ctx)
}

// Stop cancels the background watchVisibility and gauge-sampler goroutines.
// Implements router.Stopper so the router calls it on shutdown.
func (s *Service) Stop(_ context.Context) {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	if s.metricsCancelFunc != nil {
		s.metricsCancelFunc()
	}
}

// debugMessagesScanLimit bounds how many rows DebugStateKeys/DebugStateValues
// return in one response — mirrors CloudWatch Logs' debugEventsScanLimit
// (internal/services/cloudwatch/logs/service.go). Message volume is
// unbounded (that's why sqs:messages graduated to a dedicated table — see
// docs/plans/storage-plan.md item 3.10), so dumping every row into a
// synchronous JSON response is unsafe for a busy queue.
const debugMessagesScanLimit = 500

// DebugNamespace returns the virtual raw-state namespace name for SQS
// messages, implementing router.DebugStateProvider. Messages live in the
// dedicated sqs_messages SQL table (or the in-memory equivalent), not the
// generic kv store, so without this they'd be invisible to /_overcast/debug/state and
// exempt from /_overcast/reset — the graduation rule (storage-plan.md
// "Settled decisions") requires this for every dedicated table, mirroring
// DynamoDB's "dynamodb:items" and CloudWatch Logs' "logs:events".
func (s *Service) DebugNamespace() string { return "sqs:messages" }

// DebugStateKeys returns up to debugMessagesScanLimit virtual keys for
// /_overcast/debug/state's top-level listing.
func (s *Service) DebugStateKeys(ctx context.Context) ([]string, error) {
	records, _, err := s.handler.store.backend.debugScan(ctx, debugMessagesScanLimit)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(records))
	for _, r := range records {
		keys = append(keys, debugMessageKey(r))
	}
	return keys, nil
}

// DebugStateValues returns raw message values keyed by
// region/queueName/messageID, capped at debugMessagesScanLimit rows. A
// "_truncated" pseudo-key is added when more rows exist than were returned.
func (s *Service) DebugStateValues(ctx context.Context) (map[string]string, error) {
	records, truncated, err := s.handler.store.backend.debugScan(ctx, debugMessagesScanLimit)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(records)+1)
	for _, r := range records {
		values[debugMessageKey(r)] = r.RawJSON
	}
	if truncated {
		values["_truncated"] = fmt.Sprintf("showing first %d messages only", debugMessagesScanLimit)
	}
	return values, nil
}

// DebugResetState deletes every persisted message, for /_overcast/reset.
func (s *Service) DebugResetState(ctx context.Context) error {
	return s.handler.store.backend.debugDeleteAll(ctx)
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
				region, queueName := serviceutil.SplitRegionKey(p.Key)
				messages, err := s.handler.store.scanAllMessagesForQueue(ctx, region, queueName)
				if err != nil {
					continue
				}

				prev := inflight[p.Key]
				if prev == nil {
					prev = make(map[string]bool)
				}
				next := make(map[string]bool)

				for _, msg := range messages {
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
	// Query-protocol requests (boto2-style form posts, typically to the
	// queue URL /{accountID}/{queueName}) always go through DispatchQuery's
	// form→JSON translation tier, never the typed fast path below: SQS's
	// typed In types are JSON-shaped, and the Query form encoding of SQS
	// parameters (Attribute.N, MessageAttribute.N.… member shapes) is
	// handled by sqsFormToJSON's mapping table, which a generic Query
	// decode cannot replicate. Since the protocol middleware began
	// resolving Action from the form body (wire-protocol plan P1), these
	// requests arrive with a non-empty opName and would otherwise be
	// intercepted by the typed path and mis-decoded.
	if c, _ := codec.FromContext(r.Context()); c != nil && c.Name() == codec.NameAWSQuery {
		if err := r.ParseForm(); err == nil {
			s.DispatchQuery(w, r)
			return
		}
	}

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
		// The X-Amz-Target handlers speak JSON only, so a JSON request may
		// still reach one that has no typed binding — by the operation the
		// context already resolved, not by re-reading the header. Anything
		// else, RPC v2 CBOR today, has no such fallback: it also carries no
		// X-Amz-Target, which is how it used to fall to the JSON 400 at the
		// bottom whatever the operation. Refuse here, in the request's own
		// wire format: 501 for a real SQS operation Overcast has not
		// implemented, InvalidAction for a name AWS does not model (#1645).
		if c.Name() == codec.NameAWSJSON10 || c.Name() == codec.NameAWSJSON11 {
			if fn, ok := s.handler.ops[opName]; ok {
				fn(w, r)
				return
			}
		}
		serviceutil.WriteUnhandledOperation(w, r, c, awsapiService, opName, errInvalidAction(opName))
		return
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
	// A real SQS operation Overcast has not implemented gets an honest 501;
	// InvalidAction stays for a name AWS does not model (#1645).
	serviceutil.WriteUnhandledOperation(w, r, codec.JSON10, awsapiService, target, errInvalidAction(target))
}

// errInvalidAction is SQS's answer for an action naming no modeled operation,
// in every protocol it speaks. Reached via serviceutil.WriteUnhandledOperation
// -> codec.WriteError, which bypasses both of queryerror.go's boundary
// wrappers, so the x-amzn-query-error mapping is applied here directly
// instead (harmless on the Query-XML path in query.go, which ignores it).
func errInvalidAction(action string) *protocol.AWSError {
	return withLegacyQueryError(&protocol.AWSError{
		Code:       "InvalidAction",
		Message:    "The action " + action + " is not valid for this web service.",
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

	// /_aws/sqs/messages, LocalStack's name for the same peek, is registered
	// by the router rather than here (internal/router/aws_compat.go) and reads
	// through PeekQueue below — it belongs to the compatibility layer, not to
	// this service's API, and lives outside /_overcast/ for the reason
	// middleware.AWSCompatPrefix gives.

	// Emulator-specific: the web UI Monitor tab's metrics read-through
	// (docs/plans/service-metrics-platform.md phase 3). Lives under
	// /_overcast/, not the AWS-shaped path above — see
	// docs/plans/non-canonical-url-namespace.md.
	r.Get("/_overcast/sqs/queues/{name}/metrics", s.handler.GetQueueMetrics)
}

// PeekQueue returns every message in queueName without touching any state —
// no receive count, no visibility timeout — the same read GET
// /{accountID}/{queueName} serves. region names the region to look in; ""
// means whatever ctx already carries, falling back to the configured default.
//
// This is the seam the LocalStack-compatible /_aws/sqs/messages alias reads
// through (internal/router/aws_compat.go). The alias takes its region from the
// queue URL it is handed rather than from the request's own headers, which is
// why region is a parameter here rather than something read off ctx alone.
func (s *Service) PeekQueue(ctx context.Context, region, queueName string) ([]PeekedMessage, *protocol.AWSError) {
	if region != "" {
		ctx = middleware.ContextWithRegion(ctx, region)
	}
	return s.handler.peekQueue(ctx, queueName)
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

package sqs

// handler.go contains the Handler struct, operation registry, and shared helpers.
// Implemented handlers are split by concern:
//   handler_queue.go   — CreateQueue, GetQueueURL, GetQueueAttributes,
//                        SetQueueAttributes, DeleteQueue, ListQueues, PurgeQueue
//   handler_message.go — SendMessage, ReceiveMessage, DeleteMessage,
//                        SendMessageBatch, DeleteMessageBatch

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// Handler holds SQS handler dependencies.
type Handler struct {
	cfg     *config.Config
	store   *sqsStore
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	bus     *events.Bus
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation // Smithy-aligned typed registry; see typed_ops.go
	seqNum  atomic.Int64            // FIFO sequence number counter

	// metrics is nil until Service.InitMetrics is called (or when automatic
	// collection is disabled — see config.ServiceMetricsMode). Every call site
	// in metrics_sqs.go is nil-safe, matching Lambda's
	// metrics_lambda.go/handler.go pattern.
	metrics metricsRecorder
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: newSQSStore(store, clk, cfg.Region), log: log, clk: clk}
	h.initOps()
	h.typedOp = h.typedOps()
	return h
}

// initOps registers every known SQS operation to its handler.
// Implemented operations point to their handler method; stubs live in handler_stubs.go.
// Adding a new operation: add an entry here, implement in the appropriate handler_<group>.go, delete from handler_stubs.go.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// P1 — core messaging
		"CreateQueue":        h.CreateQueue,
		"GetQueueUrl":        h.GetQueueURL,
		"SendMessage":        h.SendMessage,
		"ReceiveMessage":     h.ReceiveMessage,
		"DeleteMessage":      h.DeleteMessage,
		"GetQueueAttributes": h.GetQueueAttributes,
		// P2 — queue management
		"SetQueueAttributes": h.SetQueueAttributes,
		"DeleteQueue":        h.DeleteQueue,
		"ListQueues":         h.ListQueues,
		"PurgeQueue":         h.PurgeQueue,
		"SendMessageBatch":   h.SendMessageBatch,
		"DeleteMessageBatch": h.DeleteMessageBatch,
		// TODO(priority:P2): implement ChangeMessageVisibilityBatch
		"ChangeMessageVisibility":      h.ChangeMessageVisibility,
		"ChangeMessageVisibilityBatch": h.ChangeMessageVisibilityBatch,
		// TODO(priority:P3): implement queue permissions
		"AddPermission":    h.AddPermission,
		"RemovePermission": h.RemovePermission,
		// ListDeadLetterSourceQueues implemented in handler_dlq.go
		"ListDeadLetterSourceQueues": h.ListDeadLetterSourceQueues,
		// StartMessageMoveTask implemented in handler_dlq.go
		"StartMessageMoveTask": h.StartMessageMoveTask,
		// TODO(priority:P2): implement queue tagging
		"ListQueueTags": h.ListQueueTags,
		"TagQueue":      h.TagQueue,
		"UntagQueue":    h.UntagQueue,
	}
}

// ---- Shared helpers --------------------------------------------------------

// queueURL builds the SQS queue URL for a given queue name, on the origin the
// calling client reached Overcast on.
// Format matches LocalStack: http://<host>:<port>/<accountID>/<queueName>
//
// The origin is per-request rather than server-wide because AWS SDKs resolve
// the SQS endpoint from the QueueUrl, not from client configuration — see
// internal/middleware/clientendpoint.go. A queue URL is only usable by a caller
// that can dial its origin, and "localhost" is right for a host CLI while a
// sibling Lambda container needs Overcast's container address. Requests with no
// usable origin (internal callers, background work, real AWS hostnames) fall
// back to the configured external origin.
func (h *Handler) queueURL(ctx context.Context, queueName string) string {
	return fmt.Sprintf("%s/%s/%s",
		h.queueURLBase(ctx), h.cfg.AccountID, queueName)
}

// queueURLBase returns the origin queue URLs should carry for this request:
// the caller's own origin, verbatim.
//
// DELIBERATE DIVERGENCE — do not "unify" this onto
// serviceutil.ClientBaseURLFromOrigin, which substitutes a configured
// OVERCAST_HOSTNAME. A wire-response QueueUrl is consumed by the requesting
// client itself: JS/.NET/Java SDKs dial the URL directly, so echoing exactly
// the origin the caller just dialed carries zero resolution assumptions.
// Substituting the configured name adds one for no reachability gain, and
// under the documented OVERCAST_HOSTNAME=localhost misconfiguration it would
// hand a container caller a URL meaning "yourself" — turning a bad config into
// a hard failure. Cross-party values (stack outputs, GetAtt-passed URIs) are
// the ones that need the configured name, and CloudFormation re-mints those
// itself. See docs/plans/client-facing-url-minting.md, "Per-service
// requirements and constraints".
func (h *Handler) queueURLBase(ctx context.Context) string {
	if origin := middleware.ClientEndpointFromContext(ctx); origin != "" {
		return origin
	}
	return h.cfg.ExternalBaseURL()
}

// canonicalQueueURL is the stored form of a queue URL: always the configured
// external origin, so persisted state stays portable across the callers that
// happen to create and read a queue. Wire responses re-render through queueURL.
func (h *Handler) canonicalQueueURL(queueName string) string {
	return fmt.Sprintf("%s/%s/%s",
		h.cfg.ExternalBaseURL(), h.cfg.AccountID, queueName)
}

// queueNameFromURL extracts the queue name from a queue URL or ARN.
// Handles both URL format (http://host/accountID/queueName) and ARN
// format (arn:aws:sqs:region:account:queueName).
func queueNameFromURL(queueURL string) string {
	// ARN format: colon-delimited, at least 6 parts, starts with "arn:"
	if strings.HasPrefix(queueURL, "arn:") {
		parts := strings.SplitN(queueURL, ":", 6)
		if len(parts) == 6 {
			return parts[5]
		}
	}
	return path.Base(strings.TrimRight(queueURL, "/"))
}

// encodeReceiptHandle creates an opaque receipt handle from a queue name,
// message ID, and a per-receive nonce. The result is a standard base64 string
// that looks similar to real AWS receipt handles.
// Format inside: "queueName\nmessageID\nnonce" (newline-separated).
func encodeReceiptHandle(queueName, messageID string) string {
	nonce := uuid.New().String()
	payload := queueName + "\n" + messageID + "\n" + nonce
	return base64.StdEncoding.EncodeToString([]byte(payload))
}

// decodeReceiptHandle extracts the queue name and message ID from a receipt
// handle produced by encodeReceiptHandle. Returns an error if the handle is
// malformed or has the wrong number of fields.
func decodeReceiptHandle(handle string) (queueName, messageID string, err error) {
	b, err := base64.StdEncoding.DecodeString(handle)
	if err != nil {
		return "", "", fmt.Errorf("invalid receipt handle encoding: %w", err)
	}
	parts := strings.SplitN(string(b), "\n", 3)
	if len(parts) != 3 {
		return "", "", fmt.Errorf("invalid receipt handle format: expected 3 parts, got %d", len(parts))
	}
	return parts[0], parts[1], nil
}

// isFifoQueue returns true if the queue has FifoQueue=true in its attributes.
func isFifoQueue(q *Queue) bool {
	return q.Attributes["FifoQueue"] == "true"
}

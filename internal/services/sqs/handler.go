package sqs

// handler.go contains the Handler struct, operation registry, and shared helpers.
// Implemented handlers are split by concern:
//   handler_queue.go   — CreateQueue, GetQueueURL, GetQueueAttributes,
//                        SetQueueAttributes, DeleteQueue, ListQueues, PurgeQueue
//   handler_message.go — SendMessage, ReceiveMessage, DeleteMessage,
//                        SendMessageBatch, DeleteMessageBatch

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
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

// queueURL builds the SQS queue URL for a given queue name.
// Format matches LocalStack: http://<host>:<port>/<accountID>/<queueName>
func (h *Handler) queueURL(queueName string) string {
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

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

	"github.com/google/uuid"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

// Handler holds SQS handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *sqsStore
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	ops   map[string]http.HandlerFunc
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: newSQSStore(store, clk), log: log, clk: clk}
	h.initOps()
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
		// TODO(priority:P3): implement dead-letter queue support
		"ListDeadLetterSourceQueues": h.ListDeadLetterSourceQueues,
	}
}

// ---- Shared helpers --------------------------------------------------------

// queueURL builds the SQS queue URL for a given queue name.
// Format matches LocalStack: http://<host>:<port>/<accountID>/<queueName>
func (h *Handler) queueURL(queueName string) string {
	return fmt.Sprintf("http://localhost:%d/%s/%s",
		h.cfg.Port, h.cfg.AccountID, queueName)
}

// queueNameFromURL extracts the queue name from a queue URL.
func queueNameFromURL(queueURL string) string {
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

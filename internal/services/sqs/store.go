package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/state"
)

const (
	nsQueues   = "sqs:queues"
	nsMessages = "sqs:messages"
)

// Queue represents a stored SQS queue.
type Queue struct {
	Name             string            `json:"name"`
	URL              string            `json:"url"`
	ARN              string            `json:"arn"`
	Attributes       map[string]string `json:"attributes"`
	CreatedTimestamp int64             `json:"created_timestamp"`
	Tags             map[string]string `json:"tags,omitempty"`
}

// Message represents a stored SQS message.
type Message struct {
	MessageID               string                      `json:"message_id"`
	ReceiptHandle           string                      `json:"receipt_handle"`
	Body                    string                      `json:"body"`
	Attributes              map[string]string           `json:"attributes"`
	MessageAttributes       map[string]MessageAttribute `json:"message_attributes,omitempty"`
	MD5OfBody               string                      `json:"md5_of_body"`
	SentTimestamp           int64                       `json:"sent_timestamp"`
	ApproximateReceiveCount int                         `json:"approximate_receive_count"`
	VisibleAfter            time.Time                   `json:"visible_after"`
}

// MessageAttribute is a typed SQS message attribute.
// JSON tags use PascalCase to match the AWS SQS wire format.
type MessageAttribute struct {
	DataType    string `json:"DataType"`
	StringValue string `json:"StringValue,omitempty"`
	BinaryValue []byte `json:"BinaryValue,omitempty"`
}

// IsVisible returns true if the message can currently be received.
// The clock parameter is provided by the enclosing store so that tests can
// advance time without real sleeps.
func (m *Message) IsVisible(clk clock.Clock) bool {
	return !clk.Now().Before(m.VisibleAfter)
}

// sqsStore wraps state.Store with SQS-specific helpers.
type sqsStore struct {
	store state.Store
	clk   clock.Clock
}

func newSQSStore(store state.Store, clk clock.Clock) *sqsStore {
	return &sqsStore{store: store, clk: clk}
}

func (s *sqsStore) getQueue(ctx context.Context, name string) (*Queue, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsQueues, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errQueueNotFound(name)
	}
	var q Queue
	if err := json.Unmarshal([]byte(raw), &q); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &q, nil
}

func (s *sqsStore) putQueue(ctx context.Context, q *Queue) *protocol.AWSError {
	raw, err := json.Marshal(q)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsQueues, q.Name, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sqsStore) deleteQueue(ctx context.Context, name string) *protocol.AWSError {
	// Delete queue metadata.
	if err := s.store.Delete(ctx, nsQueues, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// TODO: delete associated messages — requires listing by queue prefix.
	return nil
}

func (s *sqsStore) listQueues(ctx context.Context, prefix string) ([]*Queue, *protocol.AWSError) {
	keys, err := s.store.List(ctx, nsQueues, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	queues := make([]*Queue, 0, len(keys))
	for _, k := range keys {
		q, aerr := s.getQueue(ctx, k)
		if aerr != nil {
			return nil, aerr
		}
		queues = append(queues, q)
	}
	return queues, nil
}

// messageKey builds a store key for a message.
func messageKey(queueName, messageID string) string {
	return queueName + "/" + messageID
}

func (s *sqsStore) putMessage(ctx context.Context, queueName string, msg *Message) *protocol.AWSError {
	raw, err := json.Marshal(msg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsMessages, messageKey(queueName, msg.MessageID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sqsStore) getMessage(ctx context.Context, queueName, messageID string) (*Message, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsMessages, messageKey(queueName, messageID))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, &protocol.AWSError{Code: "ReceiptHandleIsInvalid", Message: "The receipt handle is invalid.", HTTPStatus: http.StatusBadRequest}
	}
	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &msg, nil
}

func (s *sqsStore) deleteMessage(ctx context.Context, queueName, messageID string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsMessages, messageKey(queueName, messageID)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sqsStore) listMessages(ctx context.Context, queueName string) ([]*Message, *protocol.AWSError) {
	prefix := queueName + "/"
	keys, err := s.store.List(ctx, nsMessages, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	msgs := make([]*Message, 0, len(keys))
	for _, k := range keys {
		// keys include the full "<queueName>/<messageID>" — extract just the ID.
		id := k[len(queueName)+1:]
		msg, aerr := s.getMessage(ctx, queueName, id)
		if aerr != nil {
			return nil, aerr
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// ---- SQS-specific errors ---------------------------------------------------

func errQueueNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "AWS.SimpleQueueService.NonExistentQueue",
		Message:    fmt.Sprintf("The specified queue does not exist: %s", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

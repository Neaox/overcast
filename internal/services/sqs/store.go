package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsQueues   = "sqs:queues"
	nsMessages = "sqs:messages"
	nsDedup    = "sqs:dedup" // FIFO deduplication; key = queueName/dedupId, value = expiry timestamp
	nsPurge    = "sqs:purge" // key = queueName, value = purge-in-progress deadline Unix millis
	nsAttempts = "sqs:receive-attempts"
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
	// FIFO fields — only set for messages in FIFO queues.
	MessageGroupId         string `json:"message_group_id,omitempty"`
	MessageDeduplicationId string `json:"message_deduplication_id,omitempty"`
	SequenceNumber         string `json:"sequence_number,omitempty"`
	// VisibilityVersion is incremented on each ChangeMessageVisibility call
	// so ReceiveRequestAttemptId replay can detect modified messages.
	VisibilityVersion int `json:"visibility_version,omitempty"`
}

type receiveAttempt struct {
	ExpiresAtUnixMilli int64                   `json:"expires_at_unix_milli"`
	Messages           []receiveAttemptMessage `json:"messages"`
}

type receiveAttemptMessage struct {
	MessageID         string `json:"message_id"`
	ReceiptHandle     string `json:"receipt_handle"`
	VisibilityVersion int    `json:"visibility_version,omitempty"`
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
	store         state.Store
	clk           clock.Clock
	defaultRegion string
}

func newSQSStore(store state.Store, clk clock.Clock, defaultRegion string) *sqsStore {
	return &sqsStore{store: store, clk: clk, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *sqsStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

func (s *sqsStore) getQueue(ctx context.Context, name string) (*Queue, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), name)
	raw, found, err := s.store.Get(ctx, nsQueues, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errQueueNotFound(name)
	}
	var q Queue
	if err := json.Unmarshal([]byte(raw), &q); err != nil {
		return nil, errQueueNotFound(name)
	}
	return &q, nil
}

func (s *sqsStore) putQueue(ctx context.Context, q *Queue) *protocol.AWSError {
	raw, err := json.Marshal(q)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), q.Name)
	if err := s.store.Set(ctx, nsQueues, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sqsStore) deleteQueue(ctx context.Context, name string) *protocol.AWSError {
	key := serviceutil.RegionKey(s.region(ctx), name)
	// Delete queue metadata.
	if err := s.store.Delete(ctx, nsQueues, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Delete all messages belonging to this queue.
	return s.deleteMessagesByQueuePrefix(ctx, name)
}

func (s *sqsStore) listQueues(ctx context.Context, prefix string) ([]*Queue, *protocol.AWSError) {
	scanPrefix := serviceutil.RegionKey(s.region(ctx), prefix)
	pairs, err := s.store.Scan(ctx, nsQueues, scanPrefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	queues := make([]*Queue, 0, len(pairs))
	for _, p := range pairs {
		var q Queue
		if err := json.Unmarshal([]byte(p.Value), &q); err != nil {
			continue
		}
		queues = append(queues, &q)
	}
	return queues, nil
}

// messageKey builds a store key for a message.
func messageKey(queueName, messageID string) string {
	return queueName + "/" + messageID
}

func (s *sqsStore) putMessage(ctx context.Context, queueName string, msg *Message) *protocol.AWSError {
	active, aerr := s.purgeActive(ctx, queueName)
	if aerr != nil {
		return aerr
	}
	if active {
		return nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), messageKey(queueName, msg.MessageID))
	if err := s.store.Set(ctx, nsMessages, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sqsStore) getReceiveAttempt(ctx context.Context, queueName, attemptID string) (*receiveAttempt, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), queueName+"/"+attemptID)
	raw, found, err := s.store.Get(ctx, nsAttempts, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var attempt receiveAttempt
	if err := json.Unmarshal([]byte(raw), &attempt); err != nil {
		return nil, nil
	}
	if s.clk.Now().UnixMilli() >= attempt.ExpiresAtUnixMilli {
		_ = s.store.Delete(ctx, nsAttempts, key)
		return nil, nil
	}
	return &attempt, nil
}

func (s *sqsStore) putReceiveAttempt(ctx context.Context, queueName, attemptID string, attempt *receiveAttempt) *protocol.AWSError {
	data, err := json.Marshal(attempt)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), queueName+"/"+attemptID)
	if err := s.store.Set(ctx, nsAttempts, key, string(data)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sqsStore) getMessage(ctx context.Context, queueName, messageID string) (*Message, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), messageKey(queueName, messageID))
	raw, found, err := s.store.Get(ctx, nsMessages, key)
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
	key := serviceutil.RegionKey(s.region(ctx), messageKey(queueName, messageID))
	if err := s.store.Delete(ctx, nsMessages, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sqsStore) deleteMessagesByQueuePrefix(ctx context.Context, queueName string) *protocol.AWSError {
	prefix := serviceutil.RegionKey(s.region(ctx), queueName+"/")
	if deleter, ok := s.store.(state.PrefixDeleter); ok {
		if err := deleter.DeletePrefix(ctx, nsMessages, prefix); err != nil {
			return protocol.Wrap(protocol.ErrInternalError, err)
		}
		return nil
	}
	keys, err := s.store.List(ctx, nsMessages, prefix)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	for _, key := range keys {
		if err := s.store.Delete(ctx, nsMessages, key); err != nil {
			return protocol.Wrap(protocol.ErrInternalError, err)
		}
	}
	return nil
}

func (s *sqsStore) startPurge(ctx context.Context, queueName string) *protocol.AWSError {
	active, aerr := s.purgeActive(ctx, queueName)
	if aerr != nil {
		return aerr
	}
	if active {
		return errPurgeQueueInProgress()
	}
	deadline := s.clk.Now().Add(time.Minute).UnixMilli()
	key := serviceutil.RegionKey(s.region(ctx), queueName)
	if err := s.store.Set(ctx, nsPurge, key, strconv.FormatInt(deadline, 10)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sqsStore) purgeActive(ctx context.Context, queueName string) (bool, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), queueName)
	raw, found, err := s.store.Get(ctx, nsPurge, key)
	if err != nil {
		return false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return false, nil
	}
	deadline, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || s.clk.Now().UnixMilli() >= deadline {
		if err := s.store.Delete(ctx, nsPurge, key); err != nil {
			return false, protocol.Wrap(protocol.ErrInternalError, err)
		}
		return false, nil
	}
	return true, nil
}

func (s *sqsStore) listMessages(ctx context.Context, queueName string) ([]*Message, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), queueName+"/")
	pairs, err := s.store.Scan(ctx, nsMessages, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	msgs := make([]*Message, 0, len(pairs))
	for _, p := range pairs {
		var msg Message
		if err := json.Unmarshal([]byte(p.Value), &msg); err != nil {
			continue
		}
		msgs = append(msgs, &msg)
	}
	return msgs, nil
}

// ---- FIFO deduplication helpers --------------------------------------------

// dedupKey builds a store key for deduplication tracking.
func dedupKey(queueName, dedupID string) string {
	return queueName + "/" + dedupID
}

// isDuplicate checks whether a message with this dedup ID was sent within the
// 5-minute deduplication window. Returns true if the message is a duplicate.
func (s *sqsStore) isDuplicate(ctx context.Context, queueName, dedupID string) bool {
	key := serviceutil.RegionKey(s.region(ctx), dedupKey(queueName, dedupID))
	raw, found, err := s.store.Get(ctx, nsDedup, key)
	if err != nil || !found {
		return false
	}
	expiryMs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false
	}
	return s.clk.Now().UnixMilli() < expiryMs
}

// recordDedup records a dedup ID with a 5-minute expiry window.
func (s *sqsStore) recordDedup(ctx context.Context, queueName, dedupID string) {
	key := serviceutil.RegionKey(s.region(ctx), dedupKey(queueName, dedupID))
	expiry := s.clk.Now().Add(5 * time.Minute).UnixMilli()
	_ = s.store.Set(ctx, nsDedup, key, strconv.FormatInt(expiry, 10))
}

// ---- cross-region scan helpers (for background goroutines) -----------------

// scanAllQueues returns all queues across all regions. The returned keys are
// region-prefixed (e.g. "us-east-1/myQueue") so callers can pass them directly
// to scanAllMessagesForQueue.
func (s *sqsStore) scanAllQueues(ctx context.Context) ([]state.KV, error) {
	return s.store.Scan(ctx, nsQueues, "")
}

// scanAllMessagesForQueue returns all messages for a queue identified by its
// full region-prefixed key (as returned by scanAllQueues).
func (s *sqsStore) scanAllMessagesForQueue(ctx context.Context, regionQueueKey string) ([]state.KV, error) {
	return s.store.Scan(ctx, nsMessages, regionQueueKey+"/")
}

// ---- SQS-specific errors ---------------------------------------------------

func errQueueNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "AWS.SimpleQueueService.NonExistentQueue",
		Message:    fmt.Sprintf("The specified queue does not exist: %s", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errPurgeQueueInProgress() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "PurgeQueueInProgress",
		Message:    "Only one PurgeQueue operation is allowed each 60 seconds.",
		HTTPStatus: http.StatusBadRequest,
	}
}

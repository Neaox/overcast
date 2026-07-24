package sns

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsTopics        = "sns:topics"
	nsSubscriptions = "sns:subscriptions"
)

// Topic represents a stored SNS topic.
type Topic struct {
	Name             string            `json:"name"`
	ARN              string            `json:"arn"`
	Attributes       map[string]string `json:"attributes,omitempty"`
	CreatedTimestamp int64             `json:"created_timestamp"`
}

// Subscription represents a stored SNS subscription.
type Subscription struct {
	SubscriptionARN string `json:"subscription_arn"`
	TopicARN        string `json:"topic_arn"`
	TopicName       string `json:"topic_name"`
	Protocol        string `json:"protocol"`
	Endpoint        string `json:"endpoint"`
	// QueueName is the extracted SQS queue name when Protocol == "sqs".
	QueueName string `json:"queue_name,omitempty"`
	Owner     string `json:"owner"`
	// Attributes holds subscriber-level settings (RawMessageDelivery, FilterPolicy, …).
	Attributes map[string]string `json:"attributes,omitempty"`
}

// snsStore wraps state.Store with SNS-specific helpers.
type snsStore struct {
	store         state.Store
	clk           clock.Clock
	defaultRegion string
}

func newSNSStore(store state.Store, clk clock.Clock, defaultRegion string) *snsStore {
	return &snsStore{store: store, clk: clk, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *snsStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// ---- Topics ----------------------------------------------------------------

func (s *snsStore) getTopic(ctx context.Context, name string) (*Topic, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), name)
	raw, found, err := s.store.Get(ctx, nsTopics, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errTopicNotFound(name)
	}
	var t Topic
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &t, nil
}

func (s *snsStore) getTopicByARN(ctx context.Context, arn string) (*Topic, *protocol.AWSError) {
	name := topicNameFromARN(arn)
	if name == "" {
		return nil, errTopicNotFound(arn)
	}
	return s.getTopic(ctx, name)
}

func (s *snsStore) putTopic(ctx context.Context, t *Topic) *protocol.AWSError {
	raw, err := json.Marshal(t)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), t.Name)
	if err := s.store.Set(ctx, nsTopics, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *snsStore) deleteTopic(ctx context.Context, name string) *protocol.AWSError {
	key := serviceutil.RegionKey(s.region(ctx), name)
	if err := s.store.Delete(ctx, nsTopics, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *snsStore) listTopics(ctx context.Context) ([]*Topic, *protocol.AWSError) {
	scanPrefix := serviceutil.RegionKey(s.region(ctx), "")
	pairs, err := s.store.Scan(ctx, nsTopics, scanPrefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	topics := make([]*Topic, 0, len(pairs))
	for _, p := range pairs {
		var t Topic
		if err := json.Unmarshal([]byte(p.Value), &t); err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		topics = append(topics, &t)
	}
	return topics, nil
}

// ---- Subscriptions ---------------------------------------------------------

// subscriptionKey builds the store key for a subscription.
// Format: <topicName>/<subscriptionID> — keeps all subscriptions for a topic grouped.
func subscriptionKey(topicName, subID string) string {
	return topicName + "/" + subID
}

func (s *snsStore) putSubscription(ctx context.Context, sub *Subscription) *protocol.AWSError {
	// Extract subscription ID from ARN: last segment after ":"
	parts := strings.Split(sub.SubscriptionARN, ":")
	subID := parts[len(parts)-1]
	topicName := topicNameFromARN(sub.TopicARN)

	raw, err := json.Marshal(sub)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), subscriptionKey(topicName, subID))
	if err := s.store.Set(ctx, nsSubscriptions, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *snsStore) getSubscriptionByARN(ctx context.Context, subARN string) (*Subscription, *protocol.AWSError) {
	parts := strings.Split(subARN, ":")
	if len(parts) < 2 {
		return nil, errSubscriptionNotFound(subARN)
	}
	subID := parts[len(parts)-1]
	topicName := parts[len(parts)-2]

	key := serviceutil.RegionKey(s.region(ctx), subscriptionKey(topicName, subID))
	raw, found, err := s.store.Get(ctx, nsSubscriptions, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errSubscriptionNotFound(subARN)
	}
	var sub Subscription
	if err := json.Unmarshal([]byte(raw), &sub); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &sub, nil
}

func (s *snsStore) deleteSubscription(ctx context.Context, subARN string) *protocol.AWSError {
	parts := strings.Split(subARN, ":")
	if len(parts) < 2 {
		return errSubscriptionNotFound(subARN)
	}
	subID := parts[len(parts)-1]
	topicName := parts[len(parts)-2]

	key := serviceutil.RegionKey(s.region(ctx), subscriptionKey(topicName, subID))
	if err := s.store.Delete(ctx, nsSubscriptions, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *snsStore) listSubscriptionsByTopic(ctx context.Context, topicName string) ([]*Subscription, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), topicName+"/")
	pairs, err := s.store.Scan(ctx, nsSubscriptions, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	subs := make([]*Subscription, 0, len(pairs))
	for _, p := range pairs {
		var sub Subscription
		if err := json.Unmarshal([]byte(p.Value), &sub); err != nil {
			continue
		}
		subs = append(subs, &sub)
	}
	return subs, nil
}

// listAllSubscriptions uses Scan instead of List+per-key Get (storage-plan.md
// item 3.1), mirroring listSubscriptionsByTopic above.
func (s *snsStore) listAllSubscriptions(ctx context.Context) ([]*Subscription, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsSubscriptions, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	subs := make([]*Subscription, 0, len(pairs))
	for _, p := range pairs {
		var sub Subscription
		if err := json.Unmarshal([]byte(p.Value), &sub); err != nil {
			continue
		}
		subs = append(subs, &sub)
	}
	return subs, nil
}

// ---- Helpers ---------------------------------------------------------------

// topicNameFromARN extracts the topic name from an ARN.
// e.g. "arn:aws:sns:us-east-1:000000000000:my-topic" → "my-topic".
func topicNameFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// queueNameFromARN extracts the queue name from an SQS ARN.
// e.g. "arn:aws:sqs:us-east-1:000000000000:my-queue" → "my-queue".
func queueNameFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// ---- SNS-specific errors ---------------------------------------------------

func errTopicNotFound(nameOrARN string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NotFound",
		Message:    fmt.Sprintf("Topic does not exist: %s", nameOrARN),
		HTTPStatus: http.StatusNotFound,
	}
}

func errSubscriptionNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NotFound",
		Message:    fmt.Sprintf("Subscription does not exist: %s", arn),
		HTTPStatus: http.StatusNotFound,
	}
}

func errInvalidProtocol(proto string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameter",
		Message:    fmt.Sprintf("Invalid parameter: Protocol %q is not supported by this emulator.", proto),
		HTTPStatus: http.StatusBadRequest,
	}
}

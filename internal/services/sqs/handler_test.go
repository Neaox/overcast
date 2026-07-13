package sqs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

type cancelOnSecondMessageScanStore struct {
	state.Store
	scans atomic.Int32
}

func (s *cancelOnSecondMessageScanStore) Scan(ctx context.Context, namespace, prefix string) ([]state.KV, error) {
	if namespace == nsMessages && s.scans.Add(1) == 2 {
		return nil, context.Canceled
	}
	return s.Store.Scan(ctx, namespace, prefix)
}

type cancelOnFirstMessageScanStore struct {
	state.Store
}

func (s *cancelOnFirstMessageScanStore) Scan(ctx context.Context, namespace, prefix string) ([]state.KV, error) {
	if namespace == nsMessages {
		return nil, context.Canceled
	}
	return s.Store.Scan(ctx, namespace, prefix)
}

type contextAwareStore struct {
	state.Store
}

func (s *contextAwareStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return s.Store.Get(ctx, namespace, key)
}

func (s *contextAwareStore) Scan(ctx context.Context, namespace, prefix string) ([]state.KV, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Store.Scan(ctx, namespace, prefix)
}

func TestQueueNameFromURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "standard URL",
			input: "http://localhost:4566/000000000000/my-queue",
			want:  "my-queue",
		},
		{
			name:  "URL with trailing slash",
			input: "http://localhost:4566/000000000000/my-queue/",
			want:  "my-queue",
		},
		{
			name:  "AWS-style URL",
			input: "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue",
			want:  "my-queue",
		},
		{
			name:  "ARN format",
			input: "arn:aws:sqs:ap-southeast-2:000000000000:sqs-l-ase2-web-push-service-dynamodb-stream",
			want:  "sqs-l-ase2-web-push-service-dynamodb-stream",
		},
		{
			name:  "ARN with us-east-1",
			input: "arn:aws:sqs:us-east-1:123456789012:test-queue",
			want:  "test-queue",
		},
		{
			name:  "bare queue name",
			input: "my-queue",
			want:  "my-queue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := queueNameFromURL(tt.input)
			if got != tt.want {
				t.Errorf("queueNameFromURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReceiveMessageTyped_persistedMessageWithoutAttributes(t *testing.T) {
	// Given: a persisted message from an older store shape with nil Attributes.
	ctx := context.Background()
	store := state.NewMemoryStore()
	h := newHandler(&config.Config{
		Hostname:  "localhost.localstack.cloud",
		Port:      4566,
		Region:    "ap-southeast-2",
		AccountID: "000000000000",
	}, store, serviceutil.NewServiceLogger(zap.NewNop(), serviceName), clock.New())
	sqsStore := newSQSStore(store, clock.New(), "ap-southeast-2")
	queue := &Queue{
		Name: "test-queue",
		URL:  "http://localhost.localstack.cloud:4566/000000000000/test-queue",
		Attributes: map[string]string{
			"VisibilityTimeout": "30",
		},
	}
	if aerr := sqsStore.putQueue(ctx, queue); aerr != nil {
		t.Fatalf("putQueue: %v", aerr)
	}
	msg := &Message{
		MessageID:    "msg-1",
		Body:         "hello",
		MD5OfBody:    md5Hex([]byte("hello")),
		VisibleAfter: time.Now().Add(-time.Second),
		Attributes:   nil,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	key := serviceutil.RegionKey("ap-southeast-2", messageKey(queue.Name, msg.MessageID))
	if err := store.Set(ctx, nsMessages, key, string(raw)); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	// When: the message is received with system attributes requested.
	resp, aerr := h.receiveMessageTyped(ctx, &receiveMessageRequest{
		QueueUrl:       queue.URL,
		AttributeNames: []string{"All"},
	})

	// Then: receive succeeds and normalizes the message attributes.
	if aerr != nil {
		t.Fatalf("receiveMessageTyped: %v", aerr)
	}
	if len(resp.Messages) != 1 {
		t.Fatalf("received messages = %d, want 1", len(resp.Messages))
	}
	if got := resp.Messages[0].Attributes["ApproximateReceiveCount"]; got != "1" {
		t.Fatalf("ApproximateReceiveCount = %q, want 1", got)
	}
}

func TestReceiveMessageTyped_longPollContextCanceled(t *testing.T) {
	// Given: an empty queue and a state backend that observes cancellation during long polling
	ctx := context.Background()
	clk := clock.New()
	mem := state.NewMemoryStore()
	store := &cancelOnSecondMessageScanStore{Store: mem}
	h := newHandler(&config.Config{
		Hostname:  "localhost.localstack.cloud",
		Port:      4566,
		Region:    "ap-southeast-2",
		AccountID: "000000000000",
	}, store, serviceutil.NewServiceLogger(zap.NewNop(), serviceName), clk)
	queue := &Queue{
		Name: "empty-queue",
		URL:  "http://localhost.localstack.cloud:4566/000000000000/empty-queue",
		Attributes: map[string]string{
			"VisibilityTimeout": "30",
		},
	}
	if aerr := h.store.putQueue(ctx, queue); aerr != nil {
		t.Fatalf("putQueue: %v", aerr)
	}

	// When: the long poll wakes and the backend reports context cancellation
	waitTimeSeconds := 1
	resp, aerr := h.receiveMessageTyped(ctx, &receiveMessageRequest{
		QueueUrl:        queue.URL,
		WaitTimeSeconds: &waitTimeSeconds,
	})

	// Then: ReceiveMessage returns an empty response, not InternalError
	if aerr != nil {
		t.Fatalf("receiveMessageTyped returned error: %v", aerr)
	}
	if len(resp.Messages) != 0 {
		t.Fatalf("received messages = %d, want 0", len(resp.Messages))
	}
}

func TestReceiveMessageTyped_initialEmptyLongPollContextCanceled(t *testing.T) {
	// Given: an empty queue and a state backend that observes cancellation on the initial receive scan
	ctx := context.Background()
	mem := state.NewMemoryStore()
	store := &cancelOnFirstMessageScanStore{Store: mem}
	h := newHandler(&config.Config{
		Hostname:  "localhost.localstack.cloud",
		Port:      4566,
		Region:    "ap-southeast-2",
		AccountID: "000000000000",
	}, store, serviceutil.NewServiceLogger(zap.NewNop(), serviceName), clock.New())
	queue := &Queue{
		Name: "empty-queue",
		URL:  "http://localhost.localstack.cloud:4566/000000000000/empty-queue",
		Attributes: map[string]string{
			"VisibilityTimeout": "30",
		},
	}
	if aerr := h.store.putQueue(ctx, queue); aerr != nil {
		t.Fatalf("putQueue: %v", aerr)
	}

	// When: ReceiveMessage starts a long poll and the initial scan returns context cancellation
	waitTimeSeconds := 20
	resp, aerr := h.receiveMessageTyped(ctx, &receiveMessageRequest{
		QueueUrl:        queue.URL,
		WaitTimeSeconds: &waitTimeSeconds,
	})

	// Then: ReceiveMessage returns an empty response, not InternalError
	if aerr != nil {
		t.Fatalf("receiveMessageTyped returned error: %v", aerr)
	}
	if len(resp.Messages) != 0 {
		t.Fatalf("received messages = %d, want 0", len(resp.Messages))
	}
}

func TestReceiveMessageTyped_canceledRequestContext(t *testing.T) {
	// Given: an empty queue and a state backend that observes request cancellation.
	ctx := context.Background()
	mem := state.NewMemoryStore()
	store := &contextAwareStore{Store: mem}
	h := newHandler(&config.Config{
		Hostname:  "localhost.localstack.cloud",
		Port:      4566,
		Region:    "ap-southeast-2",
		AccountID: "000000000000",
	}, store, serviceutil.NewServiceLogger(zap.NewNop(), serviceName), clock.New())
	queue := &Queue{
		Name: "empty-queue",
		URL:  "http://localhost.localstack.cloud:4566/000000000000/empty-queue",
		Attributes: map[string]string{
			"VisibilityTimeout": "30",
		},
	}
	if aerr := h.store.putQueue(ctx, queue); aerr != nil {
		t.Fatalf("putQueue: %v", aerr)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	// When: the typed ReceiveMessage path runs after the request context is canceled.
	resp, aerr := h.receiveMessageTyped(canceledCtx, &receiveMessageRequest{
		QueueUrl: queue.URL,
	})

	// Then: receive succeeds with an empty result instead of leaking InternalError.
	if aerr != nil {
		t.Fatalf("receiveMessageTyped returned error: %v", aerr)
	}
	if len(resp.Messages) != 0 {
		t.Fatalf("received messages = %d, want 0", len(resp.Messages))
	}
}

func TestReceiveMessage_httpCanceledContext(t *testing.T) {
	// Given: an empty queue and an HTTP request whose context is already canceled
	ctx := context.Background()
	mem := state.NewMemoryStore()
	h := newHandler(&config.Config{
		Hostname:  "localhost.localstack.cloud",
		Port:      4566,
		Region:    "ap-southeast-2",
		AccountID: "000000000000",
	}, mem, serviceutil.NewServiceLogger(zap.NewNop(), serviceName), clock.New())
	queue := &Queue{
		Name: "empty-queue",
		URL:  "http://localhost.localstack.cloud:4566/000000000000/empty-queue",
		Attributes: map[string]string{
			"VisibilityTimeout": "30",
		},
	}
	if aerr := h.store.putQueue(ctx, queue); aerr != nil {
		t.Fatalf("putQueue: %v", aerr)
	}
	waitTimeSeconds := 20
	body, err := json.Marshal(receiveMessageRequest{
		QueueUrl:        queue.URL,
		WaitTimeSeconds: &waitTimeSeconds,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	canceledCtx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(canceledCtx)
	rec := httptest.NewRecorder()

	// When: the handler receives the canceled request context
	h.ReceiveMessage(rec, req)

	// Then: it does not convert request cancellation into an SQS InternalError
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

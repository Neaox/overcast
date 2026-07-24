package sqs

// service_debug_test.go covers the SQS Service's router.DebugStateProvider
// implementation (DebugNamespace/DebugStateKeys/DebugStateValues/
// DebugResetState) — docs/plans/storage-plan.md's graduation rule requires
// every dedicated table to stay visible to /_debug/state and resettable via
// /_debug/reset, mirroring DynamoDB's "dynamodb:items" and CloudWatch Logs'
// "logs:events" virtual namespaces. Package-level (no internal/router
// dependency, avoiding an import cycle) — internal/router/debug_test.go
// covers the HTTP-level wiring for DynamoDB/Logs already; this file is this
// package's equivalent for its own DebugStateProvider methods directly.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func newTestSQSService(t *testing.T) *Service {
	t.Helper()
	return New(&config.Config{
		Hostname:  "localhost",
		Port:      4566,
		Region:    "us-east-1",
		AccountID: "000000000000",
	}, state.NewMemoryStore(), zap.NewNop(), clock.New())
}

func TestService_DebugNamespace(t *testing.T) {
	svc := newTestSQSService(t)
	if got := svc.DebugNamespace(); got != "sqs:messages" {
		t.Fatalf("DebugNamespace() = %q, want sqs:messages", got)
	}
}

func TestService_DebugStateKeysAndValues(t *testing.T) {
	svc := newTestSQSService(t)
	ctx := context.Background()

	queue := &Queue{
		Name:       "debug-queue",
		URL:        "http://localhost:4566/000000000000/debug-queue",
		Attributes: map[string]string{"VisibilityTimeout": "30"},
	}
	if aerr := svc.handler.store.putQueue(ctx, queue); aerr != nil {
		t.Fatalf("putQueue: %v", aerr)
	}
	msg := &Message{
		MessageID:    "msg-1",
		Body:         "hello debug",
		MD5OfBody:    md5Hex([]byte("hello debug")),
		VisibleAfter: time.Now().Add(-time.Second),
	}
	if aerr := svc.handler.store.putMessage(ctx, queue.Name, msg); aerr != nil {
		t.Fatalf("putMessage: %v", aerr)
	}

	keys, err := svc.DebugStateKeys(ctx)
	if err != nil {
		t.Fatalf("DebugStateKeys: %v", err)
	}
	wantKey := "us-east-1/debug-queue/msg-1"
	found := false
	for _, k := range keys {
		if k == wantKey {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("DebugStateKeys() = %v, want to contain %q", keys, wantKey)
	}

	values, err := svc.DebugStateValues(ctx)
	if err != nil {
		t.Fatalf("DebugStateValues: %v", err)
	}
	raw, ok := values[wantKey]
	if !ok {
		t.Fatalf("DebugStateValues() missing key %q, got %v", wantKey, values)
	}
	var decoded Message
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("unmarshal debug value: %v", err)
	}
	if decoded.Body != msg.Body {
		t.Fatalf("debug value Body = %q, want %q", decoded.Body, msg.Body)
	}
	if _, truncated := values["_truncated"]; truncated {
		t.Fatalf("did not expect truncation with only one message")
	}
}

func TestService_DebugStateValues_truncationFlag(t *testing.T) {
	svc := newTestSQSService(t)
	ctx := context.Background()

	queue := &Queue{Name: "debug-truncate-queue", URL: "http://localhost:4566/000000000000/debug-truncate-queue", Attributes: map[string]string{}}
	if aerr := svc.handler.store.putQueue(ctx, queue); aerr != nil {
		t.Fatalf("putQueue: %v", aerr)
	}
	// One more message than the scan limit.
	for i := 0; i <= debugMessagesScanLimit; i++ {
		msg := &Message{
			MessageID:    fmt.Sprintf("msg-%05d", i),
			Body:         "x",
			MD5OfBody:    "d41d8cd98f00b204e9800998ecf8427e",
			VisibleAfter: time.Now().Add(-time.Second),
		}
		if aerr := svc.handler.store.putMessage(ctx, queue.Name, msg); aerr != nil {
			t.Fatalf("putMessage %d: %v", i, aerr)
		}
	}

	values, err := svc.DebugStateValues(ctx)
	if err != nil {
		t.Fatalf("DebugStateValues: %v", err)
	}
	if _, truncated := values["_truncated"]; !truncated {
		t.Fatalf("expected _truncated marker when message count exceeds debugMessagesScanLimit")
	}

	keys, err := svc.DebugStateKeys(ctx)
	if err != nil {
		t.Fatalf("DebugStateKeys: %v", err)
	}
	if len(keys) != debugMessagesScanLimit {
		t.Fatalf("DebugStateKeys() returned %d keys, want capped at %d", len(keys), debugMessagesScanLimit)
	}
}

func TestService_DebugResetState(t *testing.T) {
	svc := newTestSQSService(t)
	ctx := context.Background()

	queue := &Queue{Name: "reset-queue", URL: "http://localhost:4566/000000000000/reset-queue", Attributes: map[string]string{}}
	if aerr := svc.handler.store.putQueue(ctx, queue); aerr != nil {
		t.Fatalf("putQueue: %v", aerr)
	}
	msg := &Message{MessageID: "msg-1", Body: "x", MD5OfBody: "d41d8cd98f00b204e9800998ecf8427e", VisibleAfter: time.Now().Add(-time.Second)}
	if aerr := svc.handler.store.putMessage(ctx, queue.Name, msg); aerr != nil {
		t.Fatalf("putMessage: %v", aerr)
	}

	keys, err := svc.DebugStateKeys(ctx)
	if err != nil || len(keys) == 0 {
		t.Fatalf("expected at least one key before reset, got %v (err=%v)", keys, err)
	}

	if err := svc.DebugResetState(ctx); err != nil {
		t.Fatalf("DebugResetState: %v", err)
	}

	keys, err = svc.DebugStateKeys(ctx)
	if err != nil {
		t.Fatalf("DebugStateKeys after reset: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("DebugStateKeys after reset = %v, want empty", keys)
	}

	// The queue itself (kv-backed metadata) is untouched by a messages-only reset.
	if _, aerr := svc.handler.store.getQueue(ctx, queue.Name); aerr != nil {
		t.Fatalf("expected queue metadata to survive a messages-only DebugResetState, got %v", aerr)
	}
}

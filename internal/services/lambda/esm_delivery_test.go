package lambda

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// readyGatedStore wraps MemoryStore but makes Scan return empty until
// signal() is called. This simulates HybridStore's lazy-SQLite behaviour:
// data is present in the store but invisible to Scan before readiness.
// It implements state.ReadyAwaiter so the fix under test can be exercised.
type readyGatedStore struct {
	*state.MemoryStore
	readyCh   chan struct{}
	readyOnce sync.Once
}

func newReadyGatedStore() *readyGatedStore {
	return &readyGatedStore{
		MemoryStore: state.NewMemoryStore(),
		readyCh:     make(chan struct{}),
	}
}

// Scan returns empty until signal() is called, then delegates to MemoryStore.
func (s *readyGatedStore) Scan(ctx context.Context, namespace, prefix string) ([]state.KV, error) {
	select {
	case <-s.readyCh:
		return s.MemoryStore.Scan(ctx, namespace, prefix)
	default:
		return nil, nil
	}
}

// WaitReady satisfies state.ReadyAwaiter.
func (s *readyGatedStore) WaitReady(ctx context.Context) error {
	select {
	case <-s.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *readyGatedStore) signal() { s.readyOnce.Do(func() { close(s.readyCh) }) }

// noopReceiver satisfies events.MessageReceiver so the SQS poller can start
// without panicking. It blocks forever so the goroutine stays alive long
// enough for the test to observe it.
type noopReceiver struct{}

func (noopReceiver) ReceiveMessages(ctx context.Context, _ string, _, _ int) ([]events.ReceivedMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (noopReceiver) DeleteMessages(_ context.Context, _ string, _ []string) error { return nil }

// TestReloadAll_WaitsForStoreReady verifies that ReloadAll waits for the
// store's ReadyAwaiter before scanning for ESMs. Without this wait, a
// HybridStore whose SQLite backend has not yet opened will return an empty
// Scan result, causing persisted ESMs to be silently skipped — pollers never
// start and SQS messages are never processed.
func TestReloadAll_WaitsForStoreReady(t *testing.T) {
	// Given: a store that withholds Scan results until signalled.
	store := newReadyGatedStore()
	ls := newLambdaStore(store, "us-east-1", clock.New())
	es := newESMStore(ls)

	// An enabled SQS ESM is written directly (bypasses Scan, so it is
	// present in the store's memory but invisible to Scan until signal).
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	esm := &EventSourceMapping{
		UUID:           "test-uuid",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn: "arn:aws:sqs:us-east-1:000000000000:my-queue",
		State:          esmStateEnabled,
		BatchSize:      1,
	}
	if aerr := es.putESM(ctx, esm); aerr != nil {
		t.Fatal(aerr)
	}

	slog := serviceutil.NewServiceLogger(zap.NewNop(), "lambda")
	mgr := newESMDeliveryManager(
		es,
		nil, // invoker — not exercised by ReloadAll itself
		noopReceiver{},
		nil, // enqueuer
		nil, // bus
		slog,
		clock.New(),
		&config.Config{},
		context.Background(),
	)

	// When: ReloadAll is called before the store signals readiness.
	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.ReloadAll(context.Background())
	}()

	// Then: the poller goroutine should not start until the store is ready.
	select {
	case <-done:
		// ReloadAll returned before the store was ready — check no pollers started.
		mgr.mu.Lock()
		n := len(mgr.stop)
		mgr.mu.Unlock()
		if n != 0 {
			t.Errorf("ReloadAll started %d poller(s) before store was ready; want 0", n)
		}
	case <-time.After(50 * time.Millisecond):
		// ReloadAll is blocked waiting for the store — this is the correct behaviour.
	}

	// Signal readiness — ReloadAll must now find the ESM and start its poller.
	store.signal()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReloadAll did not complete after store became ready")
	}

	mgr.mu.Lock()
	n := len(mgr.stop)
	mgr.mu.Unlock()
	if n != 1 {
		t.Errorf("after store ready: got %d running poller(s), want 1", n)
	}

	mgr.StopAll()
}

func TestTableNameFromStreamARN(t *testing.T) {
	tests := []struct {
		name string
		arn  string
		want string
	}{
		{
			name: "standard DynamoDB stream ARN",
			arn:  "arn:aws:dynamodb:us-east-1:000000000000:table/MyTable/stream/2024-01-01T00:00:00.000",
			want: "MyTable",
		},
		{
			name: "stream ARN with dashes in table name",
			arn:  "arn:aws:dynamodb:ap-southeast-2:000000000000:table/ddb-l-ase2-web-push-service-data/stream/2026-05-01T01:27:43.777",
			want: "ddb-l-ase2-web-push-service-data",
		},
		{
			name: "stream ARN with different region and account",
			arn:  "arn:aws:dynamodb:eu-west-1:123456789012:table/orders/stream/2025-06-15T12:00:00.000",
			want: "orders",
		},
		{
			name: "table ARN without stream suffix",
			arn:  "arn:aws:dynamodb:us-east-1:000000000000:table/JustATable",
			want: "JustATable",
		},
		{
			name: "malformed ARN returns full string",
			arn:  "not-an-arn",
			want: "not-an-arn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tableNameFromStreamARN(tt.arn)
			if got != tt.want {
				t.Errorf("tableNameFromStreamARN(%q) = %q, want %q", tt.arn, got, tt.want)
			}
		})
	}
}

func TestListAllESMs_FindsCrossRegionMappings(t *testing.T) {
	// Given: ESMs stored under different regions.
	store := state.NewMemoryStore()
	ls := newLambdaStore(store, "us-east-1", clock.New())
	es := &esmStore{s: ls}

	// Store an ESM in us-east-1.
	esm1 := &EventSourceMapping{
		UUID:           "uuid-us-east",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn1",
		EventSourceArn: "arn:aws:dynamodb:us-east-1:000000000000:table/T1/stream/2024-01-01T00:00:00.000",
		State:          esmStateEnabled,
	}
	ctx1 := middleware.ContextWithRegion(context.Background(), "us-east-1")
	if aerr := es.putESM(ctx1, esm1); aerr != nil {
		t.Fatal(aerr)
	}

	// Store an ESM in ap-southeast-2.
	esm2 := &EventSourceMapping{
		UUID:           "uuid-ap-se2",
		FunctionArn:    "arn:aws:lambda:ap-southeast-2:000000000000:function:fn2",
		EventSourceArn: "arn:aws:dynamodb:ap-southeast-2:000000000000:table/T2/stream/2026-05-01T00:00:00.000",
		State:          esmStateEnabled,
	}
	ctx2 := middleware.ContextWithRegion(context.Background(), "ap-southeast-2")
	if aerr := es.putESM(ctx2, esm2); aerr != nil {
		t.Fatal(aerr)
	}

	// When: listESMs is called with default region (us-east-1).
	defaultCtx := context.Background() // no region → falls back to us-east-1
	regional, _ := es.listESMs(defaultCtx, "", "")
	if len(regional) != 1 {
		t.Fatalf("listESMs (default region): expected 1 ESM, got %d", len(regional))
	}
	if regional[0].UUID != "uuid-us-east" {
		t.Fatalf("listESMs returned wrong ESM: %s", regional[0].UUID)
	}

	// Then: listAllESMs finds BOTH.
	all, aerr := es.listAllESMs(context.Background())
	if aerr != nil {
		t.Fatal(aerr)
	}
	if len(all) != 2 {
		t.Fatalf("listAllESMs: expected 2 ESMs, got %d", len(all))
	}

	uuids := map[string]bool{}
	for _, m := range all {
		uuids[m.UUID] = true
	}
	if !uuids["uuid-us-east"] || !uuids["uuid-ap-se2"] {
		t.Fatalf("listAllESMs missing expected UUIDs: got %v", uuids)
	}
}

// TestPollSQS_UsesRegionFromESMArn verifies that pollSQS derives a region
// context from the ESM's FunctionArn rather than inheriting the region-less
// baseCtx. Without this, getESM looks in the wrong region namespace (the
// store default), returns nil, and the poller exits on the very first tick —
// silently leaving messages unprocessed even though the ESM is Enabled.
//
// This mirrors the pattern already used by makeStreamHandler for DynamoDB
// stream ESMs, and reflects real AWS behaviour: SQS → Lambda ESMs require the
// queue and function to be in the same region.
func TestPollSQS_UsesRegionFromESMArn(t *testing.T) {
	// Given: a store whose default region is us-east-1, but the ESM is
	// created in ap-southeast-2. Without a region-aware context the poller
	// looks in the wrong namespace and exits immediately on the first tick.
	store := state.NewMemoryStore()
	ls := newLambdaStore(store, "us-east-1", clock.New()) // default ≠ ESM region
	es := newESMStore(ls)

	aseCtx := middleware.ContextWithRegion(context.Background(), "ap-southeast-2")
	esm := &EventSourceMapping{
		UUID:           "region-test-uuid",
		FunctionArn:    "arn:aws:lambda:ap-southeast-2:000000000000:function:fn",
		EventSourceArn: "arn:aws:sqs:ap-southeast-2:000000000000:region-test-queue",
		State:          esmStateEnabled,
		BatchSize:      1,
	}
	if aerr := es.putESM(aseCtx, esm); aerr != nil {
		t.Fatalf("putESM: %v", aerr)
	}

	recv := newSignalReceiver()
	slog := serviceutil.NewServiceLogger(zap.NewNop(), "lambda")
	// baseCtx has NO region — reproduces the production scenario where the
	// delivery manager is started from main, not from an HTTP request handler.
	mgr := newESMDeliveryManager(
		es, nil, recv, nil, nil,
		slog, clock.New(), &config.Config{},
		context.Background(),
	)

	// When: the poller starts with a region-less base context.
	mgr.Start(esm)

	// Then: the poller must survive and eventually call ReceiveMessages.
	// If the region bug is present the poller exits after the first tick
	// (getESM returns nil for the wrong region) and ReceiveMessages is never called.
	select {
	case <-recv.called:
		// Poller used the correct region, found the ESM, and reached ReceiveMessages.
	case <-time.After(5 * time.Second):
		t.Fatal("ReceiveMessages was never called: poller exited because getESM returned nil (wrong region context)")
	}

	mgr.StopAll()
}

// transientGetStore wraps MemoryStore and returns an error for the first
// failCount calls to Get, then delegates normally. Used to simulate transient
// SQLite/IO failures (e.g. lock contention on a Windows-mounted volume).
type transientGetStore struct {
	*state.MemoryStore
	mu        sync.Mutex
	failCount int
}

func newTransientGetStore(failCount int) *transientGetStore {
	return &transientGetStore{MemoryStore: state.NewMemoryStore(), failCount: failCount}
}

func (s *transientGetStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	s.mu.Lock()
	if s.failCount > 0 {
		s.failCount--
		s.mu.Unlock()
		return "", false, errors.New("transient store error (simulated)")
	}
	s.mu.Unlock()
	return s.MemoryStore.Get(ctx, namespace, key)
}

// signalReceiver satisfies events.MessageReceiver. The first call to
// ReceiveMessages closes the `called` channel so the test can observe it,
// then blocks until ctx is cancelled.
type signalReceiver struct {
	called   chan struct{}
	callOnce sync.Once
}

func newSignalReceiver() *signalReceiver { return &signalReceiver{called: make(chan struct{})} }

func (r *signalReceiver) ReceiveMessages(ctx context.Context, _ string, _, _ int) ([]events.ReceivedMessage, error) {
	r.callOnce.Do(func() { close(r.called) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (r *signalReceiver) DeleteMessages(_ context.Context, _ string, _ []string) error { return nil }

// TestPollSQS_TransientStoreErrorDoesNotKillPoller verifies that a transient
// error returned by getESM does not permanently terminate the polling loop.
//
// Before the fix, any non-nil error from getESM caused pollSQS to return,
// silently stopping the ESM — even for recoverable errors like SQLite lock
// contention. The poller should log and continue to the next tick instead.
func TestPollSQS_TransientStoreErrorDoesNotKillPoller(t *testing.T) {
	// Given: a store that returns a transient error on the first Get call,
	// then behaves normally. One error tick is enough to kill the poller if
	// the bug is present.
	store := newTransientGetStore(1)
	ls := newLambdaStore(store, "us-east-1", clock.New())
	es := newESMStore(ls)

	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	esm := &EventSourceMapping{
		UUID:           "transient-err-test",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn: "arn:aws:sqs:us-east-1:000000000000:transient-queue",
		State:          esmStateEnabled,
		BatchSize:      1,
	}
	if aerr := es.putESM(ctx, esm); aerr != nil {
		t.Fatalf("putESM: %v", aerr)
	}

	recv := newSignalReceiver()
	slog := serviceutil.NewServiceLogger(zap.NewNop(), "lambda")
	mgr := newESMDeliveryManager(
		es, nil, recv, nil, nil,
		slog, clock.New(), &config.Config{},
		context.Background(),
	)

	// When: the poller is started.
	mgr.Start(esm)

	// Then: even after the transient error tick, the poller must survive and
	// eventually call ReceiveMessages once the store recovers.
	// With the 1s polling interval the receiver is called within ~3s at most
	// (tick 1 errors, tick 2 succeeds, receiver called).
	select {
	case <-recv.called:
		// Poller survived the error and reached ReceiveMessages — correct.
	case <-time.After(5 * time.Second):
		t.Fatal("ReceiveMessages was never called: poller was killed by a transient store error")
	}

	mgr.StopAll()
}

// ─── Filter criteria delivery tests ──────────────────────────────────────────

// filterCapturingReceiver satisfies events.MessageReceiver for filter tests.
// Deleted receipt handles are captured for assertion.
type filterCapturingReceiver struct {
	msgs      []events.ReceivedMessage
	mu        sync.Mutex
	deleted   []string
	firstCall sync.Once
}

func newFilterCapturingReceiver(msgs []events.ReceivedMessage) *filterCapturingReceiver {
	return &filterCapturingReceiver{msgs: msgs}
}

func (r *filterCapturingReceiver) ReceiveMessages(ctx context.Context, _ string, _, _ int) ([]events.ReceivedMessage, error) {
	var result []events.ReceivedMessage
	r.firstCall.Do(func() { result = r.msgs })
	if result != nil {
		return result, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *filterCapturingReceiver) DeleteMessages(_ context.Context, _ string, handles []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, handles...)
	return nil
}

func (r *filterCapturingReceiver) deletedHandles() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.deleted...)
}

// TestFilterAndDeleteSQS_nonMatchingMessagesDeleted verifies that
// filterAndDeleteSQS deletes non-matching messages from the queue and returns
// only those that satisfy the filter criteria.
func TestFilterAndDeleteSQS_nonMatchingMessagesDeleted(t *testing.T) {
	// Given: an ESM with a body content filter.
	store := state.NewMemoryStore()
	ls := newLambdaStore(store, "us-east-1", clock.New())
	es := newESMStore(ls)

	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	esmInst := &EventSourceMapping{
		UUID:           "filter-sqs-uuid",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn: "arn:aws:sqs:us-east-1:000000000000:filter-queue",
		State:          esmStateEnabled,
		BatchSize:      10,
		FilterCriteria: &FilterCriteria{
			Filters: []Filter{{Pattern: `{"body": ["match-me"]}`}},
		},
	}
	if aerr := es.putESM(ctx, esmInst); aerr != nil {
		t.Fatal(aerr)
	}

	recv := newFilterCapturingReceiver(nil)
	slog := serviceutil.NewServiceLogger(zap.NewNop(), "lambda")
	mgr := newESMDeliveryManager(
		es, nil, recv, nil, nil,
		slog, clock.New(), &config.Config{},
		context.Background(),
	)

	msgs := []events.ReceivedMessage{
		{MessageID: "msg1", ReceiptHandle: "rh1", Body: "match-me"},
		{MessageID: "msg2", ReceiptHandle: "rh2", Body: "no-match"},
		{MessageID: "msg3", ReceiptHandle: "rh3", Body: "also-no-match"},
	}

	// When: filterAndDeleteSQS is called.
	matching := mgr.filterAndDeleteSQS(ctx, esmInst, "filter-queue", msgs)

	// Then: only the matching message is returned.
	if len(matching) != 1 || matching[0].MessageID != "msg1" {
		t.Errorf("filterAndDeleteSQS: got %d messages, want 1 (msg1); got IDs: %v",
			len(matching), func() []string {
				ids := make([]string, len(matching))
				for i, m := range matching {
					ids[i] = m.MessageID
				}
				return ids
			}())
	}

	// And: non-matching receipt handles were deleted.
	deleted := recv.deletedHandles()
	if len(deleted) != 2 {
		t.Fatalf("expected 2 deleted receipt handles; got %d: %v", len(deleted), deleted)
	}
	deletedSet := map[string]bool{}
	for _, h := range deleted {
		deletedSet[h] = true
	}
	if !deletedSet["rh2"] || !deletedSet["rh3"] {
		t.Errorf("expected rh2 and rh3 to be deleted; got %v", deleted)
	}
}

// TestFilterAndDeleteSQS_nilCriteria passes all messages unchanged.
func TestFilterAndDeleteSQS_nilCriteria(t *testing.T) {
	store := state.NewMemoryStore()
	ls := newLambdaStore(store, "us-east-1", clock.New())
	es := newESMStore(ls)

	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	esmInst := &EventSourceMapping{
		UUID:           "no-filter-uuid",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn: "arn:aws:sqs:us-east-1:000000000000:no-filter-queue",
		State:          esmStateEnabled,
		BatchSize:      10,
		FilterCriteria: nil,
	}

	recv := newFilterCapturingReceiver(nil)
	slog := serviceutil.NewServiceLogger(zap.NewNop(), "lambda")
	mgr := newESMDeliveryManager(
		es, nil, recv, nil, nil,
		slog, clock.New(), &config.Config{},
		context.Background(),
	)

	msgs := []events.ReceivedMessage{
		{MessageID: "m1", ReceiptHandle: "rh1", Body: "anything"},
		{MessageID: "m2", ReceiptHandle: "rh2", Body: "anything-else"},
	}
	matching := mgr.filterAndDeleteSQS(ctx, esmInst, "no-filter-queue", msgs)
	if len(matching) != 2 {
		t.Errorf("nil FilterCriteria should pass all messages; got %d", len(matching))
	}
	if len(recv.deletedHandles()) != 0 {
		t.Errorf("nil FilterCriteria must not delete any messages; got %v", recv.deletedHandles())
	}
}

// TestDynamoDBESM_FilterCriteria_nonMatchingDropped verifies that a DynamoDB
// stream record that does NOT satisfy the ESM filter criteria is silently
// dropped — the iterator advances past it and Lambda is never invoked.
//
// The invoker is nil intentionally: the filter must return before the invoker
// is called, so a nil dereference would make the bug immediately visible.
func TestDynamoDBESM_FilterCriteria_nonMatchingDropped(t *testing.T) {
	// Given: an ESM filtering to INSERT events only.
	store := state.NewMemoryStore()
	ls := newLambdaStore(store, "us-east-1", clock.New())
	es := newESMStore(ls)

	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	esmInst := &EventSourceMapping{
		UUID:           "ddb-filter-uuid",
		FunctionArn:    "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn: "arn:aws:dynamodb:us-east-1:000000000000:table/FilterTable/stream/2024-01-01T00:00:00.000",
		State:          esmStateEnabled,
		FilterCriteria: &FilterCriteria{
			Filters: []Filter{{Pattern: `{"eventName": ["INSERT"]}`}},
		},
	}
	if aerr := es.putESM(ctx, esmInst); aerr != nil {
		t.Fatal(aerr)
	}

	bus := events.NewBus()
	slog := serviceutil.NewServiceLogger(zap.NewNop(), "lambda")
	// invoker is nil — if the filter fails to drop the record, invoker.Invoke
	// panics, making the bug immediately visible.
	mgr := newESMDeliveryManager(
		es, nil /* invoker */, noopReceiver{}, nil, bus,
		slog, clock.New(), &config.Config{},
		context.Background(),
	)

	// Subscribe the ESM handler to the bus.
	mgr.Start(esmInst)

	// When: a MODIFY event is published (does NOT match the INSERT-only filter).
	bus.Publish(ctx, events.Event{
		Type: events.DynamoDBStreamModify,
		Payload: events.DynamoDBStreamPayload{
			Table:     "FilterTable",
			EventName: "MODIFY",
			Keys:      map[string]any{"PK": map[string]any{"S": "row1"}},
		},
	})

	// StopAll waits for m.wg to drain so all delivery goroutines have
	// completed before we inspect store state.
	mgr.StopAll()

	// Then: LastProcessingResult must still be empty — no invoke was attempted.
	updated, aerr := es.getESM(ctx, esmInst.UUID)
	if aerr != nil {
		t.Fatal(aerr)
	}
	if updated.LastProcessingResult != "" {
		t.Errorf("filtered MODIFY record should not update LastProcessingResult; got %q",
			updated.LastProcessingResult)
	}
}

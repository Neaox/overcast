package lambda

import (
	"context"
	"errors"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

// testFetch returns a deterministic zip payload for the given bucket/key.
func testFetch(data []byte) S3FetchFunc {
	return func(_ context.Context, _, _ string) ([]byte, error) {
		return data, nil
	}
}

func testWatcher(t *testing.T, fetch S3FetchFunc) (*s3SyncWatcher, *lambdaStore) {
	t.Helper()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	log, _ := zap.NewDevelopment()
	w := newS3SyncWatcher(ls, fetch, log, clock.New())
	return w, ls
}

// seedFunction stores a minimal Function in the store and returns it.
func seedFunction(t *testing.T, ls *lambdaStore, name, bucket, key string) *Function {
	t.Helper()
	fn := &Function{
		Name:         name,
		ARN:          "arn:aws:lambda:us-east-1:000000000000:function:" + name,
		CodeS3Bucket: bucket,
		CodeS3Key:    key,
		RevisionId:   "initial",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed function %s: %v", name, aerr)
	}
	return fn
}

func TestS3SyncWatcher_matchingObjectUpdatesCodeZip(t *testing.T) {
	// Given: a function backed by s3://my-bucket/fn.zip and a watcher.
	newZip := []byte("new-zip-contents")
	w, ls := testWatcher(t, testFetch(newZip))
	seedFunction(t, ls, "my-fn", "my-bucket", "fn.zip")

	// When: an S3ObjectCreated event fires for that exact object.
	w.onS3ObjectCreated(context.Background(), events.Event{
		Type: events.S3ObjectCreated,
		Payload: events.S3ObjectPayload{
			Bucket: "my-bucket",
			Key:    "fn.zip",
		},
	})

	// Then: the stored function's CodeZip is updated.
	fn, aerr := ls.getFunction(context.Background(), "my-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if string(fn.CodeZip) != string(newZip) {
		t.Errorf("CodeZip = %q, want %q", fn.CodeZip, newZip)
	}
	if fn.RevisionId == "initial" {
		t.Error("RevisionId should have been bumped")
	}
	if fn.CodeSize != int64(len(newZip)) {
		t.Errorf("CodeSize = %d, want %d", fn.CodeSize, len(newZip))
	}
}

func TestS3SyncWatcher_nonMatchingObjectLeavesCodeZipUnchanged(t *testing.T) {
	// Given: a function backed by s3://my-bucket/fn.zip.
	w, ls := testWatcher(t, testFetch([]byte("should-not-be-used")))
	seedFunction(t, ls, "my-fn", "my-bucket", "fn.zip")

	// When: an S3ObjectCreated event fires for a different object.
	w.onS3ObjectCreated(context.Background(), events.Event{
		Type: events.S3ObjectCreated,
		Payload: events.S3ObjectPayload{
			Bucket: "other-bucket",
			Key:    "other.zip",
		},
	})

	// Then: the stored function is not modified.
	fn, _ := ls.getFunction(context.Background(), "my-fn")
	if len(fn.CodeZip) != 0 {
		t.Errorf("expected CodeZip unchanged (nil), got %d bytes", len(fn.CodeZip))
	}
	if fn.RevisionId != "initial" {
		t.Error("RevisionId should not have changed")
	}
}

func TestS3SyncWatcher_fetchErrorLeavesCodeZipUnchanged(t *testing.T) {
	// Given: a fetch func that always fails.
	failFetch := func(_ context.Context, _, _ string) ([]byte, error) {
		return nil, errors.New("s3: connection refused")
	}
	w, ls := testWatcher(t, failFetch)
	seedFunction(t, ls, "my-fn", "my-bucket", "fn.zip")

	// When: the S3ObjectCreated event fires but the fetch fails.
	w.onS3ObjectCreated(context.Background(), events.Event{
		Type: events.S3ObjectCreated,
		Payload: events.S3ObjectPayload{
			Bucket: "my-bucket",
			Key:    "fn.zip",
		},
	})

	// Then: the stored function is unchanged.
	fn, _ := ls.getFunction(context.Background(), "my-fn")
	if len(fn.CodeZip) != 0 {
		t.Errorf("expected CodeZip unchanged (nil), got %d bytes", len(fn.CodeZip))
	}
}

func TestS3SyncWatcher_onlyMatchingFunctionUpdated(t *testing.T) {
	// Given: two functions sharing a bucket but with different keys.
	newZip := []byte("updated-zip")
	w, ls := testWatcher(t, testFetch(newZip))
	seedFunction(t, ls, "fn-a", "shared-bucket", "fn-a.zip")
	seedFunction(t, ls, "fn-b", "shared-bucket", "fn-b.zip")

	// When: the event fires for fn-a's key only.
	w.onS3ObjectCreated(context.Background(), events.Event{
		Type: events.S3ObjectCreated,
		Payload: events.S3ObjectPayload{
			Bucket: "shared-bucket",
			Key:    "fn-a.zip",
		},
	})

	// Then: fn-a is updated, fn-b is not.
	fnA, _ := ls.getFunction(context.Background(), "fn-a")
	if string(fnA.CodeZip) != string(newZip) {
		t.Errorf("fn-a CodeZip = %q, want %q", fnA.CodeZip, newZip)
	}
	fnB, _ := ls.getFunction(context.Background(), "fn-b")
	if len(fnB.CodeZip) != 0 {
		t.Errorf("fn-b CodeZip should be unchanged, got %d bytes", len(fnB.CodeZip))
	}
}

func TestS3SyncWatcher_register_cancelRemovesSubscription(t *testing.T) {
	// Given: a watcher registered on a bus.
	w, _ := testWatcher(t, testFetch([]byte("zip")))
	bus := events.NewBus()
	defer bus.Stop()
	cancel := w.register(bus)

	// When: the subscription is cancelled, no more events are dispatched.
	// (This test mainly verifies cancel doesn't panic and returns a callable.)
	cancel() // must not panic
}

func TestS3SyncWatcher_wrongPayloadTypeIgnored(t *testing.T) {
	// Given: a watcher and a function in the store.
	called := false
	fetchSpy := func(_ context.Context, _, _ string) ([]byte, error) {
		called = true
		return nil, nil
	}
	w, ls := testWatcher(t, fetchSpy)
	seedFunction(t, ls, "my-fn", "my-bucket", "fn.zip")

	// When: an event arrives with a non-S3ObjectPayload payload.
	w.onS3ObjectCreated(context.Background(), events.Event{
		Type:    events.S3ObjectCreated,
		Payload: "not-an-S3ObjectPayload",
	})

	// Then: the fetch func is never called.
	if called {
		t.Error("fetch should not have been called for wrong payload type")
	}
}

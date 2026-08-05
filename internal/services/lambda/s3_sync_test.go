package lambda

import (
	"context"
	"errors"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

// testFetch returns a deterministic zip payload for the given bucket/key.
func testFetch(data []byte) S3FetchFunc {
	return func(_ context.Context, _, _ string) ([]byte, *protocol.AWSError) {
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

	// Then: the stored function's package is updated (records travel without
	// bytes; loadFunctionCode materializes them).
	fn, aerr := ls.getFunction(context.Background(), "my-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if aerr := ls.loadFunctionCode(context.Background(), fn); aerr != nil {
		t.Fatalf("loadFunctionCode: %v", aerr)
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
	if fn.CodeHash != sha256hex(newZip) {
		t.Errorf("CodeHash = %q, want sha256 of the synced package — identity would miss this code change", fn.CodeHash)
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
	failFetch := func(_ context.Context, _, _ string) ([]byte, *protocol.AWSError) {
		return nil, protocol.Wrap(protocol.ErrInternalError, errors.New("s3: connection refused"))
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

func TestS3SyncWatcher_inlineDetachDuringFetchWins(t *testing.T) {
	// Given: an S3-backed function and a watcher blocked in its object fetch.
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	fetch := func(_ context.Context, _, _ string) ([]byte, *protocol.AWSError) {
		close(fetchStarted)
		<-releaseFetch
		return []byte("stale-s3-code"), nil
	}
	w, ls := testWatcher(t, fetch)
	seeded := seedFunction(t, ls, "detach-race-fn", "my-bucket", "fn.zip")
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.syncFunctionCode(context.Background(), seeded)
	}()
	<-fetchStarted

	// When: UpdateFunctionCode atomically installs inline code and detaches the
	// S3 source before the watcher's old fetch completes.
	inlineCode := []byte("current-inline-code")
	_, changed, aerr := ls.mutateFunction(context.Background(), seeded.Name, func(current *Function) (bool, *protocol.AWSError) {
		current.setCode(inlineCode)
		current.CodeS3Bucket = ""
		current.CodeS3Key = ""
		current.RevisionId = "inline-revision"
		return true, nil
	})
	if aerr != nil {
		t.Fatalf("detach S3 source: %v", aerr)
	}
	if !changed {
		t.Fatal("detach S3 source reported no mutation")
	}
	close(releaseFetch)
	<-done

	// Then: the stale watcher cannot restore the old S3 binding or package.
	got, aerr := ls.getFunction(context.Background(), seeded.Name)
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if aerr := ls.loadFunctionCode(context.Background(), got); aerr != nil {
		t.Fatalf("loadFunctionCode: %v", aerr)
	}
	if string(got.CodeZip) != string(inlineCode) {
		t.Fatalf("CodeZip after race = %q, want inline code %q", got.CodeZip, inlineCode)
	}
	if got.CodeS3Bucket != "" || got.CodeS3Key != "" {
		t.Fatalf("S3 binding restored after detach: bucket=%q key=%q", got.CodeS3Bucket, got.CodeS3Key)
	}
	if got.RevisionId != "inline-revision" {
		t.Fatalf("RevisionId after race = %q, want inline-revision", got.RevisionId)
	}
}

func TestS3SyncWatcher_newerSameBindingUpdateDuringFetchWins(t *testing.T) {
	// Given: an S3-backed function and a watcher blocked while fetching an old
	// object generation.
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	fetch := func(_ context.Context, _, _ string) ([]byte, *protocol.AWSError) {
		close(fetchStarted)
		<-releaseFetch
		return []byte("stale-s3-code"), nil
	}
	w, ls := testWatcher(t, fetch)
	seeded := seedFunction(t, ls, "same-binding-race-fn", "my-bucket", "fn.zip")
	initialCode := []byte("initial-s3-code")
	_, changed, aerr := ls.mutateFunction(context.Background(), seeded.Name, func(current *Function) (bool, *protocol.AWSError) {
		current.setCode(initialCode)
		return true, nil
	})
	if aerr != nil {
		t.Fatalf("seed initial code: %v", aerr)
	}
	if !changed {
		t.Fatal("seed initial code reported no mutation")
	}
	seeded, aerr = ls.getFunction(context.Background(), seeded.Name)
	if aerr != nil {
		t.Fatalf("get seeded function: %v", aerr)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.syncFunctionCode(context.Background(), seeded)
	}()
	<-fetchStarted

	// When: a newer code update commits different bytes while retaining the
	// same S3 bucket/key before the old fetch completes.
	newerCode := []byte("newer-s3-code")
	_, changed, aerr = ls.mutateFunction(context.Background(), seeded.Name, func(current *Function) (bool, *protocol.AWSError) {
		current.setCode(newerCode)
		current.RevisionId = "newer-revision"
		return true, nil
	})
	if aerr != nil {
		t.Fatalf("install newer same-binding code: %v", aerr)
	}
	if !changed {
		t.Fatal("newer same-binding update reported no mutation")
	}
	close(releaseFetch)
	<-done

	// Then: the stale watcher loses its compare-and-swap and cannot overwrite
	// the newer package or revision.
	got, aerr := ls.getFunction(context.Background(), seeded.Name)
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if aerr := ls.loadFunctionCode(context.Background(), got); aerr != nil {
		t.Fatalf("loadFunctionCode: %v", aerr)
	}
	if string(got.CodeZip) != string(newerCode) {
		t.Fatalf("CodeZip after race = %q, want newer code %q", got.CodeZip, newerCode)
	}
	if got.CodeS3Bucket != "my-bucket" || got.CodeS3Key != "fn.zip" {
		t.Fatalf("S3 binding changed after race: bucket=%q key=%q", got.CodeS3Bucket, got.CodeS3Key)
	}
	if got.RevisionId != "newer-revision" {
		t.Fatalf("RevisionId after race = %q, want newer-revision", got.RevisionId)
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
	if aerr := ls.loadFunctionCode(context.Background(), fnA); aerr != nil {
		t.Fatalf("loadFunctionCode fn-a: %v", aerr)
	}
	if string(fnA.CodeZip) != string(newZip) {
		t.Errorf("fn-a CodeZip = %q, want %q", fnA.CodeZip, newZip)
	}
	fnB, _ := ls.getFunction(context.Background(), "fn-b")
	if aerr := ls.loadFunctionCode(context.Background(), fnB); aerr != nil {
		t.Fatalf("loadFunctionCode fn-b: %v", aerr)
	}
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
	fetchSpy := func(_ context.Context, _, _ string) ([]byte, *protocol.AWSError) {
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

// TestS3SyncWatcher_functionDeletedDuringFetch_staysDeleted: the watcher
// lists matching functions, then fetches the object from S3 — a window a
// DeleteFunction can land in. Writing the pre-fetch snapshot back would
// resurrect the deleted record (the #414 stale-snapshot family), so the
// refresh must merge into a fresh read and skip a function that is gone.
func TestS3SyncWatcher_functionDeletedDuringFetch_staysDeleted(t *testing.T) {
	// Given: a watcher whose S3 fetch deletes the function mid-flight — the
	// deterministic stand-in for a delete racing a slow download.
	var ls *lambdaStore
	fetch := func(ctx context.Context, _, _ string) ([]byte, *protocol.AWSError) {
		if aerr := ls.deleteFunction(ctx, "sync-del-fn"); aerr != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, errors.New("delete mid-fetch: "+aerr.Message))
		}
		return []byte("fresh-zip"), nil
	}
	w, store := testWatcher(t, fetch)
	ls = store
	seedFunction(t, ls, "sync-del-fn", "my-bucket", "fn.zip")

	// When: the object event triggers a sync.
	w.onS3ObjectCreated(context.Background(), events.Event{
		Type:    events.S3ObjectCreated,
		Payload: events.S3ObjectPayload{Bucket: "my-bucket", Key: "fn.zip"},
	})

	// Then: the function stays deleted.
	got, aerr := ls.getFunction(context.Background(), "sync-del-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if got != nil {
		t.Fatalf("function exists after delete — s3 sync wrote back its pre-fetch snapshot")
	}
}

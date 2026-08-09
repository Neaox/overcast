package lambda

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

// testFetch returns a deterministic zip payload for the given bucket/key.
func testFetch(data []byte) S3FetchFunc {
	return func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
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
	failFetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
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
	fetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
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
	fetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
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

func TestS3SyncWatcher_sameBytesManualUpdateDuringFetchWins(t *testing.T) {
	// Given: a refresh blocked after observing one explicit code generation.
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	code := []byte("same-code-bytes")
	fetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
		close(fetchStarted)
		<-releaseFetch
		return code, nil
	}
	w, ls := testWatcher(t, fetch)
	seeded := seedFunction(t, ls, "same-bytes-fence-fn", "my-bucket", "fn.zip")
	seeded.setCode(code)
	if aerr := ls.putFunction(context.Background(), seeded); aerr != nil {
		t.Fatalf("seed code: %v", aerr)
	}
	done := make(chan struct{})
	go func() { defer close(done); w.syncFunctionCode(context.Background(), seeded) }()
	<-fetchStarted

	// When: UpdateFunctionCode writes identical bytes, it is still a newer
	// explicit operation and rotates the code generation and revision.
	_, changed, aerr := ls.mutateFunction(context.Background(), seeded.Name, func(current *Function) (bool, *protocol.AWSError) {
		current.setCode(code)
		current.RevisionId = "manual-same-bytes-revision"
		return true, nil
	})
	if aerr != nil || !changed {
		t.Fatalf("manual update: changed=%v err=%v", changed, aerr)
	}
	close(releaseFetch)
	<-done

	// Then: the stale watcher cannot rotate the manual operation's revision.
	got, aerr := ls.getFunction(context.Background(), seeded.Name)
	if aerr != nil {
		t.Fatalf("get function: %v", aerr)
	}
	if got.RevisionId != "manual-same-bytes-revision" {
		t.Fatalf("RevisionId = %q, want manual revision", got.RevisionId)
	}
}

func TestS3SyncWatcher_configurationUpdateDuringFetchDoesNotFence(t *testing.T) {
	// Given: an S3 refresh blocked after reading the function.
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	fetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
		close(fetchStarted)
		<-releaseFetch
		return []byte("refreshed-code"), nil
	}
	w, ls := testWatcher(t, fetch)
	seeded := seedFunction(t, ls, "config-update-fn", "my-bucket", "fn.zip")
	done := make(chan struct{})
	go func() { defer close(done); w.syncFunctionCode(context.Background(), seeded) }()
	<-fetchStarted

	// When: an unrelated function configuration update rotates RevisionId.
	_, changed, aerr := ls.mutateFunction(context.Background(), seeded.Name, func(current *Function) (bool, *protocol.AWSError) {
		current.Description = "new description"
		current.RevisionId = "configuration-revision"
		return true, nil
	})
	if aerr != nil || !changed {
		t.Fatalf("configuration update: changed=%v err=%v", changed, aerr)
	}
	close(releaseFetch)
	<-done

	// Then: the refresh applies without losing the independent configuration.
	got, aerr := ls.getFunction(context.Background(), seeded.Name)
	if aerr != nil {
		t.Fatalf("get function: %v", aerr)
	}
	if aerr := ls.loadFunctionCode(context.Background(), got); aerr != nil {
		t.Fatalf("load code: %v", aerr)
	}
	if string(got.CodeZip) != "refreshed-code" || got.Description != "new description" {
		t.Fatalf("function after refresh = code %q description %q", got.CodeZip, got.Description)
	}
}

func TestS3SyncWatcher_concurrentEventsKeepNewestEvent(t *testing.T) {
	for _, newerCommitsFirst := range []bool{false, true} {
		name := "older commits first"
		if newerCommitsFirst {
			name = "newer commits first"
		}
		t.Run(name, func(t *testing.T) {
			testConcurrentS3EventsKeepNewest(t, newerCommitsFirst)
		})
	}
}

func testConcurrentS3EventsKeepNewest(t *testing.T, newerCommitsFirst bool) {
	t.Helper()
	// Given: two same-key events whose fetches can finish in either order.
	olderStarted := make(chan struct{})
	newerStarted := make(chan struct{})
	releaseOlder := make(chan struct{})
	releaseNewer := make(chan struct{})
	var calls int
	var callsMu sync.Mutex
	fetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			close(olderStarted)
			<-releaseOlder
			return []byte("older-event-code"), nil
		}
		close(newerStarted)
		<-releaseNewer
		return []byte("newest-event-code"), nil
	}
	w, ls := testWatcher(t, fetch)
	seeded := seedFunction(t, ls, "event-order-fn", "my-bucket", "fn.zip")
	seeded.setCode([]byte("initial-code"))
	if aerr := ls.putFunction(context.Background(), seeded); aerr != nil {
		t.Fatalf("seed code: %v", aerr)
	}

	olderDone := make(chan struct{})
	newerDone := make(chan struct{})
	go func() { defer close(olderDone); w.syncFunctionCodeForEvent(context.Background(), seeded, 10) }()
	<-olderStarted
	go func() { defer close(newerDone); w.syncFunctionCodeForEvent(context.Background(), seeded, 11) }()
	<-newerStarted

	// When: the selected event commits first. A late old event must be rejected,
	// while a late new event must still advance watcher-authored code.
	if newerCommitsFirst {
		close(releaseNewer)
		<-newerDone
		close(releaseOlder)
		<-olderDone
	} else {
		close(releaseOlder)
		<-olderDone
		close(releaseNewer)
		<-newerDone
	}

	// Then: completion order cannot let the older event replace or fence out the newest event.
	got, aerr := ls.getFunction(context.Background(), seeded.Name)
	if aerr != nil {
		t.Fatalf("get function: %v", aerr)
	}
	if aerr := ls.loadFunctionCode(context.Background(), got); aerr != nil {
		t.Fatalf("load code: %v", aerr)
	}
	if string(got.CodeZip) != "newest-event-code" {
		t.Fatalf("CodeZip = %q, want newest event", got.CodeZip)
	}
}

func TestS3SyncWatcher_recreatedFunctionUsesStableStateSlot(t *testing.T) {
	// Given: an initial function generation has applied an S3 event.
	codes := []string{"first-generation-refresh", "stale-refresh", "recreated-refresh"}
	call := 0
	fetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
		code := []byte(codes[call])
		call++
		return code, nil
	}
	w, ls := testWatcher(t, fetch)
	original := seedFunction(t, ls, "recreated-state-fn", "my-bucket", "fn.zip")
	original.setCode([]byte("original"))
	if aerr := ls.putFunction(context.Background(), original); aerr != nil {
		t.Fatalf("seed original: %v", aerr)
	}
	w.syncFunctionCodeForEvent(context.Background(), original, 1)

	// When: the function is recreated under the same ARN, an event carrying the
	// deleted creation must not mutate the replacement even with a newer bus sequence.
	recreated := *original
	recreated.CreationID = "recreated-generation"
	recreated.setCode([]byte("recreated-explicit-code"))
	recreated.RevisionId = "recreated-revision"
	if aerr := ls.putFunction(context.Background(), &recreated); aerr != nil {
		t.Fatalf("seed recreation: %v", aerr)
	}
	w.syncFunctionCodeForEvent(context.Background(), original, 2)
	got, aerr := ls.getFunction(context.Background(), recreated.Name)
	if aerr != nil {
		t.Fatalf("get after stale event: %v", aerr)
	}
	if got.RevisionId != "recreated-revision" {
		t.Fatalf("stale creation changed revision to %q", got.RevisionId)
	}

	// Then: the recreated generation can advance in the same per-ARN state slot.
	w.syncFunctionCodeForEvent(context.Background(), &recreated, 3)
	got, aerr = ls.getFunction(context.Background(), recreated.Name)
	if aerr != nil {
		t.Fatalf("get recreated function: %v", aerr)
	}
	if aerr := ls.loadFunctionCode(context.Background(), got); aerr != nil {
		t.Fatalf("load recreated code: %v", aerr)
	}
	if string(got.CodeZip) != "recreated-refresh" {
		t.Fatalf("CodeZip = %q, want recreated refresh", got.CodeZip)
	}
	if len(w.state) != 1 || w.state[recreated.ARN].creationID != recreated.CreationID {
		t.Fatalf("sync state = %#v, want one slot for recreated generation", w.state)
	}
}

func TestS3SyncWatcher_deleteFunctionCleansManyUniqueStateSlots(t *testing.T) {
	// Given: many distinct S3-backed functions each leave an ordering slot after sync.
	h, _ := lifecycleTestHandler(t)
	w := newS3SyncWatcher(h.ls, testFetch([]byte("refreshed")), zap.NewNop(), h.clk)
	h.setS3SyncWatcher(w)
	ctx := context.Background()
	for i := 0; i < 64; i++ {
		name := fmt.Sprintf("sync-cleanup-%d", i)
		fn := &Function{
			Name: name, ARN: "arn:aws:lambda:us-east-1:000000000000:function:" + name,
			CreationID: fmt.Sprintf("creation-%d", i), CodeS3Bucket: "bucket", CodeS3Key: name + ".zip",
		}
		fn.setCode([]byte("initial"))
		if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
			t.Fatalf("seed %s: %v", name, aerr)
		}
		w.syncFunctionCodeForEvent(ctx, fn, uint64(i+1))
		if len(w.state) != 1 {
			t.Fatalf("state slots after sync %d = %d, want 1", i, len(w.state))
		}

		// When: DeleteFunction successfully removes the function.
		req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/"+name, nil)
		req = withFunctionNameParam(req, name)
		rec := httptest.NewRecorder()
		h.DeleteFunction(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete %s status = %d: %s", name, rec.Code, rec.Body.String())
		}

		// Then: no state accumulates across unique create/sync/delete churn.
		if len(w.state) != 0 {
			t.Fatalf("state slots after deleting %s = %#v", name, w.state)
		}
	}
}

func TestS3SyncWatcher_lateDeletedEventCannotRepopulateRecreationState(t *testing.T) {
	// Given: an old-generation event is blocked in S3 after that generation
	// already established an ordering slot.
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	var calls int
	var callsMu sync.Mutex
	fetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			close(oldStarted)
			<-releaseOld
			return []byte("late-deleted-code"), nil
		}
		return []byte("recreated-code"), nil
	}
	h, _ := lifecycleTestHandler(t)
	w := newS3SyncWatcher(h.ls, fetch, zap.NewNop(), h.clk)
	h.setS3SyncWatcher(w)
	original := &Function{
		Name: "late-recreate", ARN: "arn:aws:lambda:us-east-1:000000000000:function:late-recreate",
		CreationID: "deleted-creation", CodeS3Bucket: "bucket", CodeS3Key: "fn.zip", RevisionId: "old-revision",
	}
	original.setCode([]byte("old-code"))
	if aerr := h.ls.putFunction(context.Background(), original); aerr != nil {
		t.Fatalf("seed original: %v", aerr)
	}
	w.state[original.ARN] = s3SyncState{creationID: original.CreationID, appliedRevision: 1}
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		w.syncFunctionCodeForEvent(context.Background(), original, 2)
	}()
	<-oldStarted

	// When: DeleteFunction cleans the old slot and the name is recreated and
	// refreshed before the old fetch completes.
	req := httptest.NewRequest(http.MethodDelete, "/2015-03-31/functions/"+original.Name, nil)
	req = withFunctionNameParam(req, original.Name)
	rec := httptest.NewRecorder()
	h.DeleteFunction(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(w.state) != 0 {
		t.Fatalf("state after delete = %#v, want empty", w.state)
	}
	recreated := *original
	recreated.CreationID = "recreated-creation"
	recreated.RevisionId = "recreated-revision"
	recreated.setCode([]byte("recreated-initial"))
	if aerr := h.ls.putFunction(context.Background(), &recreated); aerr != nil {
		t.Fatalf("seed recreation: %v", aerr)
	}
	w.syncFunctionCodeForEvent(context.Background(), &recreated, 3)
	close(releaseOld)
	<-oldDone

	// Then: the old event neither mutates the recreation nor repopulates its
	// deleted generation's state; only the current creation occupies the ARN slot.
	got, aerr := h.ls.getFunction(context.Background(), recreated.Name)
	if aerr != nil {
		t.Fatalf("get recreation: %v", aerr)
	}
	if aerr := h.ls.loadFunctionCode(context.Background(), got); aerr != nil {
		t.Fatalf("load recreated code: %v", aerr)
	}
	if string(got.CodeZip) != "recreated-code" {
		t.Fatalf("CodeZip = %q, want recreated code", got.CodeZip)
	}
	state, ok := w.state[recreated.ARN]
	if len(w.state) != 1 || !ok || state.creationID != recreated.CreationID || state.appliedRevision != 3 {
		t.Fatalf("state after late event = %#v, want only recreated revision", w.state)
	}
}

func TestS3SyncWatcher_staleRetirementCannotReverseNewestPoolExpectation(t *testing.T) {
	// Given: the older event commits, then pauses immediately before retirement.
	olderStarted := make(chan struct{})
	newerStarted := make(chan struct{})
	releaseOlderFetch := make(chan struct{})
	releaseNewerFetch := make(chan struct{})
	releaseOlderRetire := make(chan struct{})
	olderRetireReady := make(chan struct{})
	newerRetireDone := make(chan struct{})
	olderRetireApplied := make(chan struct{}, 1)
	var calls int
	var callsMu sync.Mutex
	fetch := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			close(olderStarted)
			<-releaseOlderFetch
			return []byte("older-retirement-code"), nil
		}
		close(newerStarted)
		<-releaseNewerFetch
		return []byte("newest-retirement-code"), nil
	}
	h, pool := lifecycleTestHandler(t)
	w := newS3SyncWatcher(h.ls, fetch, zap.NewNop(), h.clk)
	olderHash := codeHashOf([]byte("older-retirement-code"))
	newerHash := codeHashOf([]byte("newest-retirement-code"))
	w.beforeRetire = func(fn *Function) {
		if fn.CodeHash == olderHash {
			close(olderRetireReady)
			<-releaseOlderRetire
		}
	}
	w.retire = func(ctx context.Context, fn *Function, previousIdentity string) {
		h.retireExecutionEnvironment(ctx, fn, previousIdentity)
		switch fn.CodeHash {
		case olderHash:
			olderRetireApplied <- struct{}{}
		case newerHash:
			close(newerRetireDone)
		}
	}
	seeded := seedFunction(t, h.ls, "retirement-order-fn", "bucket", "fn.zip")
	seeded.CreationID = "retirement-creation"
	seeded.setCode([]byte("initial-code"))
	if aerr := h.ls.putFunction(context.Background(), seeded); aerr != nil {
		t.Fatalf("seed function: %v", aerr)
	}

	olderDone := make(chan struct{})
	newerDone := make(chan struct{})
	go func() {
		defer close(olderDone)
		w.syncFunctionCodeForEvent(context.Background(), seeded, 10)
	}()
	<-olderStarted
	go func() {
		defer close(newerDone)
		w.syncFunctionCodeForEvent(context.Background(), seeded, 11)
	}()
	<-newerStarted
	close(releaseOlderFetch)
	<-olderRetireReady

	// When: the newer event commits and retires while the older callback is
	// paused, then the stale older callback resumes.
	close(releaseNewerFetch)
	<-newerRetireDone
	close(releaseOlderRetire)
	<-olderDone
	<-newerDone

	// Then: stale retirement is discarded and the pool's expected identity is
	// still the newest function generation.
	select {
	case <-olderRetireApplied:
		t.Fatal("stale older retirement callback was applied")
	default:
	}
	got, aerr := h.ls.getFunction(context.Background(), seeded.Name)
	if aerr != nil {
		t.Fatalf("get function: %v", aerr)
	}
	pool.mu.Lock()
	expected := pool.expected[seeded.Name]
	pool.mu.Unlock()
	if want := functionInstanceIdentity(got); expected != want {
		t.Fatalf("pool expected identity = %q, want newest %q", expected, want)
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
	fetchSpy := func(_ context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
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
	fetch := func(ctx context.Context, _, _, _ string) ([]byte, *protocol.AWSError) {
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

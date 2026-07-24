package cloudformation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// newTestCFNStore returns a cfnStore backed by a fresh MemoryStore and a
// stopped mock clock, so uniqueSuffix's timestamp component never advances
// on its own — any uniqueness the tests observe comes from the counter,
// which is the worst case for the "same nanosecond" scenario the row-per-
// event key scheme has to handle.
func newTestCFNStore() (*cfnStore, *clock.Mock) {
	mock := clock.NewMock()
	return newCFNStore(state.NewMemoryStore(), "us-east-1", mock), mock
}

func testEvent(id string) StackEvent {
	return StackEvent{
		EventID:           id,
		StackID:           "arn:aws:cloudformation:us-east-1:000000000000:stack/mystack/abc",
		StackName:         "mystack",
		LogicalResourceID: "mystack",
		ResourceType:      "AWS::CloudFormation::Stack",
		ResourceStatus:    "CREATE_IN_PROGRESS",
		Timestamp:         time.Unix(0, 0).UTC(),
	}
}

// ---- appendStackEvent / getStackEvents — happy path ------------------------

func TestAppendStackEvent_singleAppend_isReadableAfterward(t *testing.T) {
	// Given: an empty store
	st, _ := newTestCFNStore()
	ctx := context.Background()

	// When: a single event is appended
	if err := st.appendStackEvent(ctx, "mystack", testEvent("evt-1")); err != nil {
		t.Fatalf("appendStackEvent: %v", err)
	}

	// Then: getStackEvents returns exactly that event
	evts, err := st.getStackEvents(ctx, "mystack")
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}
	if len(evts) != 1 || evts[0].EventID != "evt-1" {
		t.Fatalf("expected [evt-1], got %+v", evts)
	}
}

func TestGetStackEvents_noEvents_returnsNilNotError(t *testing.T) {
	// Given: a store with no events for the stack
	st, _ := newTestCFNStore()
	ctx := context.Background()

	// When: getStackEvents is called
	evts, err := st.getStackEvents(ctx, "no-such-stack")

	// Then: no error, no events
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}
	if len(evts) != 0 {
		t.Fatalf("expected no events, got %+v", evts)
	}
}

// ---- Ordering — matches the pre-existing contract ---------------------------
//
// The old blob implementation appended to a JSON array and returned it as-is
// (oldest first); DescribeStackEvents (handler.go / typed_logic.go) reverses
// that slice to produce AWS's newest-first wire order. The row-per-event
// store must preserve the same oldest-first contract so the handlers need no
// changes.

func TestGetStackEvents_ordering_oldestFirstMatchesAppendOrder(t *testing.T) {
	// Given: events appended in a known sequence
	st, _ := newTestCFNStore()
	ctx := context.Background()
	ids := []string{"evt-1", "evt-2", "evt-3", "evt-4", "evt-5"}
	for _, id := range ids {
		if err := st.appendStackEvent(ctx, "mystack", testEvent(id)); err != nil {
			t.Fatalf("appendStackEvent(%s): %v", id, err)
		}
	}

	// When: getStackEvents is called
	evts, err := st.getStackEvents(ctx, "mystack")
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}

	// Then: events come back oldest-first, in append order
	if len(evts) != len(ids) {
		t.Fatalf("expected %d events, got %d: %+v", len(ids), len(evts), evts)
	}
	for i, id := range ids {
		if evts[i].EventID != id {
			t.Fatalf("event %d: expected %s, got %s (full order: %v)", i, id, evts[i].EventID, eventIDs(evts))
		}
	}
}

func eventIDs(evts []StackEvent) []string {
	ids := make([]string, len(evts))
	for i, e := range evts {
		ids[i] = e.EventID
	}
	return ids
}

// ---- Concurrent appends — the bug this change fixes -------------------------

func TestAppendStackEvent_concurrentAppends_allEventsPresentNoneLost(t *testing.T) {
	// Given: a store and N concurrent appenders targeting the same stack,
	// all racing under a clock frozen at the same instant so every event's
	// timestamp component is identical — uniqueness must come entirely from
	// the counter in uniqueSuffix.
	st, _ := newTestCFNStore()
	ctx := context.Background()
	const n = 100

	// When: N goroutines each append one event concurrently
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := st.appendStackEvent(ctx, "mystack", testEvent(fmt.Sprintf("evt-%d", i))); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("appendStackEvent: %v", err)
	}

	// Then: all N events are present — the old read-modify-write blob could
	// lose events here (see TestZZBaseline in the fix's history); per-row
	// Sets have no shared state to race on.
	evts, err := st.getStackEvents(ctx, "mystack")
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}
	if len(evts) != n {
		t.Fatalf("expected %d events, got %d — events were lost to a race", n, len(evts))
	}
	seen := make(map[string]bool, n)
	for _, e := range evts {
		if seen[e.EventID] {
			t.Fatalf("duplicate event id %s", e.EventID)
		}
		seen[e.EventID] = true
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("evt-%d", i)
		if !seen[id] {
			t.Errorf("missing event %s", id)
		}
	}
}

// ---- Legacy blob compatibility ----------------------------------------------

func TestGetStackEvents_legacyBlob_readAndOpportunisticallyConverted(t *testing.T) {
	// Given: a stack's events stored in the pre-migration single-blob format
	// (as if written by the old appendStackEvent, or carried over from an
	// older Overcast version)
	st, _ := newTestCFNStore()
	ctx := context.Background()
	legacy := []StackEvent{testEvent("evt-1"), testEvent("evt-2"), testEvent("evt-3")}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy events: %v", err)
	}
	blobKey := serviceutil.RegionKey("us-east-1", "mystack")
	if err := st.s.Set(ctx, nsEvents, blobKey, string(raw)); err != nil {
		t.Fatalf("seed legacy blob: %v", err)
	}

	// When: getStackEvents is called
	evts, err := st.getStackEvents(ctx, "mystack")
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}

	// Then: the legacy events are returned, in their original order
	if len(evts) != 3 {
		t.Fatalf("expected 3 legacy events, got %d: %+v", len(evts), evts)
	}
	for i, id := range []string{"evt-1", "evt-2", "evt-3"} {
		if evts[i].EventID != id {
			t.Fatalf("event %d: expected %s, got %s", i, id, evts[i].EventID)
		}
	}

	// And: the data has been converted — the old blob key is gone, and the
	// events now live as individual rows under the new prefix
	if _, found, err := st.s.Get(ctx, nsEvents, blobKey); err != nil {
		t.Fatalf("check blob key: %v", err)
	} else if found {
		t.Fatalf("expected legacy blob key %q to be deleted after conversion", blobKey)
	}
	rows, err := st.s.Scan(ctx, nsEvents, stackEventsPrefix("us-east-1", "mystack"))
	if err != nil {
		t.Fatalf("scan converted rows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 converted per-event rows, got %d", len(rows))
	}

	// And: a second read no longer needs the legacy path and still returns
	// the same events in the same order (the fast path must agree with the
	// legacy path it replaced).
	evts2, err := st.getStackEvents(ctx, "mystack")
	if err != nil {
		t.Fatalf("getStackEvents (post-conversion): %v", err)
	}
	if len(evts2) != 3 || evts2[0].EventID != "evt-1" || evts2[2].EventID != "evt-3" {
		t.Fatalf("post-conversion read mismatch: %+v", evts2)
	}
}

func TestGetStackEvents_corruptLegacyBlob_isolatedNotFatal(t *testing.T) {
	// Given: an undecodable value under the legacy blob key (simulating a
	// poisoned/corrupt persisted record)
	st, _ := newTestCFNStore()
	ctx := context.Background()
	blobKey := serviceutil.RegionKey("us-east-1", "mystack")
	if err := st.s.Set(ctx, nsEvents, blobKey, "not valid json"); err != nil {
		t.Fatalf("seed corrupt blob: %v", err)
	}

	// When: getStackEvents is called
	evts, err := st.getStackEvents(ctx, "mystack")

	// Then: the call does not fail — the corrupt record is isolated, not
	// propagated as an error that would take down DescribeStackEvents
	if err != nil {
		t.Fatalf("expected corrupt legacy blob to be isolated, got error: %v", err)
	}
	if len(evts) != 0 {
		t.Fatalf("expected no events from an undecodable blob, got %+v", evts)
	}

	// And: since we couldn't read it, the record is left in place rather
	// than being destroyed
	if _, found, err := st.s.Get(ctx, nsEvents, blobKey); err != nil {
		t.Fatalf("check blob key: %v", err)
	} else if !found {
		t.Fatalf("expected corrupt legacy blob to remain untouched")
	}
}

func TestGetStackEvents_corruptRow_skippedNotFatal(t *testing.T) {
	// Given: one healthy event row and one undecodable row under the same
	// stack's prefix
	st, _ := newTestCFNStore()
	ctx := context.Background()
	if err := st.appendStackEvent(ctx, "mystack", testEvent("evt-1")); err != nil {
		t.Fatalf("appendStackEvent: %v", err)
	}
	badKey := stackEventsPrefix("us-east-1", "mystack") + "zzz-corrupt"
	if err := st.s.Set(ctx, nsEvents, badKey, "not valid json"); err != nil {
		t.Fatalf("seed corrupt row: %v", err)
	}

	// When: getStackEvents is called
	evts, err := st.getStackEvents(ctx, "mystack")

	// Then: the healthy event is returned and the corrupt row is skipped,
	// not surfaced as an error
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}
	if len(evts) != 1 || evts[0].EventID != "evt-1" {
		t.Fatalf("expected only the healthy event, got %+v", evts)
	}
}

// ---- deleteStackEvents -------------------------------------------------------

func TestDeleteStackEvents_removesAllEventsForThatStackOnly(t *testing.T) {
	// Given: two stacks, each with events, in the same region
	st, _ := newTestCFNStore()
	ctx := context.Background()
	for _, id := range []string{"a-1", "a-2", "a-3"} {
		if err := st.appendStackEvent(ctx, "stack-a", testEvent(id)); err != nil {
			t.Fatalf("appendStackEvent(stack-a): %v", err)
		}
	}
	for _, id := range []string{"b-1", "b-2"} {
		if err := st.appendStackEvent(ctx, "stack-b", testEvent(id)); err != nil {
			t.Fatalf("appendStackEvent(stack-b): %v", err)
		}
	}

	// When: stack-a's events are deleted
	if err := st.deleteStackEvents(ctx, "stack-a"); err != nil {
		t.Fatalf("deleteStackEvents: %v", err)
	}

	// Then: stack-a has no events left
	evtsA, err := st.getStackEvents(ctx, "stack-a")
	if err != nil {
		t.Fatalf("getStackEvents(stack-a): %v", err)
	}
	if len(evtsA) != 0 {
		t.Fatalf("expected stack-a events to be gone, got %+v", evtsA)
	}

	// And: stack-b's events are untouched
	evtsB, err := st.getStackEvents(ctx, "stack-b")
	if err != nil {
		t.Fatalf("getStackEvents(stack-b): %v", err)
	}
	if len(evtsB) != 2 {
		t.Fatalf("expected stack-b to keep its 2 events, got %+v", evtsB)
	}
}

func TestDeleteStackEvents_alsoRemovesLegacyBlobIfPresent(t *testing.T) {
	// Given: a stack still on the legacy blob layout (never read, so never
	// opportunistically converted)
	st, _ := newTestCFNStore()
	ctx := context.Background()
	blobKey := serviceutil.RegionKey("us-east-1", "mystack")
	raw, _ := json.Marshal([]StackEvent{testEvent("evt-1")})
	if err := st.s.Set(ctx, nsEvents, blobKey, string(raw)); err != nil {
		t.Fatalf("seed legacy blob: %v", err)
	}

	// When: the stack's events are deleted
	if err := st.deleteStackEvents(ctx, "mystack"); err != nil {
		t.Fatalf("deleteStackEvents: %v", err)
	}

	// Then: the legacy blob key is gone too
	if _, found, err := st.s.Get(ctx, nsEvents, blobKey); err != nil {
		t.Fatalf("check blob key: %v", err)
	} else if found {
		t.Fatalf("expected legacy blob key to be deleted")
	}
}

func TestDeleteStackEvents_noEvents_isNotAnError(t *testing.T) {
	// Given: a store with no events at all
	st, _ := newTestCFNStore()
	ctx := context.Background()

	// When: deleteStackEvents is called for a stack that never had events
	err := st.deleteStackEvents(ctx, "never-existed")

	// Then: it succeeds silently (Delete on a missing key is a no-op)
	if err != nil {
		t.Fatalf("deleteStackEvents: %v", err)
	}
}

// ---- uniqueSuffix -------------------------------------------------------------

func TestUniqueSuffix_sameFrozenClock_stillProducesDistinctSortedValues(t *testing.T) {
	// Given: a clock that never advances (worst case for uniqueness)
	mock := clock.NewMock()

	// When: many suffixes are generated back-to-back
	const n = 1000
	suffixes := make([]string, n)
	for i := range suffixes {
		suffixes[i] = uniqueSuffix(mock)
	}

	// Then: every suffix is unique
	seen := make(map[string]bool, n)
	for _, s := range suffixes {
		if seen[s] {
			t.Fatalf("duplicate suffix %q", s)
		}
		seen[s] = true
	}

	// And: suffixes sort lexicographically in generation order (so keys
	// built from them preserve chronological/append order under Scan)
	for i := 1; i < n; i++ {
		if suffixes[i-1] >= suffixes[i] {
			t.Fatalf("suffixes not strictly increasing at index %d: %q >= %q", i, suffixes[i-1], suffixes[i])
		}
	}
}

// ---- Error paths — failingStore fake ----------------------------------------
//
// failingStore wraps a state.Store and can be configured to force specific
// methods to fail, so store.go's error-handling branches (which never fire
// against a healthy MemoryStore) are exercised. It embeds the state.Store
// *interface*, not a concrete type, so it never promotes DeletePrefix even
// when the wrapped store implements it — this also makes it double as the
// "store without ranged deletes" fixture for deleteStackEvents' List+Delete
// fallback path.

var errBoom = errors.New("boom: simulated store failure")

type failingStore struct {
	state.Store
	failGet    bool
	failSet    bool
	failScan   bool
	failList   bool
	failDelete bool
}

func (f *failingStore) Get(ctx context.Context, ns, key string) (string, bool, error) {
	if f.failGet {
		return "", false, errBoom
	}
	return f.Store.Get(ctx, ns, key)
}

func (f *failingStore) Set(ctx context.Context, ns, key, value string) error {
	if f.failSet {
		return errBoom
	}
	return f.Store.Set(ctx, ns, key, value)
}

func (f *failingStore) Scan(ctx context.Context, ns, prefix string) ([]state.KV, error) {
	if f.failScan {
		return nil, errBoom
	}
	return f.Store.Scan(ctx, ns, prefix)
}

func (f *failingStore) List(ctx context.Context, ns, prefix string) ([]string, error) {
	if f.failList {
		return nil, errBoom
	}
	return f.Store.List(ctx, ns, prefix)
}

func (f *failingStore) Delete(ctx context.Context, ns, key string) error {
	if f.failDelete {
		return errBoom
	}
	return f.Store.Delete(ctx, ns, key)
}

// failingPrefixDeleteStore additionally implements state.PrefixDeleter (with
// its own failure switch), so deleteStackEvents' ranged-delete branch can be
// exercised for both the success and the error case, distinct from the
// unconditional trailing legacy-blob Delete which failingStore.failDelete
// already covers.
type failingPrefixDeleteStore struct {
	failingStore
	failDeletePrefix bool
}

func (f *failingPrefixDeleteStore) DeletePrefix(ctx context.Context, ns, prefix string) error {
	if f.failDeletePrefix {
		return errBoom
	}
	if deleter, ok := f.failingStore.Store.(state.PrefixDeleter); ok {
		return deleter.DeletePrefix(ctx, ns, prefix)
	}
	return nil
}

func TestGetStackEvents_scanError_returnsError(t *testing.T) {
	// Given: a store whose Scan always fails
	fs := &failingStore{Store: state.NewMemoryStore(), failScan: true}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	ctx := context.Background()

	// When: getStackEvents is called
	_, err := st.getStackEvents(ctx, "mystack")

	// Then: the Scan failure surfaces as an error
	if err == nil {
		t.Fatal("expected an error when Scan fails")
	}
}

func TestGetStackEvents_legacyGetError_returnsError(t *testing.T) {
	// Given: an empty store (so the prefix Scan is empty and getStackEvents
	// falls back to checking the legacy blob key) whose Get always fails
	fs := &failingStore{Store: state.NewMemoryStore(), failGet: true}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	ctx := context.Background()

	// When: getStackEvents is called
	_, err := st.getStackEvents(ctx, "mystack")

	// Then: the legacy-lookup Get failure surfaces as an error
	if err == nil {
		t.Fatal("expected an error when the legacy blob Get fails")
	}
}

func TestAppendStackEvent_setError_returnsError(t *testing.T) {
	// Given: a store whose Set always fails
	fs := &failingStore{Store: state.NewMemoryStore(), failSet: true}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	ctx := context.Background()

	// When: appendStackEvent is called
	err := st.appendStackEvent(ctx, "mystack", testEvent("evt-1"))

	// Then: the Set failure surfaces as an error
	if err == nil {
		t.Fatal("expected an error when Set fails")
	}
}

func TestGetStackEvents_legacyConversionSetFails_blobLeftInPlaceEventsStillReturned(t *testing.T) {
	// Given: a legacy blob, and a store whose Set always fails (so the
	// opportunistic per-row conversion cannot write anything)
	mem := state.NewMemoryStore()
	ctx := context.Background()
	blobKey := serviceutil.RegionKey("us-east-1", "mystack")
	raw, _ := json.Marshal([]StackEvent{testEvent("evt-1"), testEvent("evt-2")})
	if err := mem.Set(ctx, nsEvents, blobKey, string(raw)); err != nil {
		t.Fatalf("seed legacy blob: %v", err)
	}
	fs := &failingStore{Store: mem, failSet: true}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())

	// When: getStackEvents reads the legacy blob
	evts, err := st.getStackEvents(ctx, "mystack")

	// Then: it still succeeds and returns the events decoded from the blob —
	// conversion is best-effort and its failure must not fail the read
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}
	if len(evts) != 2 {
		t.Fatalf("expected 2 events despite failed conversion, got %+v", evts)
	}

	// And: the blob is left in place, not destroyed, since the conversion
	// that would have replaced it never completed
	if _, found, err := mem.Get(ctx, nsEvents, blobKey); err != nil {
		t.Fatalf("check blob key: %v", err)
	} else if !found {
		t.Fatalf("expected the legacy blob to remain after a failed conversion")
	}
}

func TestDeleteStackEvents_fallbackPath_noPrefixDeleter(t *testing.T) {
	// Given: a store that does not implement state.PrefixDeleter
	fs := &failingStore{Store: state.NewMemoryStore()}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	ctx := context.Background()
	for _, id := range []string{"evt-1", "evt-2"} {
		if err := st.appendStackEvent(ctx, "mystack", testEvent(id)); err != nil {
			t.Fatalf("appendStackEvent: %v", err)
		}
	}

	// When: deleteStackEvents is called
	if err := st.deleteStackEvents(ctx, "mystack"); err != nil {
		t.Fatalf("deleteStackEvents: %v", err)
	}

	// Then: the List+Delete fallback still removes every event
	evts, err := st.getStackEvents(ctx, "mystack")
	if err != nil {
		t.Fatalf("getStackEvents: %v", err)
	}
	if len(evts) != 0 {
		t.Fatalf("expected events deleted via the fallback path, got %+v", evts)
	}
}

func TestDeleteStackEvents_fallbackListError_returnsError(t *testing.T) {
	// Given: a store without PrefixDeleter whose List always fails
	fs := &failingStore{Store: state.NewMemoryStore(), failList: true}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	ctx := context.Background()

	// When: deleteStackEvents is called
	err := st.deleteStackEvents(ctx, "mystack")

	// Then: the List failure surfaces as an error
	if err == nil {
		t.Fatal("expected an error when List fails in the fallback path")
	}
}

func TestDeleteStackEvents_fallbackDeleteError_returnsError(t *testing.T) {
	// Given: a store without PrefixDeleter that has one event row, and whose
	// Delete always fails
	mem := state.NewMemoryStore()
	ctx := context.Background()
	fs := &failingStore{Store: mem}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	if err := st.appendStackEvent(ctx, "mystack", testEvent("evt-1")); err != nil {
		t.Fatalf("appendStackEvent: %v", err)
	}
	fs.failDelete = true

	// When: deleteStackEvents is called
	err := st.deleteStackEvents(ctx, "mystack")

	// Then: the per-key Delete failure surfaces as an error
	if err == nil {
		t.Fatal("expected an error when Delete fails in the fallback path")
	}
}

func TestDeleteStackEvents_prefixDeleterError_returnsError(t *testing.T) {
	// Given: a store that implements PrefixDeleter but whose DeletePrefix fails
	fs := &failingPrefixDeleteStore{
		failingStore:     failingStore{Store: state.NewMemoryStore()},
		failDeletePrefix: true,
	}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	ctx := context.Background()

	// When: deleteStackEvents is called
	err := st.deleteStackEvents(ctx, "mystack")

	// Then: the DeletePrefix failure surfaces as an error
	if err == nil {
		t.Fatal("expected an error when DeletePrefix fails")
	}
}

func TestDeleteStackEvents_finalLegacyBlobDeleteError_returnsError(t *testing.T) {
	// Given: a store whose ranged DeletePrefix succeeds but whose plain
	// Delete (used for the unconditional trailing legacy-blob cleanup) fails
	fs := &failingPrefixDeleteStore{failingStore: failingStore{Store: state.NewMemoryStore()}}
	st := newCFNStore(fs, "us-east-1", clock.NewMock())
	ctx := context.Background()
	if err := st.appendStackEvent(ctx, "mystack", testEvent("evt-1")); err != nil {
		t.Fatalf("appendStackEvent: %v", err)
	}
	fs.failDelete = true

	// When: deleteStackEvents is called
	err := st.deleteStackEvents(ctx, "mystack")

	// Then: the trailing legacy-blob Delete failure surfaces as an error
	if err == nil {
		t.Fatal("expected an error when the trailing legacy-blob Delete fails")
	}
}

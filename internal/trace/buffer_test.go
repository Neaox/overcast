package trace

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// traceSpec is the handful of fields the ring buffer and its filters read.
// The buffer holds live recorders, so tests build one the way the middleware
// does rather than assembling an Entry by hand.
type traceSpec struct {
	RequestID string
	Timestamp time.Time
	Method    string
	Path      string
	Service   string
	Status    int
}

func newSpecRecorder(s traceSpec) *Recorder {
	rec := NewRecorder(s.RequestID, s.Timestamp, s.Method, s.Path, "localhost", "", http.Header{})
	if s.Service != "" {
		rec.SetServiceInfo(s.Service, "", "")
	}
	if s.Status != 0 {
		rec.SetResponse(http.Header{}, nil, s.Status, 1024, false)
	}
	return rec
}

// addTrace registers a trace described by spec and returns its recorder.
func addTrace(b *Buffer, s traceSpec) *Recorder {
	rec := newSpecRecorder(s)
	b.Add(rec)
	return rec
}

// requestIDs is the assertion shorthand for "which traces came back".
func requestIDs(summaries []Summary) []string {
	ids := make([]string, len(summaries))
	for i, s := range summaries {
		ids[i] = s.RequestID
	}
	return ids
}

func TestBuffer_addAndGet(t *testing.T) {
	// Given: an empty buffer
	buf := NewBuffer(10)

	// When: a trace is registered
	addTrace(buf, traceSpec{RequestID: "req-1", Timestamp: time.Now(), Method: "POST", Path: "/", Service: "sqs"})

	// Then: it is retrievable by request ID
	if buf.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", buf.Len())
	}
	got, ok := buf.Get("req-1")
	if !ok {
		t.Fatal("entry not found")
	}
	if got.RequestID != "req-1" {
		t.Errorf("expected req-1, got %s", got.RequestID)
	}
	if got.Service != "sqs" {
		t.Errorf("expected sqs, got %s", got.Service)
	}
}

// The buffer holds the live recorder, so writes made after registration — the
// case that matters, because CloudFormation provisions stacks on a goroutine
// that outlives the request — need no second store to become visible.
func TestBuffer_writesAfterAddAreVisible(t *testing.T) {
	// Given: a registered trace with nothing recorded on it yet
	buf := NewBuffer(10)
	rec := addTrace(buf, traceSpec{RequestID: "req-1", Timestamp: time.Now(), Method: "POST", Path: "/"})

	// When: the response and a late hop are recorded on the recorder
	rec.SetResponse(http.Header{}, []byte("ok"), http.StatusOK, 1024, false)
	rec.SetDuration(3 * time.Second)
	rec.AddHop(Hop{Service: "sqs", Operation: "CreateQueue", RequestID: "hop-req", ResponseStatus: 200})

	// Then: the buffer reflects all of it, with no further Add
	got, ok := buf.Get("req-1")
	if !ok {
		t.Fatal("entry not found")
	}
	if got.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", got.StatusCode)
	}
	if got.Duration != 3*time.Second {
		t.Errorf("Duration = %s, want 3s", got.Duration)
	}
	if len(got.Hops) != 1 {
		t.Fatalf("hops = %d, want 1", len(got.Hops))
	}
	summaries, _ := buf.ListSummaries(ListFilter{})
	if len(summaries) != 1 || summaries[0].HopCount != 1 || summaries[0].StatusCode != http.StatusOK {
		t.Errorf("summary = %+v, want 1 hop and status 200", summaries)
	}
}

func TestBuffer_reAddReplaces(t *testing.T) {
	// Given: a registered trace
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "req-1", Service: "sqs"})

	// When: the same request ID is registered again
	addTrace(buf, traceSpec{RequestID: "req-1", Service: "lambda"})

	// Then: it replaces the first rather than occupying a second slot
	if buf.Len() != 1 {
		t.Fatalf("expected 1 entry after re-add, got %d", buf.Len())
	}
	got, _ := buf.Get("req-1")
	if got.Service != "lambda" {
		t.Errorf("expected lambda after re-add, got %s", got.Service)
	}
}

func TestBuffer_eviction(t *testing.T) {
	// Given/When: more traces than the buffer holds
	buf := NewBuffer(3)
	for i := 0; i < 5; i++ {
		addTrace(buf, traceSpec{
			RequestID: "req-" + strconv.Itoa(i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	// Then: the oldest are evicted and the newest survive
	if buf.Len() != 3 {
		t.Fatalf("expected 3 entries after eviction, got %d", buf.Len())
	}
	if _, ok := buf.Get("req-0"); ok {
		t.Error("req-0 should have been evicted")
	}
	if _, ok := buf.Get("req-1"); ok {
		t.Error("req-1 should have been evicted")
	}
	if _, ok := buf.Get("req-4"); !ok {
		t.Error("req-4 should still be present")
	}
}

// countInternal reports how many retained traces are internal, so tests can
// check the cap without reaching into ring indices.
func countInternal(b *Buffer) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	b.eachRecorderLocked(func(rec *Recorder) {
		if rec.internal {
			n++
		}
	})
	return n
}

func TestBuffer_internalCapHoldsAfterWrap(t *testing.T) {
	// Given a full buffer of user entries, When internal poll traces keep
	// arriving well past the internal ring's capacity, Then the cap holds and —
	// now that the two populations no longer share a ring — not one user entry
	// is lost, rather than the oldest few being spent on the quota.
	buf := NewBuffer(10) // internal ring holds 2
	for i := 0; i < 10; i++ {
		addTrace(buf, traceSpec{
			RequestID: "user-" + strconv.Itoa(i),
			Path:      "/",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	// Simulate idle polling: many internal traces against a full buffer.
	for i := 0; i < 20; i++ {
		addTrace(buf, traceSpec{
			RequestID: "int-" + strconv.Itoa(i),
			Path:      "/_overcast/health",
			Timestamp: time.Now().Add(time.Duration(10+i) * time.Second),
		})
	}

	if got := countInternal(buf); got > 2 {
		t.Errorf("internal entries exceeded the ring: got %d, want <= 2", got)
	}
	for i := 0; i < 10; i++ {
		if _, ok := buf.Get("user-" + strconv.Itoa(i)); !ok {
			t.Errorf("user-%d was evicted by internal polling", i)
		}
	}
}

func TestBuffer_internalQuotaRotatesWhenNotFull(t *testing.T) {
	// Given a never-filled buffer whose internal quota is full, When a new
	// internal trace arrives, Then the oldest internal entry is recycled
	// (ring semantics within the quota) instead of dropping the new trace.
	buf := NewBuffer(10) // maxInternal = 2
	addTrace(buf, traceSpec{RequestID: "int-0", Path: "/_overcast/health", Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "int-1", Path: "/_overcast/health", Timestamp: time.Now().Add(time.Second)})
	addTrace(buf, traceSpec{RequestID: "int-2", Path: "/_overcast/health", Timestamp: time.Now().Add(2 * time.Second)})

	if _, ok := buf.Get("int-2"); !ok {
		t.Error("newest internal trace was dropped; expected oldest to be recycled")
	}
	if _, ok := buf.Get("int-0"); ok {
		t.Error("oldest internal trace should have been recycled")
	}
	if _, ok := buf.Get("int-1"); !ok {
		t.Error("int-1 should still be present")
	}
	if got := countInternal(buf); got != 2 {
		t.Errorf("expected 2 internal entries, got %d", got)
	}
}

// TestBuffer_fullEvictionFairness was removed with the mechanism it guarded.
// It covered a user entry inheriting a reclaimed internal slot near the head
// and being evicted almost immediately as a result — a hazard created by
// replaceLocked's slot shuffling, which separate rings delete outright. The
// property it protected, eviction being oldest-first, is covered by
// TestBuffer_evictionIsOldestFirst in rings_buffer_test.go.

func TestBuffer_getMissing(t *testing.T) {
	buf := NewBuffer(10)
	if _, ok := buf.Get("nonexistent"); ok {
		t.Error("expected false for missing key")
	}
}

func TestBuffer_listFilterService(t *testing.T) {
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "r1", Service: "sqs", Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "r2", Service: "lambda", Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "r3", Service: "sqs", Timestamp: time.Now()})

	entries, _ := buf.ListSummaries(ListFilter{Service: "sqs", Limit: 10})
	if len(entries) != 2 {
		t.Fatalf("expected 2 sqs entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Service != "sqs" {
			t.Errorf("expected sqs, got %s", e.Service)
		}
	}
}

func TestBuffer_listFilterStatus(t *testing.T) {
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "r1", Status: 200, Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "r2", Status: 404, Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "r3", Status: 500, Timestamp: time.Now()})

	entries, _ := buf.ListSummaries(ListFilter{Statuses: []string{"2xx"}, Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry with 2xx, got %d", len(entries))
	}
	if entries[0].RequestID != "r1" {
		t.Errorf("expected r1, got %s", entries[0].RequestID)
	}
}

func TestBuffer_listFilterMethod(t *testing.T) {
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "r1", Method: "GET", Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "r2", Method: "POST", Timestamp: time.Now()})

	entries, _ := buf.ListSummaries(ListFilter{Methods: []string{"post"}, Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 POST entry, got %d", len(entries))
	}
	if entries[0].RequestID != "r2" {
		t.Errorf("method match is case-insensitive: got %s", entries[0].RequestID)
	}
}

func TestBuffer_listFilterPath(t *testing.T) {
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "r1", Path: "/2015-03-31/functions", Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "r2", Path: "/", Timestamp: time.Now()})

	entries, _ := buf.ListSummaries(ListFilter{Path: "functions", Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry matching path, got %d", len(entries))
	}
}

func TestBuffer_listSearch(t *testing.T) {
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "abc-123", Path: "/some/path", Service: "sqs", Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "xyz-789", Path: "/other", Service: "lambda", Timestamp: time.Now()})

	entries, _ := buf.ListSummaries(ListFilter{Search: "abc", Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry matching search, got %d", len(entries))
	}
	if entries[0].RequestID != "abc-123" {
		t.Errorf("expected abc-123, got %s", entries[0].RequestID)
	}
}

// The web UI's "show me all errors" is one query, not two: the trace list is
// server-paginated, so filtering client-side would leave pages sparse.
func TestBuffer_listFilterMultipleStatusClasses(t *testing.T) {
	// Given: traces spanning every status class
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "ok", Status: 200, Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "redirect", Status: 302, Timestamp: time.Now().Add(time.Second)})
	addTrace(buf, traceSpec{RequestID: "client-err", Status: 404, Timestamp: time.Now().Add(2 * time.Second)})
	addTrace(buf, traceSpec{RequestID: "server-err", Status: 500, Timestamp: time.Now().Add(3 * time.Second)})

	// When: both error classes are asked for at once
	entries, _ := buf.ListSummaries(ListFilter{Statuses: []string{"4xx", "5xx"}, Limit: 10})

	// Then: exactly the error traces come back, newest first
	got := requestIDs(entries)
	if len(got) != 2 || got[0] != "server-err" || got[1] != "client-err" {
		t.Fatalf("statuses [4xx 5xx] = %v, want [server-err client-err]", got)
	}
}

func TestBuffer_listFilterMultipleMethods(t *testing.T) {
	// Given: traces using three different methods
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "r1", Method: "GET", Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "r2", Method: "POST", Timestamp: time.Now().Add(time.Second)})
	addTrace(buf, traceSpec{RequestID: "r3", Method: "DELETE", Timestamp: time.Now().Add(2 * time.Second)})

	// When: two of them are asked for at once
	entries, _ := buf.ListSummaries(ListFilter{Methods: []string{"GET", "delete"}, Limit: 10})

	// Then: only those two match, and the match stayed case-insensitive
	got := requestIDs(entries)
	if len(got) != 2 || got[0] != "r3" || got[1] != "r1" {
		t.Fatalf("methods [GET delete] = %v, want [r3 r1]", got)
	}
}

func TestBuffer_listFilterExactStatusCodeAmongClasses(t *testing.T) {
	// Given: two 4xx traces with different codes
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "not-found", Status: 404, Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "conflict", Status: 409, Timestamp: time.Now().Add(time.Second)})
	addTrace(buf, traceSpec{RequestID: "boom", Status: 503, Timestamp: time.Now().Add(2 * time.Second)})

	// When: an exact code is mixed with a class
	entries, _ := buf.ListSummaries(ListFilter{Statuses: []string{"409", "5xx"}, Limit: 10})

	// Then: per-value semantics are unchanged — exact codes and classes both
	// still work, and combine as a match-any set
	got := requestIDs(entries)
	if len(got) != 2 || got[0] != "boom" || got[1] != "conflict" {
		t.Fatalf("statuses [409 5xx] = %v, want [boom conflict]", got)
	}
}

func TestBuffer_listFilterEmptyValuesAreIgnored(t *testing.T) {
	// Given: traces of two statuses and two methods
	buf := NewBuffer(10)
	addTrace(buf, traceSpec{RequestID: "ok", Method: "GET", Status: 200, Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "bad", Method: "POST", Status: 404, Timestamp: time.Now().Add(time.Second)})

	// When: a blank value is submitted alongside a real one
	entries, _ := buf.ListSummaries(ListFilter{Statuses: []string{"", "4xx"}, Methods: []string{"POST", ""}, Limit: 10})

	// Then: the blank is ignored rather than matching everything
	if got := requestIDs(entries); len(got) != 1 || got[0] != "bad" {
		t.Fatalf("filter with a blank value = %v, want [bad]", got)
	}

	// And: a set of nothing but blanks means "no filter", as an absent
	// parameter does
	all, _ := buf.ListSummaries(ListFilter{Statuses: []string{"", ""}, Limit: 10})
	if len(all) != 2 {
		t.Fatalf("all-blank filter returned %d entries, want 2 (no filter)", len(all))
	}
}

func TestSplitFilterValues_repeatedAndCommaSeparated(t *testing.T) {
	// Given/When/Then: repeated params, comma-separated params and a mix of
	// the two all flatten into one match-any set, with blanks dropped
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"absent", nil, nil},
		{"single", []string{"4xx"}, []string{"4xx"}},
		{"repeated", []string{"4xx", "5xx"}, []string{"4xx", "5xx"}},
		{"comma separated", []string{"4xx,5xx"}, []string{"4xx", "5xx"}},
		{"mixed", []string{"2xx", "4xx,5xx"}, []string{"2xx", "4xx", "5xx"}},
		{"blanks dropped", []string{"", "4xx", ",", " 5xx "}, []string{"4xx", "5xx"}},
		{"all blank", []string{"", ","}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitFilterValues(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("SplitFilterValues(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("SplitFilterValues(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// HopsFor answers "which request originated this one?". It is backed by a
// per-trace index rather than a scan of every hop of every trace.
func TestBuffer_listFilterHopsFor(t *testing.T) {
	// Given: two traces, one of which dispatched the hop being looked up
	buf := NewBuffer(10)
	deploy := addTrace(buf, traceSpec{RequestID: "deploy", Path: "/", Timestamp: time.Now()})
	addTrace(buf, traceSpec{RequestID: "unrelated", Path: "/", Timestamp: time.Now().Add(time.Second)})
	deploy.AddHop(Hop{Service: "sqs", RequestID: "child-1", ResponseStatus: 200})
	deploy.AddHop(Hop{Service: "sqs", RequestID: "child-2", ResponseStatus: 200})

	// When: the originating trace of a hop is looked up
	entries, _ := buf.ListSummaries(ListFilter{HopsFor: "child-2", Limit: 10})

	// Then: only the dispatching trace matches
	if got := requestIDs(entries); len(got) != 1 || got[0] != "deploy" {
		t.Fatalf("HopsFor child-2 = %v, want [deploy]", got)
	}
	if entries, _ := buf.ListSummaries(ListFilter{HopsFor: "no-such-hop", Limit: 10}); len(entries) != 0 {
		t.Errorf("HopsFor of an unknown ID matched %d entries, want 0", len(entries))
	}
}

// Listing must not copy bodies: the buffer now retains live traces, and a
// list runs once a second from the UI poll.
func TestBuffer_listSummariesCopiesNoBodies(t *testing.T) {
	// Given: a trace carrying request, response and hop bodies
	buf := NewBuffer(10)
	rec := addTrace(buf, traceSpec{RequestID: "req-1", Method: "POST", Path: "/", Timestamp: time.Now()})
	rec.SetRequestBody([]byte("request payload"), false, -1)
	rec.SetResponse(http.Header{"X-Test": []string{"1"}}, []byte("response payload"), 200, 1024, false)
	rec.AddHop(Hop{Service: "sqs", RequestBody: []byte("hop payload"), ResponseBody: []byte("hop response")})
	rec.AddLog(LogEntry{Level: "info", Message: "hello"})

	// When: the trace is listed
	summaries, _ := buf.ListSummaries(ListFilter{Limit: 10})

	// Then: the summary carries counts, not payloads — Summary has no body,
	// header, hop or log fields at all, so this is enforced structurally as
	// well as by the counts below
	if len(summaries) != 1 {
		t.Fatalf("summaries = %d, want 1", len(summaries))
	}
	s := summaries[0]
	if s.HopCount != 1 || s.LogCount != 1 {
		t.Errorf("HopCount/LogCount = %d/%d, want 1/1", s.HopCount, s.LogCount)
	}
	if s.StatusCode != 200 || s.Method != "POST" {
		t.Errorf("summary = %+v, want status 200 and method POST", s)
	}
}

func TestBuffer_listPagination(t *testing.T) {
	buf := NewBuffer(10)
	for i := 0; i < 5; i++ {
		addTrace(buf, traceSpec{
			RequestID: "req-" + strconv.Itoa(i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	page1, cursor := buf.ListSummaries(ListFilter{Limit: 2})
	if len(page1) != 2 {
		t.Fatalf("expected 2 entries on page 1, got %d", len(page1))
	}
	if cursor == "" {
		t.Fatal("expected non-empty cursor")
	}

	page2, cursor2 := buf.ListSummaries(ListFilter{Limit: 2, After: cursor})
	if len(page2) != 2 {
		t.Fatalf("expected 2 entries on page 2, got %d", len(page2))
	}
	if cursor2 == "" {
		t.Fatal("expected non-empty cursor for page 2")
	}

	page3, cursor3 := buf.ListSummaries(ListFilter{Limit: 2, After: cursor2})
	if len(page3) != 1 {
		t.Fatalf("expected 1 entry on page 3, got %d", len(page3))
	}
	if cursor3 != "" {
		t.Error("expected empty cursor on last page")
	}
}

func TestBuffer_listBeforeHonoursLimit(t *testing.T) {
	// Given entries newer than the cursor, When listing with Before and a
	// Limit, Then at most Limit entries come back (newest first).
	buf := NewBuffer(10)
	for i := 0; i < 6; i++ {
		addTrace(buf, traceSpec{
			RequestID: "req-" + strconv.Itoa(i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	entries, cursor := buf.ListSummaries(ListFilter{Before: "req-0", Limit: 2})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with Limit 2, got %d", len(entries))
	}
	if entries[0].RequestID != "req-5" || entries[1].RequestID != "req-4" {
		t.Errorf("expected newest-first [req-5 req-4], got [%s %s]", entries[0].RequestID, entries[1].RequestID)
	}
	if cursor != "req-5" {
		t.Errorf("expected cursor req-5, got %q", cursor)
	}
}

func TestBuffer_listBeforeEvictedCursorHonoursLimit(t *testing.T) {
	// Given a Before cursor whose entry has been evicted, When listing with a
	// Limit, Then at most Limit entries come back instead of everything.
	buf := NewBuffer(10)
	for i := 0; i < 6; i++ {
		addTrace(buf, traceSpec{
			RequestID: "req-" + strconv.Itoa(i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	entries, _ := buf.ListSummaries(ListFilter{Before: "gone", Limit: 2})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with Limit 2 for evicted cursor, got %d", len(entries))
	}
	if entries[0].RequestID != "req-5" || entries[1].RequestID != "req-4" {
		t.Errorf("expected newest-first [req-5 req-4], got [%s %s]", entries[0].RequestID, entries[1].RequestID)
	}
}

// The buffer holds live recorders, so a deploy writing hops runs concurrently
// with the UI listing and reading them. Under -race this is the test that
// proves the read and write paths do not share unguarded state.
func TestBuffer_concurrentHopsWhileListing(t *testing.T) {
	// Given: several registered traces
	const traces, hops = 4, 200
	buf := NewBuffer(64)
	recs := make([]*Recorder, traces)
	for i := range recs {
		recs[i] = addTrace(buf, traceSpec{
			RequestID: "req-" + strconv.Itoa(i),
			Method:    "POST",
			Path:      "/",
			Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
		})
	}

	// When: every trace records hops while readers list and materialise them
	var wg sync.WaitGroup
	for i, rec := range recs {
		wg.Add(1)
		go func(i int, rec *Recorder) {
			defer wg.Done()
			for h := 0; h < hops; h++ {
				rec.AddHop(Hop{
					Service:        "sqs",
					Operation:      "CreateQueue",
					RequestID:      "hop-" + strconv.Itoa(i) + "-" + strconv.Itoa(h),
					RequestBody:    []byte("body"),
					ResponseStatus: 200,
				})
			}
		}(i, rec)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				summaries, _ := buf.ListSummaries(ListFilter{Statuses: []string{"2xx", "4xx"}, Methods: []string{"POST"}, Limit: 100})
				for _, s := range summaries {
					buf.Get(s.RequestID)
				}
				buf.ListSummaries(ListFilter{HopsFor: "hop-0-0"})
			}
		}()
	}
	wg.Wait()

	// Then: every hop landed
	for i := range recs {
		entry, ok := buf.Get("req-" + strconv.Itoa(i))
		if !ok {
			t.Fatalf("req-%d missing from buffer", i)
		}
		if len(entry.Hops) != hops {
			t.Errorf("req-%d hops = %d, want %d", i, len(entry.Hops), hops)
		}
	}
}

func TestIsInternalPathSeparatesPollingFromClientTraffic(t *testing.T) {
	// The whole /_overcast/debug/* namespace and /_overcast/metrics are polled by the web UI
	// and must be classified internal, matching middleware's
	// isOperationalPollPath, so polling never consumes user-trace budget.
	internal := []string{
		"/_overcast/health",
		"/_overcast/metrics",
		"/_overcast/debug",
		"/_overcast/debug/traces",
		"/_overcast/debug/traces/abc-123",
		"/_overcast/debug/trace/abc-123",
		"/_overcast/debug/traces/count",
		"/_overcast/debug/metrics",
		"/_overcast/debug/state",
		"/_overcast/debug/state/sqs:queues",
		"/_overcast/events",
		"/_overcast/events/request",
		"/_overcast/ses/inbox",
		"/_overcast/ses/inbox/messages",
		"/_overcast/info",
		// The console's own polling, reached through the BFF: the system map
		// asks for the topology graph and the Lambda pages ask for the live
		// container list, both about once a second. #1613 saw them listed on
		// the traces page with "Hide internal" ticked, because neither was
		// here.
		"/_overcast/topology",
		"/_overcast/lambda/instances",
	}
	for _, p := range internal {
		if !isInternalPath(p) {
			t.Errorf("isInternalPath(%q) = false, want true", p)
		}
	}
	// A leading underscore does NOT mean internal, and this half of the test
	// is the one that says so. isInternalPath is an allowlist, not a prefix
	// test, because Overcast serves two different kinds of thing under "/_":
	// its own operational endpoints, and the data plane of the workloads it
	// emulates. The second kind is a real client's request — an SDK, a
	// browser, curl — and belongs in the trace list like any other.
	//
	// It used to survive by accident: the data-plane routes sat on first
	// segments of their own (/_appsync, /_apigateway, /_cognito, /_cloudfront,
	// /_elb, /_lambda/url-invoke) that nobody had added to internalPaths, so
	// the allowlist excluded them without anyone deciding it should.
	//
	// docs/plans/non-canonical-url-namespace.md spent that accident. Phase 5
	// moved every one of them under /_overcast/, so the cases below now sit in
	// the same namespace as /_overcast/health and /_overcast/debug and are
	// distinguished from them by this allowlist alone. That is why these were
	// written down before the move rather than after — they are the reason
	// isInternalPath cannot become a prefix test now that a prefix test would
	// compile and pass.
	//
	// Getting this wrong is quiet, which is why it is worth a test: an
	// AppSync GraphQL call or a Lambda function URL invoke misfiled as
	// internal does not error, it just stops appearing in the trace UI, and
	// the first symptom is someone unable to find a request they know they
	// made.
	user := []string{
		"/",
		"/2015-03-31/functions",
		"/my-bucket/key",
		"/_overcast/debugfoo",
		"/_overcast/init",

		// Data plane: the emulated workload answers these, not Overcast.
		"/_overcast/appsync/apis/abc123/graphql",
		"/_overcast/appsync/apis/abc123/realtime",
		"/_overcast/apigateway/execute-api/abc123/us-east-1/test/hello",
		"/_overcast/lambda/url-invoke/abc123/",
		// Neighbours of /_overcast/lambda/instances that are not it. The
		// allowlist matches exact paths, so adding the instances endpoint must
		// not drag the rest of /_overcast/lambda in with it.
		"/_overcast/lambda/instances/abc123",
		"/_overcast/cloudfront/distributions/E123456789/index.html",
		"/_overcast/elb/healthz",
		"/_overcast/cognito/user-pools/us-east-1_abc123/login",
		"/_overcast/cognito/user-pools/us-east-1_abc123/oauth2/token",
	}
	for _, p := range user {
		if isInternalPath(p) {
			t.Errorf("isInternalPath(%q) = true, want false", p)
		}
	}
}

// Capacity spans both rings, so that Len — which counts every retained trace —
// can never exceed it. The number asked for is the user-facing ring; the
// internal ring is additional, which is the point of separating them.
func TestBufferCapacity(t *testing.T) {
	buf := NewBuffer(42)
	if want := 42 + 42/5; buf.Capacity() != want {
		t.Errorf("Capacity = %d, want %d", buf.Capacity(), want)
	}

	buf2 := NewBuffer(0)
	if want := 1000 + maxInternalRing; buf2.Capacity() != want {
		t.Errorf("Capacity = %d, want %d for the default floor", buf2.Capacity(), want)
	}
}

func TestRecorderAddHop(t *testing.T) {
	rec := NewRecorder("req-1", time.Now(), "POST", "/", "host", "", http.Header{})
	id := rec.AddHop(Hop{
		Service:        "lambda",
		Operation:      "CreateFunction",
		CallerService:  "cloudformation",
		ResponseStatus: 200,
		Duration:       time.Millisecond * 100,
	})
	if id != "hop-1" {
		t.Errorf("expected hop-1, got %s", id)
	}

	e := rec.Entry()
	if len(e.Hops) != 1 {
		t.Fatalf("expected 1 hop, got %d", len(e.Hops))
	}
	if e.Hops[0].Service != "lambda" {
		t.Errorf("expected lambda, got %s", e.Hops[0].Service)
	}
	if e.Hops[0].Order != 1 {
		t.Errorf("expected order 1, got %d", e.Hops[0].Order)
	}
}

func TestRecorderRequestID(t *testing.T) {
	rec := NewRecorder("req-1", time.Now(), "POST", "/", "host", "", http.Header{})
	if got := rec.RequestID(); got != "req-1" {
		t.Errorf("expected req-1, got %q", got)
	}

	var nilRec *Recorder
	if got := nilRec.RequestID(); got != "" {
		t.Errorf("expected empty request ID from nil recorder, got %q", got)
	}
}

func TestRecorderAddLog(t *testing.T) {
	rec := NewRecorder("req-1", time.Now(), "GET", "/", "host", "", http.Header{})
	rec.AddLog(LogEntry{
		Level:   "INFO",
		Message: "request",
	})
	rec.AddLog(LogEntry{
		Level:   "ERROR",
		Message: "request failed",
	})

	e := rec.Entry()
	if len(e.LogEntries) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(e.LogEntries))
	}
}

func TestRecorderNilSafe(t *testing.T) {
	var rec *Recorder
	rec.SetRequestBody([]byte("test"), false, -1)
	rec.SetResponse(http.Header{}, []byte("resp"), 200, 1024, false)
	rec.SetServiceInfo("sqs", "SendMessage", "us-east-1")
	rec.SetDuration(time.Second)
	rec.AddHop(Hop{Service: "lambda"})
	rec.AddLog(LogEntry{Message: "test"})
	rec.AddMeta("key", "value")

	e := rec.Entry()
	if e.RequestID != "" {
		t.Error("nil recorder should return empty entry")
	}
	if rec.Summary().RequestID != "" {
		t.Error("nil recorder should return empty summary")
	}
	if rec.HasHop("anything") {
		t.Error("nil recorder should report no hops")
	}
}

// A nil buffer is the debug-off configuration: every method must be inert
// rather than panicking.
func TestBuffer_nilSafe(t *testing.T) {
	var buf *Buffer
	buf.Add(NewRecorder("req-1", time.Now(), "GET", "/", "host", "", http.Header{}))
	if _, ok := buf.Get("req-1"); ok {
		t.Error("nil buffer should hold nothing")
	}
	if summaries, cursor := buf.ListSummaries(ListFilter{}); summaries != nil || cursor != "" {
		t.Error("nil buffer should list nothing")
	}
	if buf.Len() != 0 || buf.Capacity() != 0 {
		t.Error("nil buffer should report no length or capacity")
	}
}

func TestRecorderBodyTruncation(t *testing.T) {
	rec := NewRecorder("req-1", time.Now(), "POST", "/", "host", "", http.Header{})
	body := make([]byte, 2000)
	for i := range body {
		body[i] = 'x'
	}
	rec.SetRequestBody(body[:1024], true, int64(len(body)))

	e := rec.Entry()
	if e.RequestBodyOmitted != OmitSize {
		t.Errorf("RequestBodyOmitted = %q, want %q", e.RequestBodyOmitted, OmitSize)
	}
	if len(e.RequestBody) != 1024 {
		t.Errorf("expected 1024 bytes, got %d", len(e.RequestBody))
	}
	if e.RequestSize != 2000 {
		t.Errorf("expected RequestSize 2000, got %d", e.RequestSize)
	}
}

func TestRecorderStreamingResponse(t *testing.T) {
	rec := NewRecorder("req-1", time.Now(), "GET", "/", "host", "", http.Header{})
	rec.SetResponse(http.Header{}, []byte("partial"), 200, 1024, true)

	e := rec.Entry()
	if !e.Streaming {
		t.Error("expected streaming true")
	}
	if e.ResponseBodyOmitted != OmitStreaming {
		t.Errorf("ResponseBodyOmitted = %q, want %q", e.ResponseBodyOmitted, OmitStreaming)
	}
}

func TestRecorderSetMeta(t *testing.T) {
	rec := NewRecorder("req-1", time.Now(), "GET", "/", "host", "", http.Header{})
	rec.SetMeta("1.2.3.4", "test-agent", "", "AccessDenied", "User is not authorized")

	e := rec.Entry()
	if e.RemoteAddr != "1.2.3.4" {
		t.Errorf("expected remote addr 1.2.3.4, got %s", e.RemoteAddr)
	}
	if e.UserAgent != "test-agent" {
		t.Errorf("expected user agent test-agent, got %s", e.UserAgent)
	}
	if e.AWSErrorCode != "AccessDenied" {
		t.Errorf("expected AccessDenied, got %s", e.AWSErrorCode)
	}
}

func TestContextWithRecorder(t *testing.T) {
	ctx := context.Background()
	rec := NewRecorder("req-1", time.Now(), "GET", "/", "host", "", http.Header{})
	ctx = ContextWithRecorder(ctx, rec)

	got := RecorderFromContext(ctx)
	if got == nil {
		t.Fatal("expected recorder from context")
	}
}

func TestRecorderFromContextNil(t *testing.T) {
	ctx := context.Background()
	got := RecorderFromContext(ctx)
	if got != nil {
		t.Error("expected nil from empty context")
	}
}

func TestRecorderAddMeta(t *testing.T) {
	rec := NewRecorder("req-1", time.Now(), "GET", "/", "host", "", http.Header{})
	rec.AddMeta("lambdaFunctionName", "my-func")
	rec.AddMeta("queueUrl", "http://localhost:4566/queue/my-queue")

	e := rec.Entry()
	if e.Metadata["lambdaFunctionName"] != "my-func" {
		t.Errorf("unexpected metadata value: %v", e.Metadata["lambdaFunctionName"])
	}
	if len(e.Metadata) != 2 {
		t.Errorf("expected 2 metadata entries, got %d", len(e.Metadata))
	}
}

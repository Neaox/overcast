package trace

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestBufferStoreAndGet(t *testing.T) {
	buf := NewBuffer(10)
	e := &Entry{
		RequestID: "req-1",
		Timestamp: time.Now(),
		Method:    "POST",
		Path:      "/",
		Service:   "sqs",
	}

	buf.Store(e)
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

func TestBufferStoreUpdate(t *testing.T) {
	buf := NewBuffer(10)
	e1 := &Entry{RequestID: "req-1", Service: "sqs"}
	e2 := &Entry{RequestID: "req-1", Service: "lambda"}

	buf.Store(e1)
	buf.Store(e2)
	if buf.Len() != 1 {
		t.Fatalf("expected 1 entry after update, got %d", buf.Len())
	}

	got, _ := buf.Get("req-1")
	if got.Service != "lambda" {
		t.Errorf("expected lambda after update, got %s", got.Service)
	}
}

func TestBufferEviction(t *testing.T) {
	buf := NewBuffer(3)
	for i := 0; i < 5; i++ {
		buf.Store(&Entry{
			RequestID: "req-" + strconv.Itoa(i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}
	if buf.Len() != 3 {
		t.Fatalf("expected 3 entries after eviction, got %d", buf.Len())
	}
	_, ok := buf.Get("req-0")
	if ok {
		t.Error("req-0 should have been evicted")
	}
	_, ok = buf.Get("req-1")
	if ok {
		t.Error("req-1 should have been evicted")
	}
	_, ok = buf.Get("req-4")
	if !ok {
		t.Error("req-4 should still be present")
	}
}

// countInternal walks the raw slots and reports how many stored entries are
// internal, so tests can verify quota accounting independently of the
// internalCount field.
func countInternal(b *Buffer) int {
	n := 0
	for _, e := range b.entries {
		if e != nil && isInternalPath(e.Path) {
			n++
		}
	}
	return n
}

func TestBufferInternalCapHoldsAfterWrap(t *testing.T) {
	// Given a buffer filled past capacity with user entries, When internal
	// poll traces keep arriving, Then the internal quota (capacity/5) holds
	// and user traces are not evicted beyond that quota.
	buf := NewBuffer(10) // maxInternal = 2
	for i := 0; i < 10; i++ {
		buf.Store(&Entry{
			RequestID: "user-" + strconv.Itoa(i),
			Path:      "/",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	// Simulate idle polling: many internal traces against a full buffer.
	for i := 0; i < 20; i++ {
		buf.Store(&Entry{
			RequestID: "int-" + strconv.Itoa(i),
			Path:      "/_health",
			Timestamp: time.Now().Add(time.Duration(10+i) * time.Second),
		})
	}

	if got := countInternal(buf); got > 2 {
		t.Errorf("internal entries exceeded quota: got %d, want <= 2", got)
	}
	if buf.internalCount != countInternal(buf) {
		t.Errorf("internalCount accounting drifted: field says %d, actual %d", buf.internalCount, countInternal(buf))
	}
	if buf.internalCount < 0 {
		t.Errorf("internalCount went negative: %d", buf.internalCount)
	}
	// The two oldest user entries may have been evicted to admit the quota's
	// worth of internal entries; the remaining eight must survive.
	for i := 2; i < 10; i++ {
		if _, ok := buf.Get("user-" + strconv.Itoa(i)); !ok {
			t.Errorf("user-%d was evicted by internal polling", i)
		}
	}
}

func TestBufferInternalQuotaRotatesWhenNotFull(t *testing.T) {
	// Given a never-filled buffer whose internal quota is full, When a new
	// internal trace arrives, Then the oldest internal entry is recycled
	// (ring semantics within the quota) instead of dropping the new trace.
	buf := NewBuffer(10) // maxInternal = 2
	buf.Store(&Entry{RequestID: "int-0", Path: "/_health", Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "int-1", Path: "/_health", Timestamp: time.Now().Add(time.Second)})
	buf.Store(&Entry{RequestID: "int-2", Path: "/_health", Timestamp: time.Now().Add(2 * time.Second)})

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
	if buf.internalCount != 2 {
		t.Errorf("expected internalCount 2, got %d", buf.internalCount)
	}
}

func TestBufferFullEvictionFairness(t *testing.T) {
	// Given a full buffer where a user entry reclaimed an internal entry's
	// near-head slot, When further user entries arrive, Then the reclaiming
	// entry is not evicted almost immediately — eviction stays oldest-first.
	buf := NewBuffer(5) // maxInternal = 1
	buf.Store(&Entry{RequestID: "user-0", Path: "/", Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "int-1", Path: "/_health", Timestamp: time.Now().Add(time.Second)})
	for i := 2; i < 5; i++ {
		buf.Store(&Entry{
			RequestID: "user-" + strconv.Itoa(i),
			Path:      "/",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	// Full buffer: this user entry reclaims int-1's slot (index 1, near head).
	buf.Store(&Entry{RequestID: "user-5", Path: "/", Timestamp: time.Now().Add(5 * time.Second)})
	// Two more user entries should evict user-0 and user-2 (the oldest),
	// not the just-inserted user-5.
	buf.Store(&Entry{RequestID: "user-6", Path: "/", Timestamp: time.Now().Add(6 * time.Second)})
	buf.Store(&Entry{RequestID: "user-7", Path: "/", Timestamp: time.Now().Add(7 * time.Second)})

	if _, ok := buf.Get("user-5"); !ok {
		t.Error("user-5 was evicted prematurely after inheriting a near-head slot")
	}
	if _, ok := buf.Get("user-0"); ok {
		t.Error("user-0 (oldest) should have been evicted first")
	}
	if _, ok := buf.Get("user-2"); ok {
		t.Error("user-2 (second oldest) should have been evicted second")
	}
}

func TestBufferGetMissing(t *testing.T) {
	buf := NewBuffer(10)
	_, ok := buf.Get("nonexistent")
	if ok {
		t.Error("expected false for missing key")
	}
}

func TestBufferListFilterService(t *testing.T) {
	buf := NewBuffer(10)
	buf.Store(&Entry{RequestID: "r1", Service: "sqs", Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "r2", Service: "lambda", Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "r3", Service: "sqs", Timestamp: time.Now()})

	entries, _ := buf.List(ListFilter{Service: "sqs", Limit: 10})
	if len(entries) != 2 {
		t.Fatalf("expected 2 sqs entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Service != "sqs" {
			t.Errorf("expected sqs, got %s", e.Service)
		}
	}
}

func TestBufferListFilterStatus(t *testing.T) {
	buf := NewBuffer(10)
	buf.Store(&Entry{RequestID: "r1", StatusCode: 200, Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "r2", StatusCode: 404, Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "r3", StatusCode: 500, Timestamp: time.Now()})

	entries, _ := buf.List(ListFilter{Status: "2xx", Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry with 2xx, got %d", len(entries))
	}
	if entries[0].RequestID != "r1" {
		t.Errorf("expected r1, got %s", entries[0].RequestID)
	}
}

func TestBufferListFilterMethod(t *testing.T) {
	buf := NewBuffer(10)
	buf.Store(&Entry{RequestID: "r1", Method: "GET", Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "r2", Method: "POST", Timestamp: time.Now()})

	entries, _ := buf.List(ListFilter{Method: "POST", Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 POST entry, got %d", len(entries))
	}
}

func TestBufferListFilterPath(t *testing.T) {
	buf := NewBuffer(10)
	buf.Store(&Entry{RequestID: "r1", Path: "/2015-03-31/functions", Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "r2", Path: "/", Timestamp: time.Now()})

	entries, _ := buf.List(ListFilter{Path: "functions", Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry matching path, got %d", len(entries))
	}
}

func TestBufferListSearch(t *testing.T) {
	buf := NewBuffer(10)
	buf.Store(&Entry{RequestID: "abc-123", Path: "/some/path", Service: "sqs", Timestamp: time.Now()})
	buf.Store(&Entry{RequestID: "xyz-789", Path: "/other", Service: "lambda", Timestamp: time.Now()})

	entries, _ := buf.List(ListFilter{Search: "abc", Limit: 10})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry matching search, got %d", len(entries))
	}
	if entries[0].RequestID != "abc-123" {
		t.Errorf("expected abc-123, got %s", entries[0].RequestID)
	}
}

func TestBufferListPagination(t *testing.T) {
	buf := NewBuffer(10)
	for i := 0; i < 5; i++ {
		buf.Store(&Entry{
			RequestID: "req-" + strconv.Itoa(i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	page1, cursor := buf.List(ListFilter{Limit: 2})
	if len(page1) != 2 {
		t.Fatalf("expected 2 entries on page 1, got %d", len(page1))
	}
	if cursor == "" {
		t.Fatal("expected non-empty cursor")
	}

	page2, cursor2 := buf.List(ListFilter{Limit: 2, After: cursor})
	if len(page2) != 2 {
		t.Fatalf("expected 2 entries on page 2, got %d", len(page2))
	}
	if cursor2 == "" {
		t.Fatal("expected non-empty cursor for page 2")
	}

	page3, cursor3 := buf.List(ListFilter{Limit: 2, After: cursor2})
	if len(page3) != 1 {
		t.Fatalf("expected 1 entry on page 3, got %d", len(page3))
	}
	if cursor3 != "" {
		t.Error("expected empty cursor on last page")
	}
}

func TestBufferListBeforeHonoursLimit(t *testing.T) {
	// Given entries newer than the cursor, When listing with Before and a
	// Limit, Then at most Limit entries come back (newest first).
	buf := NewBuffer(10)
	for i := 0; i < 6; i++ {
		buf.Store(&Entry{
			RequestID: "req-" + strconv.Itoa(i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	entries, cursor := buf.List(ListFilter{Before: "req-0", Limit: 2})
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

func TestBufferListBeforeEvictedCursorHonoursLimit(t *testing.T) {
	// Given a Before cursor whose entry has been evicted, When listing with a
	// Limit, Then at most Limit entries come back instead of everything.
	buf := NewBuffer(10)
	for i := 0; i < 6; i++ {
		buf.Store(&Entry{
			RequestID: "req-" + strconv.Itoa(i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	entries, _ := buf.List(ListFilter{Before: "gone", Limit: 2})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with Limit 2 for evicted cursor, got %d", len(entries))
	}
	if entries[0].RequestID != "req-5" || entries[1].RequestID != "req-4" {
		t.Errorf("expected newest-first [req-5 req-4], got [%s %s]", entries[0].RequestID, entries[1].RequestID)
	}
}

func TestIsInternalPathDebugNamespace(t *testing.T) {
	// The whole /_debug/* namespace and /_metrics are polled by the web UI
	// and must be classified internal, matching middleware's
	// isOperationalPollPath, so polling never consumes user-trace budget.
	internal := []string{
		"/_health",
		"/_metrics",
		"/_debug",
		"/_debug/traces",
		"/_debug/traces/abc-123",
		"/_debug/trace/abc-123",
		"/_debug/traces/count",
		"/_debug/metrics",
		"/_debug/state",
		"/_debug/state/sqs:queues",
		"/_events",
		"/_events/request",
		"/_overcast/inbox",
		"/_overcast/inbox/messages",
		"/_/info",
	}
	for _, p := range internal {
		if !isInternalPath(p) {
			t.Errorf("isInternalPath(%q) = false, want true", p)
		}
	}
	user := []string{
		"/",
		"/2015-03-31/functions",
		"/my-bucket/key",
		"/_debugfoo",
		"/_overcast/init",
	}
	for _, p := range user {
		if isInternalPath(p) {
			t.Errorf("isInternalPath(%q) = true, want false", p)
		}
	}
}

func TestBufferCapacity(t *testing.T) {
	buf := NewBuffer(42)
	if buf.Capacity() != 42 {
		t.Errorf("expected 42, got %d", buf.Capacity())
	}

	buf2 := NewBuffer(0)
	if buf2.Capacity() != 1000 {
		t.Errorf("expected default 1000, got %d", buf2.Capacity())
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
	rec.SetRequestBody([]byte("test"), 1024)
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
}

func TestRecorderBodyTruncation(t *testing.T) {
	rec := NewRecorder("req-1", time.Now(), "POST", "/", "host", "", http.Header{})
	body := make([]byte, 2000)
	for i := range body {
		body[i] = 'x'
	}
	rec.SetRequestBody(body, 1024)

	e := rec.Entry()
	if !e.RequestBodyTruncated {
		t.Error("expected body to be truncated")
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
	if !e.ResponseBodyTruncated {
		t.Error("expected response body truncated for streaming")
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

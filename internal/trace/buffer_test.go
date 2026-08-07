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

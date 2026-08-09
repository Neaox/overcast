package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/trace"
)

// With debug on, DebugTrace has already captured the request. Logger must
// reuse that capture rather than reading and holding the body a second time.
func TestLogger_reusesDebugTraceCapture(t *testing.T) {
	// Given: the debug-on chain — DebugTrace then Logger
	cfg := &config.Config{Debug: true, Region: "us-east-1"}
	var seen *requestCapture
	var wrappedAgain bool
	h := DebugTrace(cfg, trace.NewBuffer(10), clock.New())(
		Logger(zap.NewNop(), clock.New())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = requestCaptureFromContext(r.Context())
			_, wrappedAgain = r.Body.(*teeBody)
			w.WriteHeader(http.StatusOK)
		})))

	// When: a request with a body goes through
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"QueueName":"q"}`))
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Then: one capture served both, and Logger installed no second tee
	if seen == nil {
		t.Fatal("no request capture published on the context")
	}
	if wrappedAgain {
		t.Error("Logger tee'd a body DebugTrace had already captured")
	}
	if got := string(seen.buf); got != `{"QueueName":"q"}` {
		t.Errorf("captured body = %q", got)
	}
}

// With debug off nothing has captured the request, so Logger tees the body —
// lazily, so a handler that ignores it costs nothing.
func TestLogger_teesBodyWhenNothingElseCaptured(t *testing.T) {
	// Given: Logger alone
	var wrapped bool
	h := Logger(zap.NewNop(), clock.New())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, wrapped = r.Body.(*teeBody)
		w.WriteHeader(http.StatusOK)
	}))

	// When: a request with a body goes through
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Then: the body was tee'd, not pre-read
	if !wrapped {
		t.Error("Logger did not install a body tee")
	}
}

// A request rejected before the handler reads its body still has to show what
// the client sent — the lazy tee must not cost the failure log its body.
func TestLogger_failureLogsBodyTheHandlerNeverRead(t *testing.T) {
	// Given: a handler that 500s without reading the request body
	core, logs := observer.New(zapcore.DebugLevel)
	h := Logger(zap.New(core), clock.New())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	// When: a request with a body fails
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("Action=SendMessage&MessageBody=hi"))
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Then: the failure log still carries the body
	entries := logs.FilterMessage("request failed").All()
	if len(entries) != 1 {
		t.Fatalf("request failed log entries = %d, want 1", len(entries))
	}
	if got := entries[0].ContextMap()["request_body"]; got != "Action=SendMessage&MessageBody=hi" {
		t.Errorf("request_body = %v", got)
	}
}

// A body too large for a log line is bounded, and the log says so rather than
// implying the client sent only that much.
func TestLogger_failureLogsTruncatedBodyHonestly(t *testing.T) {
	// Given: a handler that 500s after reading an oversized body
	core, logs := observer.New(zapcore.DebugLevel)
	h := Logger(zap.New(core), clock.New())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	// When: a request far larger than the log cap fails
	payload := bytes.Repeat([]byte("x"), maxLoggedRequestBody+4096)
	req := httptest.NewRequest(http.MethodPut, "/bucket/key", bytes.NewReader(payload))
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Then: the logged body is capped and flagged as truncated
	entries := logs.FilterMessage("request failed").All()
	if len(entries) != 1 {
		t.Fatalf("request failed log entries = %d, want 1", len(entries))
	}
	ctx := entries[0].ContextMap()
	body, _ := ctx["request_body"].(string)
	if len(body) != maxLoggedRequestBody {
		t.Errorf("logged body = %d bytes, want %d", len(body), maxLoggedRequestBody)
	}
	if ctx["request_body_truncated"] != true {
		t.Error("oversized body logged without request_body_truncated")
	}
}

// Logger's and RequestEvents' response writers both capture a stack at
// WriteHeader time and they nest. Capturing on every call cost two
// runtime.Stack calls and kept the outer one, which is a frame further from
// the handler. The innermost capture must win, and it must happen once.
func TestResponseWriter_capturesStackOncePerRequest(t *testing.T) {
	// Given: the full chain — DebugTrace, Logger, then RequestEvents
	cfg := &config.Config{Debug: true, Region: "us-east-1"}
	buf := trace.NewBuffer(10)
	var bus *events.Bus
	h := DebugTrace(cfg, buf, clock.New())(
		Logger(zap.NewNop(), clock.New())(
			RequestEvents(&bus, clock.New())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))))

	// When: a request goes through
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Then: the stored stack is the innermost writer's — one
	// responseWriter.WriteHeader frame, not the two an outer capture shows
	summaries, _ := buf.ListSummaries(trace.ListFilter{})
	if len(summaries) != 1 {
		t.Fatalf("trace entries = %d, want 1", len(summaries))
	}
	entry, ok := buf.Get(summaries[0].RequestID)
	if !ok {
		t.Fatalf("trace %q listed but not retrievable", summaries[0].RequestID)
	}
	stack := entry.Stack
	if stack == "" {
		t.Fatal("no stack captured")
	}
	if got := strings.Count(stack, "responseWriter).WriteHeader"); got != 1 {
		t.Errorf("responseWriter.WriteHeader frames = %d, want 1 (the innermost writer's)\n%s", got, stack)
	}
}

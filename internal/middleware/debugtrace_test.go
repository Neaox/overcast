package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/trace"
)

func TestDebugTraceMiddlewareDisabled(t *testing.T) {
	cfg := &config.Config{Debug: false}
	mw := DebugTrace(cfg, nil, clock.New())
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	handler := mw(next)

	req := httptest.NewRequest("POST", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", rec.Body.String())
	}
}

func TestDebugTraceMiddlewareEnabled(t *testing.T) {
	cfg := &config.Config{Debug: true, Region: "us-east-1"}
	buf := trace.NewBuffer(100)
	mw := DebugTrace(cfg, buf, clock.New())

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rec := trace.RecorderFromContext(r.Context()); rec == nil {
			t.Error("expected recorder in context")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	})
	handler := mw(next)

	req := httptest.NewRequest("POST", "/2015-03-31/functions", nil)
	req.Header.Set("Content-Type", "application/json")
	ctx := protocol.ContextWithRequestID(req.Context(), "test-req-id")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	entry, found := buf.Get("test-req-id")
	if !found {
		t.Fatal("trace entry not found in buffer")
	}
	if entry.RequestID != "test-req-id" {
		t.Errorf("expected test-req-id, got %s", entry.RequestID)
	}
	if entry.Method != "POST" {
		t.Errorf("expected POST, got %s", entry.Method)
	}
	if entry.Path != "/2015-03-31/functions" {
		t.Errorf("unexpected path: %s", entry.Path)
	}
	if entry.Service != "lambda" {
		t.Errorf("expected lambda, got %s", entry.Service)
	}
	if entry.Region != "us-east-1" {
		t.Errorf("expected us-east-1, got %s", entry.Region)
	}
	if entry.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", entry.StatusCode)
	}
	if string(entry.ResponseBody) != `{"result":"ok"}` {
		t.Errorf("unexpected response body: %s", entry.ResponseBody)
	}
	if entry.Duration < 0 {
		t.Error("expected non-negative duration")
	}
}

func TestDebugTraceMiddlewareRequestBodyCapture(t *testing.T) {
	cfg := &config.Config{Debug: true}
	buf := trace.NewBuffer(100)
	mw := DebugTrace(cfg, buf, clock.New())

	var handlerBody []byte
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(next)

	req := httptest.NewRequest("POST", "/", strings.NewReader("hello world"))
	req.Header.Set("Content-Type", "text/plain")
	ctx := protocol.ContextWithRequestID(req.Context(), "body-test")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if string(handlerBody) != "hello world" {
		t.Errorf("handler should still be able to read body, got %q", handlerBody)
	}

	entry, _ := buf.Get("body-test")
	if string(entry.RequestBody) != "hello world" {
		t.Errorf("unexpected captured body: %q", entry.RequestBody)
	}
	if entry.RequestSize != 11 {
		t.Errorf("expected RequestSize 11, got %d", entry.RequestSize)
	}
}

// A handler that writes the body without calling WriteHeader responds 200 via
// net/http's implicit default; the trace must record 200 rather than 0, which
// the UI renders as a request that never completed.
func TestDebugTraceMiddlewareImplicitStatusDefaultsTo200(t *testing.T) {
	cfg := &config.Config{Debug: true}
	buf := trace.NewBuffer(100)
	mw := DebugTrace(cfg, buf, clock.New())

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok")) // no explicit WriteHeader
	})
	handler := mw(next)

	req := httptest.NewRequest("GET", "/", nil)
	ctx := protocol.ContextWithRequestID(req.Context(), "implicit-status")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry, found := buf.Get("implicit-status")
	if !found {
		t.Fatal("trace entry not found in buffer")
	}
	if entry.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", entry.StatusCode)
	}
}

func TestTraceResponseWriter(t *testing.T) {
	inner := httptest.NewRecorder()
	trw := &traceResponseWriter{
		ResponseWriter: inner,
		tee:            new(bytes.Buffer),
		cap:            1024,
	}

	trw.WriteHeader(http.StatusCreated)
	trw.Write([]byte("response body"))
	trw.Write([]byte(" more"))

	if trw.status != 201 {
		t.Errorf("expected status 201, got %d", trw.status)
	}
	if inner.Code != 201 {
		t.Errorf("expected inner status 201, got %d", inner.Code)
	}
	if trw.tee.String() != "response body more" {
		t.Errorf("unexpected tee content: %q", trw.tee.String())
	}
	if inner.Body.String() != "response body more" {
		t.Errorf("unexpected inner body: %q", inner.Body.String())
	}
}

func TestTraceResponseWriterCap(t *testing.T) {
	inner := httptest.NewRecorder()
	trw := &traceResponseWriter{
		ResponseWriter: inner,
		tee:            new(bytes.Buffer),
		cap:            5,
	}

	trw.Write([]byte("1234567890"))
	if trw.tee.String() != "12345" {
		t.Errorf("expected capped to 5 bytes, got %q", trw.tee.String())
	}
	if inner.Body.String() != "1234567890" {
		t.Errorf("inner should get full body, got %q", inner.Body.String())
	}
}

func TestTraceResponseWriterStreaming(t *testing.T) {
	inner := httptest.NewRecorder()
	trw := &traceResponseWriter{
		ResponseWriter: inner,
		tee:            new(bytes.Buffer),
		cap:            1024,
	}

	trw.Write([]byte("before flush"))
	trw.Flush()

	if !trw.streaming {
		t.Error("expected streaming true after Flush")
	}

	trw.Write([]byte("after flush"))
	if trw.tee.String() != "before flush" {
		t.Errorf("should not capture after flush, got %q", trw.tee.String())
	}
}

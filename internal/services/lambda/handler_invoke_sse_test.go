package lambda

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// syncResponseWriter is an http.ResponseWriter the test can read while the
// handler is still writing to it, which httptest.ResponseRecorder cannot be.
type syncResponseWriter struct {
	mu      sync.Mutex
	hdr     http.Header
	buf     bytes.Buffer
	flushed chan struct{}
}

func newSyncResponseWriter() *syncResponseWriter {
	return &syncResponseWriter{hdr: make(http.Header), flushed: make(chan struct{}, 64)}
}

func (w *syncResponseWriter) Header() http.Header { return w.hdr }

func (w *syncResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncResponseWriter) WriteHeader(int) {}

func (w *syncResponseWriter) Flush() {
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}

func (w *syncResponseWriter) body() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// blockingInstance is a RuntimeInstance whose Invoke parks until the test
// releases it, standing in for a handler doing slow work.
type blockingInstance struct {
	invoking chan struct{}
	release  chan struct{}
}

func (b *blockingInstance) Invoke(ctx context.Context, _ []byte, _ InvokeOptions) (*InvokeResult, error) {
	close(b.invoking)
	select {
	case <-b.release:
		return &InvokeResult{StatusCode: 200, Payload: []byte(`{"ok":true}`)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingInstance) LogStreamName() string  { return "2026/07/28/[$LATEST]abc" }
func (b *blockingInstance) Healthy() bool          { return true }
func (b *blockingInstance) FunctionName() string   { return "slow" }
func (b *blockingInstance) ConfigIdentity() string { return "identity" }
func (b *blockingInstance) ContainerID() string    { return "" }
func (b *blockingInstance) InstanceID() string     { return "instance-1" }
func (b *blockingInstance) Close() error           { return nil }

// stubRuntime hands out a single pre-built instance.
type stubRuntime struct{ inst RuntimeInstance }

func (s *stubRuntime) CanHandle(string) bool { return true }
func (s *stubRuntime) Acquire(context.Context, *Function) (RuntimeInstance, error) {
	return s.inst, nil
}
func (s *stubRuntime) Release(context.Context, RuntimeInstance, bool) {}

// TestInvokeFunctionSSE_keepsStreamWarmWhileHandlerRuns pins that the invoke
// progress stream emits keepalive frames for the whole time the handler runs,
// and still delivers the result afterwards.
//
// Between "Invoking function handler" and the result the stream had nothing to
// say, so a function running for minutes — well within its configured timeout —
// left the connection idle for the whole invocation, which is what proxies and
// browsers drop. A dropped connection surfaced in the console as a stream that
// ended with no result at all.
func TestInvokeFunctionSSE_keepsStreamWarmWhileHandlerRuns(t *testing.T) {
	// Given: a function whose handler parks until the test releases it.
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	fn := &Function{
		Name:       "slow",
		Runtime:    "nodejs22.x",
		Handler:    "index.handler",
		ARN:        "arn:aws:lambda:us-east-1:000000000000:function:slow",
		MemorySize: 512,
		Timeout:    900,
		State:      "Active",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("putFunction: %v", aerr)
	}

	inst := &blockingInstance{invoking: make(chan struct{}), release: make(chan struct{})}
	h := &Handler{
		cfg:      &config.Config{Region: "us-east-1"},
		log:      serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk:      clk,
		ls:       ls,
		runtimes: newRuntimeRegistry([]Runtime{&stubRuntime{inst: inst}}),
	}

	req := httptest.NewRequest(http.MethodPost,
		"/2015-03-31/functions/slow/invoke-with-progress",
		strings.NewReader(`{"payload":"{}"}`))
	req = withFunctionNameParam(req, "slow")
	w := newSyncResponseWriter()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.InvokeFunctionSSE(w, req)
	}()

	// When: the handler has been running for two keepalive intervals.
	select {
	case <-inst.invoking:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never reached Invoke")
	}
	waitForKeepalives := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			clk.Add(sseKeepaliveInterval)
			if strings.Count(w.body(), ": keepalive") >= want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("stream went idle: wanted %d keepalive frames, body was:\n%s", want, w.body())
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitForKeepalives(2)

	// And: the handler then returns.
	close(inst.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the invocation completed")
	}

	// Then: the result event still lands, after the keepalives.
	body := w.body()
	if !strings.Contains(body, "event: result") {
		t.Fatalf("no result event in stream:\n%s", body)
	}
	if strings.Index(body, ": keepalive") > strings.Index(body, "event: result") {
		t.Errorf("keepalives should precede the result, got:\n%s", body)
	}
}

//go:build linux

package lambdainit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

func TestClassifyNamesTheInvocationBoundaries(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   observation
		wantID string
	}{
		{method: "GET", path: "/2018-06-01/runtime/invocation/next", want: observeNext},
		{method: "POST", path: "/2018-06-01/runtime/invocation/abc-123/response", want: observeResponse, wantID: "abc-123"},
		{method: "POST", path: "/2018-06-01/runtime/invocation/abc-123/error", want: observeError, wantID: "abc-123"},
		// Everything else is pass-through, including the rest of the runtime
		// API and the whole extensions API.
		{method: "POST", path: "/2018-06-01/runtime/init/error", want: observeNone},
		{method: "POST", path: "/2020-01-01/extension/register", want: observeNone},
		{method: "GET", path: "/2020-01-01/extension/event/next", want: observeNone},
		{method: "POST", path: "/2020-08-15/logs", want: observeNone},
		{method: "GET", path: "/2018-06-01/runtime/invocation/abc/response", want: observeNone},
		{method: "POST", path: "/2018-06-01/runtime/invocation/next", want: observeNone},
		{method: "POST", path: "/2018-06-01/runtime/invocation//response", want: observeNone},
		{method: "POST", path: "/2018-06-01/runtime/invocation/abc/unknown", want: observeNone},
	}

	for _, tc := range tests {
		got, id := classify(tc.method, tc.path)
		if got != tc.want || id != tc.wantID {
			t.Errorf("classify(%s %s) = (%v, %q), want (%v, %q)", tc.method, tc.path, got, id, tc.want, tc.wantID)
		}
	}
}

// echoRecord is what the fake host saw of one proxied request.
type echoRecord struct {
	method string
	path   string
	query  string
	header http.Header
	body   []byte
}

func TestProxyPassesEverythingThrough(t *testing.T) {
	var got atomic.Pointer[echoRecord]
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.Store(&echoRecord{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			header: r.Header.Clone(),
			body:   body,
		})
		w.Header().Set("Lambda-Extension-Identifier", "ext-9")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"functionName":"test"}`)
	}))
	defer target.Close()

	var drains atomic.Int64
	addr := startProxy(t, target.Listener.Addr().String(), func(context.Context) uint64 {
		drains.Add(1)
		return 0
	})

	// The extensions API, which the init has no business understanding.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+addr+"/2020-01-01/extension/register?feature=logs",
		strings.NewReader(`{"events":["INVOKE","SHUTDOWN"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Name", "telemetry")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	rec := got.Load()
	if rec == nil {
		t.Fatal("the host never saw the request")
	}
	if rec.method != http.MethodPost || rec.path != "/2020-01-01/extension/register" || rec.query != "feature=logs" {
		t.Errorf("host saw %s %s?%s", rec.method, rec.path, rec.query)
	}
	if rec.header.Get("Lambda-Extension-Name") != "telemetry" {
		t.Errorf("request headers did not survive: %v", rec.header)
	}
	if string(rec.body) != `{"events":["INVOKE","SHUTDOWN"]}` {
		t.Errorf("request body did not survive: %q", rec.body)
	}
	if rec.header.Get(initproto.HeaderLogSeq) != "" {
		t.Errorf("the init added %s to a request that is not an invocation result", initproto.HeaderLogSeq)
	}

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %s", resp.Status)
	}
	if resp.Header.Get("Lambda-Extension-Identifier") != "ext-9" {
		t.Errorf("response headers did not survive: %v", resp.Header)
	}
	if string(body) != `{"functionName":"test"}` {
		t.Errorf("response body did not survive: %q", body)
	}
	if n := drains.Load(); n != 0 {
		t.Errorf("the pipes were drained %d times for a pass-through call", n)
	}
}

func TestProxyStreamsLargeResponsesWithoutBuffering(t *testing.T) {
	const chunk = 1024
	const total = 6 << 20 // a Lambda response payload at the documented limit

	release := make(chan struct{})
	served := make(chan error, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The first kilobyte, then nothing until the client has read it. If
		// anything between here and the client buffers the whole response, the
		// client cannot read and this handler never resumes.
		if _, err := w.Write(bytes.Repeat([]byte("a"), chunk)); err != nil {
			served <- err
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-time.After(20 * time.Second):
			served <- context.DeadlineExceeded
			return
		}
		_, err := w.Write(bytes.Repeat([]byte("b"), total-chunk))
		served <- err
	}))
	defer target.Close()

	addr := startProxy(t, target.Listener.Addr().String(), func(context.Context) uint64 { return 0 })

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+addr+"/2018-06-01/runtime/invocation/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	head := make([]byte, chunk)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		t.Fatalf("the first chunk never arrived — the response was buffered: %v", err)
	}
	close(release)

	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the rest: %v", err)
	}
	if err := <-served; err != nil {
		t.Fatalf("the host could not serve the body: %v", err)
	}
	if len(head)+len(rest) != total {
		t.Fatalf("got %d bytes, want %d", len(head)+len(rest), total)
	}
	if bytes.Count(rest, []byte("b")) != total-chunk {
		t.Fatalf("the body was corrupted in transit")
	}
}

func TestProxyStreamsLargeRequestBodies(t *testing.T) {
	const total = 6 << 20

	payload := bytes.Repeat([]byte("payload!"), total/8)
	want := sha256.Sum256(payload)

	var gotSum atomic.Pointer[[32]byte]
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		if _, err := io.Copy(h, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		gotSum.Store(&sum)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	drained := make(chan struct{}, 1)
	addr := startProxy(t, target.Listener.Addr().String(), func(context.Context) uint64 {
		drained <- struct{}{}
		return 42
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+addr+"/2018-06-01/runtime/invocation/req-1/response", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %s", resp.Status)
	}
	got := gotSum.Load()
	if got == nil || *got != want {
		t.Fatal("the 6 MiB request body did not arrive intact")
	}
	select {
	case <-drained:
	default:
		t.Fatal("the pipes were not drained before the response was forwarded")
	}
}

func TestProxyObservesTheRequestIDAndStampsTheResult(t *testing.T) {
	var seen atomic.Pointer[string]
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/next"):
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", "req-42")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "{}")
		default:
			v := r.Header.Get(initproto.HeaderLogSeq)
			seen.Store(&v)
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer target.Close()

	tracker := &requestTracker{}
	addr := startProxyWithTracker(t, target.Listener.Addr().String(), tracker, func(context.Context) uint64 { return 77 })

	if id := tracker.current(); id != "" {
		t.Fatalf("a request was in flight before /next: %q", id)
	}

	if _, err := ricNext(addr); err != nil {
		t.Fatal(err)
	}
	if id := tracker.current(); id != "req-42" {
		t.Fatalf("after /next the current request is %q, want req-42", id)
	}
	// A line read now belongs to that invocation.
	if id := tracker.attribute(); id != "req-42" {
		t.Fatalf("attribute() = %q", id)
	}

	if err := ricRespond(addr, "req-42", "done"); err != nil {
		t.Fatal(err)
	}
	if v := seen.Load(); v == nil || *v != "77" {
		t.Fatalf("the forwarded response carried %s = %v, want 77", initproto.HeaderLogSeq, v)
	}
	if id := tracker.current(); id != "" {
		t.Fatalf("the invocation is still in flight after its response: %q", id)
	}
}

// The other half of the ordering contract: the init drains and stamps the
// sequence on GET /next too, so the host knows what the container printed
// before it asked for work — the INIT phase before the first invocation, and
// anything printed after answering the previous one before later invocations.
// Without it the host writes the next START before that output has landed, and
// CloudWatch shows INIT output *after* the first START.
func TestProxyDrainsAndStampsTheSequenceOnNext(t *testing.T) {
	var seenSeq atomic.Pointer[string]
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			v := r.Header.Get(initproto.HeaderLogSeq)
			seenSeq.Store(&v)
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", "req-7")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "{}")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	// The drain must happen *before* the request is forwarded, or the number
	// stamped on it describes a moment that has not been reached yet.
	var drained atomic.Int64
	var stampedAfterDrain atomic.Bool
	addr := startProxy(t, target.Listener.Addr().String(), func(context.Context) uint64 {
		drained.Add(1)
		stampedAfterDrain.Store(seenSeq.Load() == nil)
		return 12
	})

	if _, err := ricNext(addr); err != nil {
		t.Fatal(err)
	}
	if got := drained.Load(); got != 1 {
		t.Fatalf("the pipes were drained %d times for one /next, want 1", got)
	}
	if !stampedAfterDrain.Load() {
		t.Error("/next was forwarded before the drain, so its sequence describes a moment that had not happened")
	}
	if v := seenSeq.Load(); v == nil || *v != "12" {
		t.Fatalf("the forwarded /next carried %s = %v, want 12", initproto.HeaderLogSeq, v)
	}
}

func startProxy(t *testing.T, hostAddr string, drain func(context.Context) uint64) string {
	t.Helper()
	return startProxyWithTracker(t, hostAddr, &requestTracker{}, drain)
}

func startProxyWithTracker(t *testing.T, hostAddr string, tracker *requestTracker, drain func(context.Context) uint64) string {
	t.Helper()
	var diag lockedBuffer
	p := newProxy(hostAddr, tracker, drain, &diagLog{w: &diag})
	addr, err := p.listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		p.shutdown(ctx)
		if strings.Contains(diag.String(), "proxy error") {
			t.Errorf("the proxy reported an error:\n%s", diag.String())
		}
	})
	return addr
}

//go:build linux

package lambdainit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

// The tests in this package run the whole init in-process: real pipes, real
// child processes, a real Runtime API proxy, and an httptest server playing the
// Overcast host. Nothing here needs Docker.
//
// They must not run in parallel with one another. The init reaps with
// wait4(-1), which collects whichever child died, so two inits alive at once
// would steal each other's exit statuses — the same reason a real init is PID 1
// and alone. run() shuts its reaper down before returning, so sequential tests
// are safe.

// ---- child processes -------------------------------------------------------

// The test binary re-executes itself to play the runtime, an extension, or a
// process that just exits. That keeps every scenario in Go, with no dependency
// on what the container image happens to have in /bin.
const (
	childScenarioEnv = "OVERCAST_LAMBDAINIT_TEST_CHILD"
	childArgEnv      = "OVERCAST_LAMBDAINIT_TEST_ARG"
)

// childCoverDir is where a coverage-instrumented child writes its counters.
// Under `go test -cover` this binary is instrumented, and a re-exec'd copy that
// finds no GOCOVERDIR prints "warning: GOCOVERDIR not set, no coverage data
// emitted" to stderr on exit — one extra stderr line that every tee and frame
// assertion below would then count. Pointing it at a scratch directory keeps
// the child's stderr exactly what the scenario wrote, instrumented or not.
var childCoverDir string

func TestMain(m *testing.M) {
	if scenario := os.Getenv(childScenarioEnv); scenario != "" {
		os.Exit(runChildScenario(scenario, os.Getenv(childArgEnv)))
	}
	dir, err := os.MkdirTemp("", "lambdainit-cover-")
	if err != nil {
		panic(err)
	}
	childCoverDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// childCmd builds the command the init is asked to run: this binary, plus
// whatever stands in for the image's ENTRYPOINT arguments. Which scenario it
// plays comes from the environment, so the arguments are free to be anything a
// test wants to see arrive.
func childCmd(args ...string) []string {
	return append([]string{os.Args[0]}, args...)
}

// childEnviron is the environment the init is given, carrying the scenario the
// child should run.
func childEnviron(scenario, arg string) []string {
	return []string{
		childScenarioEnv + "=" + scenario,
		childArgEnv + "=" + arg,
		"PATH=" + os.Getenv("PATH"),
		"GOCOVERDIR=" + childCoverDir,
	}
}

func runChildScenario(scenario, arg string) int {
	switch scenario {
	case "exit":
		code, _ := strconv.Atoi(arg)
		return code

	case "echo-args":
		fmt.Fprintf(os.Stdout, "argv=%s\n", strings.Join(os.Args[1:], "|"))
		return 0

	case "print-then-exit":
		fmt.Fprint(os.Stdout, "out-one\nout-two\n")
		fmt.Fprint(os.Stderr, "err-one\n")
		code, _ := strconv.Atoi(arg)
		return code

	case "sleep":
		time.Sleep(60 * time.Second)
		return 0

	case "orphan":
		// Fork a grandchild and exit without waiting for it. Under an init (or
		// a subreaper) the grandchild's status has to be collected by the init.
		grand := exec.Command(os.Args[0])
		grand.Env = childEnviron("exit", "0")
		if err := grand.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "grandchild: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "grandchild=%d\n", grand.Process.Pid)
		return 0

	case "runtime-attribution":
		return childAttribution()

	case "runtime-bigline":
		return childBigLine()

	case "runtime-interleave":
		return childInterleave(arg)

	case "runtime-error":
		return childRuntimeError()

	default:
		fmt.Fprintf(os.Stderr, "unknown scenario %q\n", scenario)
		return 99
	}
}

// childAttribution exercises every attribution state: INIT (before the first
// /next), in-invocation, and the gap between a response and the next /next.
func childAttribution() int {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	fmt.Fprintln(os.Stdout, "init-line")

	for i := 1; i <= 2; i++ {
		id, err := ricNext(api)
		if err != nil {
			fmt.Fprintf(os.Stderr, "next: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "inv-%d-out\n", i)
		fmt.Fprintf(os.Stderr, "inv-%d-err\n", i)
		if err := ricRespond(api, id, "result-"+strconv.Itoa(i)); err != nil {
			fmt.Fprintf(os.Stderr, "respond: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "between-%d\n", i)
	}
	return 0
}

// childBigLine writes a full pipe's worth of bytes with no newline, then a
// line, then responds — the case the drain-then-forward ordering exists for.
func childBigLine() int {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	id, err := ricNext(api)
	if err != nil {
		fmt.Fprintf(os.Stderr, "next: %v\n", err)
		return 1
	}
	if _, err := os.Stdout.Write(bytes.Repeat([]byte("x"), bigLineBytes)); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		return 1
	}
	fmt.Fprint(os.Stdout, "\nfinal-line\n")
	if err := ricRespond(api, id, "ok"); err != nil {
		fmt.Fprintf(os.Stderr, "respond: %v\n", err)
		return 1
	}
	return 0
}

// bigLineBytes is a full default Linux pipe buffer, so the writer blocks unless
// the init's reader is genuinely draining.
const bigLineBytes = 64 * 1024

func childInterleave(arg string) int {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	n, _ := strconv.Atoi(arg)
	id, err := ricNext(api)
	if err != nil {
		fmt.Fprintf(os.Stderr, "next: %v\n", err)
		return 1
	}
	for i := 1; i <= n; i++ {
		fmt.Fprintf(os.Stdout, "out-%d\n", i)
		fmt.Fprintf(os.Stderr, "err-%d\n", i)
	}
	if err := ricRespond(api, id, "ok"); err != nil {
		fmt.Fprintf(os.Stderr, "respond: %v\n", err)
		return 1
	}
	return 0
}

func childRuntimeError() int {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	id, err := ricNext(api)
	if err != nil {
		fmt.Fprintf(os.Stderr, "next: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "handler blew up")
	req, err := http.NewRequest(http.MethodPost, "http://"+api+"/2018-06-01/runtime/invocation/"+id+"/error", strings.NewReader(`{"errorMessage":"boom"}`))
	if err != nil {
		return 1
	}
	req.Header.Set("Lambda-Runtime-Function-Error-Type", "Unhandled")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error post: %v\n", err)
		return 1
	}
	resp.Body.Close()
	return 0
}

func ricNext(api string) (string, error) {
	resp, err := http.Get("http://" + api + "/2018-06-01/runtime/invocation/next") //nolint:noctx // a test child, mirroring what a runtime interface client does
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return "", err
	}
	id := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
	if id == "" {
		return "", errors.New("no request id on the /next response")
	}
	return id, nil
}

func ricRespond(api, id, body string) error {
	resp, err := http.Post("http://"+api+"/2018-06-01/runtime/invocation/"+id+"/response", "application/json", strings.NewReader(body)) //nolint:noctx // a test child
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// ---- the fake Overcast host ------------------------------------------------

type invocation struct {
	id      string
	payload string
	// before runs on the /next handler before the response is written. It is
	// how a test says "do not start this invocation until the host has seen
	// that line", which removes every sleep from the attribution tests.
	before func()
}

type recordedCall struct {
	kind   string // "response" or "error"
	id     string
	logSeq string
	body   string
}

type fakeHost struct {
	t    *testing.T
	srv  *httptest.Server
	stop chan struct{}

	invocations chan invocation

	mu      sync.Mutex
	cond    *sync.Cond
	frames  []initproto.Frame // de-duplicated by seq, in arrival order
	seen    map[uint64]bool
	calls   []recordedCall
	streams int
	// dropAfter, when positive, ends the log stream once that many frames have
	// been recorded on it — a host-side connection loss, mid-stream.
	dropAfter int
	hungUp    bool
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	h := &fakeHost{
		t:           t,
		stop:        make(chan struct{}),
		invocations: make(chan invocation, 8),
		seen:        map[uint64]bool{},
	}
	h.cond = sync.NewCond(&h.mu)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2018-06-01/runtime/invocation/next", h.handleNext)
	mux.HandleFunc("POST /2018-06-01/runtime/invocation/{id}/response", h.handleResult("response"))
	mux.HandleFunc("POST /2018-06-01/runtime/invocation/{id}/error", h.handleResult("error"))
	mux.HandleFunc("POST "+initproto.LogsPath, h.handleLogs)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotImplemented)
	})

	h.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		close(h.stop)
		h.wake()
		h.srv.Close()
	})
	return h
}

func (h *fakeHost) addr() string { return strings.TrimPrefix(h.srv.URL, "http://") }

func (h *fakeHost) wake() {
	h.mu.Lock()
	h.cond.Broadcast()
	h.mu.Unlock()
}

func (h *fakeHost) enqueue(inv invocation) { h.invocations <- inv }

func (h *fakeHost) handleNext(w http.ResponseWriter, r *http.Request) {
	var inv invocation
	select {
	case inv = <-h.invocations:
	case <-h.stop:
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	}
	if inv.before != nil {
		inv.before()
	}
	w.Header().Set("Lambda-Runtime-Aws-Request-Id", inv.id)
	w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(3*time.Second).UnixMilli(), 10))
	w.Header().Set("Lambda-Runtime-Invoked-Function-Arn", "arn:aws:lambda:us-east-1:000000000000:function:test")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, inv.payload)
}

func (h *fakeHost) handleResult(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.calls = append(h.calls, recordedCall{
			kind:   kind,
			id:     r.PathValue("id"),
			logSeq: r.Header.Get(initproto.HeaderLogSeq),
			body:   string(body),
		})
		h.cond.Broadcast()
		h.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"OK"}`)
	}
}

// handleLogs consumes one NDJSON frame stream. Frames already seen are dropped,
// which is exactly what the host will do in Phase 2: the init replays its
// backlog after a reconnect and seq is the de-duplication key.
func (h *fakeHost) handleLogs(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.streams++
	limit := h.dropAfter
	h.mu.Unlock()

	done := make(chan struct{})
	drop := make(chan struct{})
	go func() {
		br := bufio.NewReader(r.Body)
		recorded := 0
		for {
			f, err := initproto.Decode(br)
			if err != nil {
				close(done)
				return
			}
			h.record(f)
			recorded++
			if limit > 0 && recorded >= limit {
				h.mu.Lock()
				h.dropAfter = 0
				h.mu.Unlock()
				close(drop)
				return
			}
		}
	}()

	select {
	case <-done:
		w.WriteHeader(http.StatusOK)
	case <-drop:
		// A connection loss, not a polite end: the init has to notice and
		// reconnect. Returning from the handler would not do it — the server
		// would sit there draining a request body that never ends.
		h.hangUp(w)
	case <-h.stop:
		h.hangUp(w)
	case <-r.Context().Done():
	}
}

// hangUp drops the TCP connection under the stream, as a restarted host or a
// broken network would.
func (h *fakeHost) hangUp(w http.ResponseWriter) {
	h.t.Helper()
	hj, ok := w.(http.Hijacker)
	if !ok {
		h.t.Error("the test server does not support hijacking")
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	conn.Close()

	h.mu.Lock()
	h.hungUp = true
	h.cond.Broadcast()
	h.mu.Unlock()
}

// awaitHangUp blocks until the host has dropped a stream.
func (h *fakeHost) awaitHangUp() {
	h.t.Helper()
	if !h.await(func() bool { return h.hungUp }) {
		h.t.Fatal("the host never dropped the log stream")
	}
}

func (h *fakeHost) record(f initproto.Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f.Gap == 0 && h.seen[f.Seq] {
		return // a replayed frame the host already has
	}
	h.seen[f.Seq] = true
	h.frames = append(h.frames, f)
	h.cond.Broadcast()
}

func (h *fakeHost) snapshotFrames() []initproto.Frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]initproto.Frame, len(h.frames))
	copy(out, h.frames)
	return out
}

func (h *fakeHost) snapshotCalls() []recordedCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]recordedCall, len(h.calls))
	copy(out, h.calls)
	return out
}

func (h *fakeHost) streamCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.streams
}

func (h *fakeHost) setDropAfter(n int) {
	h.mu.Lock()
	h.dropAfter = n
	h.mu.Unlock()
}

// waitForFrame blocks until a frame satisfying match has been recorded. Tests
// use it as a barrier instead of sleeping.
// awaitMessage blocks until a frame with this message has been recorded. It is
// safe to call from a server goroutine — an invocation's before hook uses it as
// a barrier, which is how the attribution tests avoid sleeping — so it reports
// rather than failing the test itself.
func (h *fakeHost) awaitMessage(msg string) bool {
	return h.await(func() bool {
		for _, f := range h.frames {
			if f.Msg == msg {
				return true
			}
		}
		return false
	})
}

// mustAwaitFrameCount fails the test if n frames do not arrive.
func (h *fakeHost) mustAwaitFrameCount(n int) {
	h.t.Helper()
	if !h.await(func() bool { return len(h.frames) >= n }) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.t.Fatalf("timed out waiting for %d frames after %d log stream(s); have %v", n, h.streams, frameMessages(h.frames))
	}
}

// await blocks until cond (evaluated under h.mu) holds, and reports whether it
// did before the timeout.
func (h *fakeHost) await(cond func() bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, h.wake)
	defer stop()

	h.mu.Lock()
	defer h.mu.Unlock()
	for !cond() && ctx.Err() == nil {
		h.cond.Wait()
	}
	return cond()
}

// ---- init harness ----------------------------------------------------------

// initResult is what a completed init run left behind.
type initResult struct {
	code   int
	stdout string
	stderr string
	diag   string
}

// runInit runs the init to completion with the fake host as its Runtime API,
// and returns its exit code and the three streams it wrote to.
func runInit(t *testing.T, h *fakeHost, opts options) initResult {
	t.Helper()

	var out, errOut, diag lockedBuffer
	opts.hostAddr = h.addr()
	if opts.listenAddr == "" {
		opts.listenAddr = "127.0.0.1:0"
	}
	if opts.extensionsDir == "" {
		opts.extensionsDir = t.TempDir() // empty: no extensions
	}
	opts.stdout = &out
	opts.stderr = &errOut
	opts.diag = &diag

	done := make(chan int, 1)
	go func() { done <- run(context.Background(), opts) }()

	select {
	case code := <-done:
		return initResult{code: code, stdout: out.String(), stderr: errOut.String(), diag: diag.String()}
	case <-time.After(60 * time.Second):
		t.Fatalf("the init did not exit\ndiagnostics:\n%s", diag.String())
		return initResult{}
	}
}

// lockedBuffer is a bytes.Buffer that survives being written from the reader
// goroutines while a test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ---- assertions ------------------------------------------------------------

func framesBySource(frames []initproto.Frame, src string) []initproto.Frame {
	var out []initproto.Frame
	for _, f := range frames {
		if f.Src == src {
			out = append(out, f)
		}
	}
	return out
}

func findFrame(t *testing.T, frames []initproto.Frame, msg string) initproto.Frame {
	t.Helper()
	for _, f := range frames {
		if f.Msg == msg {
			return f
		}
	}
	t.Fatalf("no frame with message %q in %v", msg, frameMessages(frames))
	return initproto.Frame{}
}

func frameMessages(frames []initproto.Frame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, fmt.Sprintf("%d:%s:%s:%q", f.Seq, f.Req, f.Src, truncate(f.Msg, 40)))
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// assertContiguous checks that the host saw every seq exactly once, in order,
// with no holes — the property replay and de-duplication have to preserve.
func assertContiguous(t *testing.T, frames []initproto.Frame) {
	t.Helper()
	var want uint64 = 1
	for _, f := range frames {
		if f.Gap != 0 {
			// A gap frame accounts for the seqs it says were lost.
			want = f.Seq + 1
			continue
		}
		if f.Seq != want {
			t.Fatalf("sequence is not contiguous: got seq %d, want %d\nframes: %v", f.Seq, want, frameMessages(frames))
		}
		want++
	}
}

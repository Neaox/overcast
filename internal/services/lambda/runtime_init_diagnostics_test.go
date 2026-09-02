package lambda

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/events"
)

// runtime_init_diagnostics_test.go covers what Overcast says when an execution
// environment fails to finish INIT.
//
// The failure it has to describe is a container that starts, imports the
// handler, and never polls for work. Before #800 the whole of Overcast's
// account of that was a Runtime.InitError quoting the init budget, which names
// the function and nothing else — no container, no endpoint, and no way to tell
// a container that could not reach the Runtime API from one that reached it and
// was refused. Both are silence, and the diagnosis went to the wrong one.

// newObservedRuntimeAPIServer is newIdentityTestServer with its log captured.
func newObservedRuntimeAPIServer(t *testing.T, level zapcore.LevelEnabler) (*RuntimeAPIServer, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(level)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewRuntimeAPIServerFromListener(ln, ln.Addr().String(), zap.New(core), clock.New())
	if err != nil {
		_ = ln.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv, logs
}

func TestContainerListener_countsWhatConnectedToTheEnvironment(t *testing.T) {
	// Given: an execution environment's own Runtime API endpoint, freshly
	// allocated and dialled by nothing.
	srv, _ := newObservedRuntimeAPIServer(t, zap.DebugLevel)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	if got := listener.Accepted(); got != 0 {
		t.Fatalf("Accepted() before any connection = %d, want 0", got)
	}

	// When: something connects to it — no request, just the connection, which
	// is all a container has managed when its RIC dies mid-handshake.
	conn, err := net.DialTimeout("tcp", listener.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", listener.Addr(), err)
	}
	defer conn.Close()

	// Then: the endpoint has recorded it. This is the datum that separates
	// "nothing could reach us" from "something did and we turned it away".
	deadline := time.Now().Add(5 * time.Second)
	for listener.Accepted() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := listener.Accepted(); got != 1 {
		t.Fatalf("Accepted() after one connection = %d, want 1", got)
	}
}

func TestRuntimeAPI_firstNextFromAnEnvironmentIsLogged(t *testing.T) {
	// Given: a registered environment that has not polled yet.
	srv, logs := newObservedRuntimeAPIServer(t, zap.DebugLevel)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	srv.RegisterContainerConfig("172.18.0.5", runtimeContainerConfig{
		FunctionARN:  identityTestARN,
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	listener.Attach("172.18.0.5")
	srv.SubmitInvocation(identityTestARN, []byte(`{}`), time.Now().Add(30*time.Second))

	// When: its RIC polls /next twice — the cold start, then the warm loop.
	resp := getIdentity(t, listener.Addr(), "/2018-06-01/runtime/invocation/next")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /next = %d, want 200", resp.StatusCode)
	}

	// Then: exactly one line records that the environment finished INIT, and it
	// names the environment and the port the call arrived on. Without it, a
	// cold start that worked and one that never made contact leave the same
	// empty log behind.
	entries := logs.FilterMessage("runtime api: execution environment finished INIT").All()
	if len(entries) != 1 {
		t.Fatalf("first-INIT log lines = %d, want 1 (all: %v)", len(entries), logs.All())
	}
	fields := entries[0].ContextMap()
	if got := fields["container_ip"]; got != "172.18.0.5" {
		t.Errorf("container_ip = %v, want 172.18.0.5", got)
	}
	if got := fields["function"]; got != "demo" {
		t.Errorf("function = %v, want demo", got)
	}
	if got, want := fields["arrived_on_port"], int64(listener.port); got != want {
		t.Errorf("arrived_on_port = %v, want %d", got, want)
	}
}

// awaitReadyPastDeadline runs AwaitReady on an environment nothing will ever
// make ready, with the init budget already spent.
func awaitReadyPastDeadline(t *testing.T, ci *containerInstance) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := ci.AwaitReady(ctx); err == nil {
		t.Fatal("AwaitReady() = nil, want the init budget to expire")
	}
}

func TestContainerInstance_initTimeoutNamesAnEndpointNothingReached(t *testing.T) {
	// Given: an environment holding its own Runtime API endpoint, which its
	// container never connects to.
	srv, logs := newObservedRuntimeAPIServer(t, zap.WarnLevel)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	ci := &containerInstance{
		functionName: "diag",
		containerIP:  "172.18.0.2",
		rapiListener: listener,
		logger:       srv.logger,
		readyCh:      make(chan struct{}),
	}

	// When: the init budget expires.
	awaitReadyPastDeadline(t, ci)

	// Then: the diagnostic names the address the container was handed — the
	// per-environment one, which appears in no other log line — and reports
	// that nothing ever arrived there.
	entries := logs.FilterMessageSnippet("never reached its Runtime API endpoint").All()
	if len(entries) != 1 {
		t.Fatalf("INIT-timeout log lines = %d, want 1 (all: %v)", len(entries), logs.All())
	}
	fields := entries[0].ContextMap()
	if got := fields["runtime_api"]; got != listener.Addr() {
		t.Errorf("runtime_api = %v, want %v", got, listener.Addr())
	}
	if got := fields["runtime_api_connections"]; got != int64(0) {
		t.Errorf("runtime_api_connections = %v, want 0", got)
	}
	if got := fields["container_ip"]; got != "172.18.0.2" {
		t.Errorf("container_ip = %v, want 172.18.0.2", got)
	}
	// An empty container log is the normal state for an AWS base image, whose
	// RIC prints nothing before it runs the handler. Saying so is the point:
	// read as suppressed output it sends the diagnosis somewhere there is
	// nothing to find.
	if got := fields["container_output"]; got != "(the container printed nothing)" {
		t.Errorf("container_output = %v, want the no-output note", got)
	}
}

func TestContainerInstance_initTimeoutDistinguishesARuntimeThatConnected(t *testing.T) {
	// Given: the same environment, whose container did reach the endpoint.
	srv, logs := newObservedRuntimeAPIServer(t, zap.WarnLevel)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	conn, err := net.DialTimeout("tcp", listener.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", listener.Addr(), err)
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for listener.Accepted() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	ci := &containerInstance{
		functionName: "diag",
		containerIP:  "172.18.0.2",
		rapiListener: listener,
		logger:       srv.logger,
		readyCh:      make(chan struct{}),
	}

	// When: the init budget expires anyway.
	awaitReadyPastDeadline(t, ci)

	// Then: the diagnostic says so, which rules the container's route back to
	// this host out and leaves identity.
	entries := logs.FilterMessageSnippet("reached its Runtime API endpoint but never polled").All()
	if len(entries) != 1 {
		t.Fatalf("INIT-timeout log lines = %d, want 1 (all: %v)", len(entries), logs.All())
	}
	if got := entries[0].ContextMap()["runtime_api_connections"]; got != int64(1) {
		t.Errorf("runtime_api_connections = %v, want 1", got)
	}
}

func TestContainerInstance_abandonedInvokeIsNotDiagnosedAsAnINITTimeout(t *testing.T) {
	// Given: an environment still initialising.
	srv, logs := newObservedRuntimeAPIServer(t, zap.WarnLevel)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	ci := &containerInstance{
		functionName: "diag",
		rapiListener: listener,
		logger:       srv.logger,
		readyCh:      make(chan struct{}),
	}

	// When: the caller gives up rather than the budget running out — an
	// abandoned invoke, or a shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ci.AwaitReady(ctx); err == nil {
		t.Fatal("AwaitReady() = nil, want the cancellation")
	}

	// Then: nothing is diagnosed. There is no fault here to explain, and a
	// warning per cancelled invoke is how a useful line stops being read.
	if got := logs.Len(); got != 0 {
		t.Fatalf("log lines = %d, want 0 (all: %v)", got, logs.All())
	}
}

// A container that dies during INIT is the other way INIT ends, and it used to
// be explained by nothing at all: the caller got an exit code and the log got
// no line, even though the same evidence the timeout branch quotes was sitting
// right there. The case that costs most is a container that cannot route back
// to this host — its own [overcast-init] output names the address and the
// timeout, but that output travels over the connection that is broken, so the
// daemon's copy is where it survives.
func TestContainerInstance_initExitIsDiagnosedWithTheSameEvidence(t *testing.T) {
	// Given: an environment whose container is about to die during INIT.
	srv, logs := newObservedRuntimeAPIServer(t, zap.WarnLevel)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	notifier := newExitNotifier()
	ci := &containerInstance{
		id:           "0123456789abcdef",
		functionName: "diag",
		containerIP:  "172.18.0.2",
		rapiListener: listener,
		exitNotify:   notifier,
		logger:       srv.logger,
		readyCh:      make(chan struct{}),
	}

	// When: the daemon reports it gone with a signal exit code.
	done := make(chan error, 1)
	go func() { done <- ci.AwaitReady(context.Background()) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		notifier.mu.Lock()
		registered := len(notifier.chans) == 1
		notifier.mu.Unlock()
		if registered || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	notifier.handleContainerDied(context.Background(), events.Event{
		Payload: events.DockerContainerPayload{
			Service:     "lambda",
			ContainerID: ci.id,
			ExitCode:    "139",
		},
	})
	if err := <-done; err == nil {
		t.Fatal("AwaitReady() = nil, want the container's exit")
	}

	// Then: the exit is explained with the endpoint, what reached it, and what
	// the container printed — not with the exit code alone.
	entries := logs.FilterMessageSnippet("exited during INIT").All()
	if len(entries) != 1 {
		t.Fatalf("INIT-exit log lines = %d, want 1 (all: %v)", len(entries), logs.All())
	}
	fields := entries[0].ContextMap()
	if got := fields["exit_code"]; got != "139" {
		t.Errorf("exit_code = %v, want 139", got)
	}
	if got := fields["runtime_api"]; got != listener.Addr() {
		t.Errorf("runtime_api = %v, want %v", got, listener.Addr())
	}
	if got := fields["runtime_api_connections"]; got != int64(0) {
		t.Errorf("runtime_api_connections = %v, want 0", got)
	}
	if got := fields["container_ip"]; got != "172.18.0.2" {
		t.Errorf("container_ip = %v, want 172.18.0.2", got)
	}
	if _, ok := fields["container_output"]; !ok {
		t.Errorf("the diagnostic carries no container_output: %v", fields)
	}
}

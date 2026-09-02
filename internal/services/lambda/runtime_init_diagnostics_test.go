package lambda

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/containerendpoint"
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

func TestContainerInstance_initExitCarriesTheReachabilityVerdict(t *testing.T) {
	// Given: an environment on a host where the reachability probe already
	// established that no address works (#1572). Everything below is what the
	// exit-139 line was missing: the number names the runtime, and the cause is
	// the network.
	srv, logs := newObservedRuntimeAPIServer(t, zap.WarnLevel)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	reach := containerendpoint.Listen{
		ContainerHost: "192.168.8.19",
		Mode:          "wildcard",
		Unreachable:   true,
		Attempts: []containerendpoint.Attempt{
			{Mode: "docker-internal", Host: "host.docker.internal", Error: "wget: download timed out"},
			{Mode: "host", Host: "192.168.8.19", Error: "wget: download timed out"},
		},
	}
	ci := &containerInstance{
		id:           "0123456789abcdef",
		functionName: "diag",
		containerIP:  "172.18.0.2",
		rapiListener: listener,
		logger:       srv.logger,
		reach:        reach,
	}

	// When: the exit is diagnosed.
	ci.logInitExit("139")

	// Then: the line says how the address was chosen, that nothing was ever
	// seen to reach it, which candidates were tried, and what to do.
	entries := logs.FilterMessageSnippet("exited during INIT").All()
	if len(entries) != 1 {
		t.Fatalf("INIT-exit log lines = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if got := fields["runtime_api_mode"]; got != "wildcard" {
		t.Errorf("runtime_api_mode = %v, want wildcard", got)
	}
	if got := fields["runtime_api_container_verified"]; got != false {
		t.Errorf("runtime_api_container_verified = %v, want false", got)
	}
	candidates := fmt.Sprint(fields["runtime_api_candidates"])
	for _, want := range []string{"host.docker.internal", "192.168.8.19"} {
		if !strings.Contains(candidates, want) {
			t.Errorf("runtime_api_candidates = %s, want %q among them", candidates, want)
		}
	}
	diagnosis, _ := fields["runtime_api_diagnosis"].(string)
	if !strings.Contains(diagnosis, "LAMBDA_RUNTIME_API_HOST") {
		t.Errorf("runtime_api_diagnosis does not name the override:\n%s", diagnosis)
	}
}

func TestContainerInstance_initExitHintExplainsA139ToTheCaller(t *testing.T) {
	// Given: the same host. The user's Invoke gets a Runtime.InitError, and for
	// many of them it is the only place they will look — "exit code 139" alone
	// reads as SIGSEGV in the runtime, which is the wrong subsystem.
	unreachable := &containerInstance{reach: containerendpoint.Listen{
		Unreachable: true,
		Attempts:    []containerendpoint.Attempt{{Mode: "host", Host: "192.168.8.19", Error: "wget: download timed out"}},
	}}
	if got := unreachable.initExitHint(); !strings.Contains(got, "192.168.8.19") {
		t.Errorf("initExitHint() = %q, want the address in it", got)
	}

	// And on a host where the address was established, the error stays the
	// terse one: appending a network diagnosis to an exit that has nothing to
	// do with the network would send the next reader down the wrong path.
	verified := &containerInstance{reach: containerendpoint.Listen{
		ContainerHost: "172.19.0.1", Verified: true}}
	if got := verified.initExitHint(); got != "" {
		t.Errorf("initExitHint() = %q, want empty on a verified address", got)
	}
}

func TestContainerInstance_forgetsARememberedAddressThisContainerDisproved(t *testing.T) {
	dir := t.TempDir()
	path := containerendpoint.HintPath(dir, "overcast_control")

	newInstance := func(t *testing.T, mode string) (*containerInstance, *containerListener) {
		t.Helper()
		srv, _ := newObservedRuntimeAPIServer(t, zap.WarnLevel)
		listener, err := srv.AddContainerListener()
		if err != nil {
			t.Fatalf("AddContainerListener() error = %v", err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		return &containerInstance{
			functionName:  "diag",
			rapiListener:  listener,
			logger:        srv.logger,
			reachHintPath: path,
			reach:         containerendpoint.Listen{Mode: mode, ContainerHost: "10.0.0.9", Verified: true},
		}, listener
	}

	// Given: an address taken from the remembered probe result, and a container
	// that died during INIT having opened no connection to it.
	writeTestHint(t, path)
	hinted, _ := newInstance(t, "hinted:host")
	hinted.logInitExit("139")

	// Then: the file is gone, so the next startup probes again rather than
	// inheriting an answer this container just disproved.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the remembered address survived a container that could not reach it (stat err = %v)", err)
	}

	// And: an address this run probed for itself is left alone. Deleting a file
	// neither the probe nor a pin reads would only hide which of them happened.
	writeTestHint(t, path)
	probed, _ := newInstance(t, "gateway")
	probed.logInitExit("139")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a hint was dropped for an address this run probed itself: %v", err)
	}
}

// writeTestHint puts a file where the remembered probe result lives. Its
// contents do not matter here — what is under test is whether the file survives.
func writeTestHint(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"daemon":"d","network":"n","mode":"host","containerHost":"10.0.0.9","bindHosts":["10.0.0.9"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestContainerInstance_keepsTheRememberedAddressWhenAContainerDidReachIt(t *testing.T) {
	// Given: an address taken from the remembered probe result, and a container
	// that died during INIT *after* something reached its endpoint — a broken
	// handler, not a broken route.
	dir := t.TempDir()
	path := containerendpoint.HintPath(dir, "overcast_control")
	writeTestHint(t, path)

	srv, _ := newObservedRuntimeAPIServer(t, zap.WarnLevel)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	// Something connects, which is the whole distinction: the route works.
	conn, err := net.DialTimeout("tcp", listener.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", listener.Addr(), err)
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for listener.Accepted() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if listener.Accepted() == 0 {
		t.Fatal("the test connection was never accepted")
	}

	ci := &containerInstance{
		functionName:  "diag",
		rapiListener:  listener,
		logger:        srv.logger,
		reachHintPath: path,
		reach:         containerendpoint.Listen{Mode: "hinted:host", ContainerHost: "10.0.0.9", Verified: true},
	}

	// When: the exit is diagnosed.
	ci.logInitExit("139")

	// Then: the remembered address survives. Throwing away a measurement that
	// this container just demonstrated is correct would make every crashing
	// handler cost a probe.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the remembered address was discarded although a container reached it: %v", err)
	}
}

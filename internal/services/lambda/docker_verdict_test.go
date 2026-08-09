package lambda

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// verdictTestService builds a Service that has not run any Docker
// initialisation, with the re-probe loop wound down to something a test can
// wait for. The returned function stops it, and is registered as a cleanup so
// a test that never stops it explicitly still releases the tracker's sweeper.
func verdictTestService(t *testing.T, probe dockerProbeFunc) (*Service, func()) {
	t.Helper()
	clk := clock.NewMock()
	tracker := newInstanceTracker(clk, zap.NewNop())
	s := &Service{
		clk:                 clk,
		log:                 serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		handler:             &Handler{},
		tracker:             tracker,
		stop:                make(chan struct{}),
		probe:               probe,
		dockerRetryInterval: time.Millisecond,
	}
	var once sync.Once
	stop := func() { once.Do(func() { s.Stop(context.Background()) }) }
	t.Cleanup(stop)
	return s, stop
}

// verdictWindow is how long a test lets the wrong answer happen before it
// concludes that the right one is being waited for. It bounds a negative
// assertion only — a slow machine makes it vacuous, never failing.
const verdictWindow = 200 * time.Millisecond

// TestInvokeFunction_dockerVerdictStillPending is the regression test for the
// CI failure on PR #770 and for the user-facing bug behind it.
//
// The Lambda service registers the stub NodeRuntime synchronously and probes
// Docker on a background goroutine, so for the first moments of a process's
// life the only runtime registered is the one whose answer to every invocation
// is "Docker is not available". On the run that failed, the emulator's own log
// recorded "Docker available" at 04:26:44.932 and the invocation was answered
// by the stub at 04:26:45.024 — Docker was up, the probe had succeeded, and the
// container runtime simply had not been installed yet.
//
// An invocation that arrives before the verdict must wait for it. Reporting
// that Docker is missing is a guess at that point, and blaming the user's
// Docker installation for the emulator's own startup ordering is the worst
// possible guess to get wrong.
func TestInvokeFunction_dockerVerdictStillPending(t *testing.T) {
	// Given: a handler whose runtime registry still holds only the stub,
	// because the Docker probe has not reported yet.
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)

	rr := newPendingRuntimeRegistry([]Runtime{newNodeRuntime(clk, zap.NewNop())})
	h := &Handler{
		cfg:      &config.Config{Region: "us-east-1"},
		log:      serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk:      clk,
		ls:       ls,
		tracker:  tracker,
		runtimes: rr,
	}
	fn := &Function{
		Name:       "verdict-fn",
		ARN:        "arn:aws:lambda:us-east-1:000000000000:function:verdict-fn",
		Runtime:    "nodejs20.x",
		Handler:    "index.handler",
		Timeout:    3,
		MemorySize: 128,
		State:      "Active",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed function: %s", aerr.Message)
	}

	// When: the function is invoked, and the container runtime is installed
	// while that invocation is in flight — the startup ordering the CI job hit.
	req := httptest.NewRequest(http.MethodPost,
		"/2015-03-31/functions/verdict-fn/invocations", bytes.NewReader([]byte(`{"ping":"pong"}`)))
	req = withFunctionNameParam(req, "verdict-fn")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.InvokeFunction(rec, req)
	}()

	select {
	case <-done:
		t.Fatalf("the invocation was answered before the Docker verdict was in: %s", rec.Body.String())
	case <-time.After(verdictWindow):
	}

	inst := &blockingInstance{invoking: make(chan struct{}), release: make(chan struct{})}
	close(inst.release)
	rr.set([]Runtime{&stubRuntime{inst: inst}})
	rr.settle()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("InvokeFunction did not return after the verdict")
	}

	// Then: the real runtime ran it. The stub's Runtime.DockerUnavailable would
	// be a lie about the host, told because the emulator asked itself too early.
	if got := rec.Header().Get("X-Amz-Function-Error"); got != "" {
		t.Fatalf("X-Amz-Function-Error = %q, want none: %s", got, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Runtime.DockerUnavailable") {
		t.Fatalf("invocation answered with the Docker-unavailable stub before the probe reported: %s",
			rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("body = %s, want the handler's own return value", rec.Body.String())
	}
}

// TestRuntimeFor_waitsForTheVerdict is the same defect one layer down, where
// the selection actually happens.
func TestRuntimeFor_waitsForTheVerdict(t *testing.T) {
	// Given: a registry that still holds only the stub, because the probe has
	// not reported.
	clk := clock.NewMock()
	stub := newNodeRuntime(clk, zap.NewNop())
	rr := newPendingRuntimeRegistry([]Runtime{stub})

	// When: a runtime is selected.
	got := make(chan Runtime, 1)
	go func() { got <- rr.runtimeFor(context.Background(), "nodejs20.x") }()

	select {
	case rt := <-got:
		t.Fatalf("runtimeFor chose %T before the verdict was in", rt)
	case <-time.After(verdictWindow):
	}

	container := &stubRuntime{}
	rr.set([]Runtime{container})
	rr.settle()

	// Then: the runtime it chose is the one the verdict installed.
	select {
	case rt := <-got:
		if rt != Runtime(container) {
			t.Fatalf("runtimeFor = %T, want the runtime installed by the verdict", rt)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runtimeFor never returned after the verdict")
	}
}

// TestRuntimeFor_settledRegistryDoesNotWait pins the other half: once the
// verdict is in, selection is a plain lookup. A registry built with a known
// runtime set — which is every construction site except Service.New — must
// never make a caller wait for a probe that will not run.
func TestRuntimeFor_settledRegistryDoesNotWait(t *testing.T) {
	// Given: a registry whose runtime set is final.
	clk := clock.NewMock()
	rr := newRuntimeRegistry([]Runtime{newNodeRuntime(clk, zap.NewNop())})

	// When: a runtime is selected with a context that is already cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Then: it resolves anyway, from the set that is already known.
	if rt := rr.runtimeFor(ctx, "nodejs20.x"); rt == nil {
		t.Fatal("runtimeFor returned nil for a runtime the registry can handle")
	}
	if rt := rr.runtimeFor(context.Background(), "python3.12"); rt != nil {
		t.Fatalf("runtimeFor returned %T for a runtime nothing registered handles", rt)
	}
}

// TestInitDockerRuntime_noSocketSettlesTheRegistry guards the deadlock the
// waiting introduces if a path ever forgets to publish its verdict: an
// unsettled registry with no probe behind it parks every invocation forever.
// A test server configures no socket, so this is the common path.
func TestInitDockerRuntime_noSocketSettlesTheRegistry(t *testing.T) {
	// Given: a service with no Docker socket configured.
	s, _ := verdictTestService(t, func(string, string, *zap.Logger) (*docker.ProbeResult, error) {
		t.Error("probe called despite an empty LambdaDockerSocket")
		return nil, nil
	})
	rr := newPendingRuntimeRegistry([]Runtime{newNodeRuntime(s.clk, zap.NewNop())})

	// When: Docker initialisation runs.
	s.initDockerRuntime(&config.Config{}, s.clk, rr)

	// Then: the verdict is published, so invocations answer immediately.
	select {
	case <-rr.settled:
	default:
		t.Fatal("registry left unsettled with no probe to settle it")
	}
}

// TestAwaitDockerProbe_retriesUntilDockerAnswers covers the recovery half.
// Starting Overcast before Docker Desktop has finished booting is an ordinary
// sequence on Windows and macOS; before this loop existed the startup probe's
// failure was latched for the life of the process and the only recovery was to
// restart the emulator — which is what the stub's error message used to advise.
func TestAwaitDockerProbe_retriesUntilDockerAnswers(t *testing.T) {
	// Given: a Docker daemon that is not there for the first two attempts.
	var calls atomic.Int32
	want := &docker.ProbeResult{}
	s, _ := verdictTestService(t, func(string, string, *zap.Logger) (*docker.ProbeResult, error) {
		if calls.Add(1) < 3 {
			return nil, context.DeadlineExceeded
		}
		return want, nil
	})

	// When: the service waits for it.
	got := s.awaitDockerProbe(&config.Config{LambdaDockerSocket: "/var/run/docker.sock"},
		clock.New(), zap.NewNop())

	// Then: the probe that finally answered is the one returned.
	if got != want {
		t.Fatalf("awaitDockerProbe = %v, want the successful probe result", got)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("probe attempts = %d, want 3", n)
	}
}

// TestAwaitDockerProbe_stopsWithTheService pins that the loop is not a leak:
// a Docker daemon that never appears must not keep a goroutine alive past
// shutdown.
func TestAwaitDockerProbe_stopsWithTheService(t *testing.T) {
	// Given: a Docker daemon that never answers.
	probed := make(chan struct{}, 1)
	s, stop := verdictTestService(t, func(string, string, *zap.Logger) (*docker.ProbeResult, error) {
		select {
		case probed <- struct{}{}:
		default:
		}
		return nil, context.DeadlineExceeded
	})

	done := make(chan *docker.ProbeResult, 1)
	go func() {
		done <- s.awaitDockerProbe(&config.Config{LambdaDockerSocket: "/var/run/docker.sock"},
			clock.New(), zap.NewNop())
	}()

	// When: the service stops after the loop has run at least once.
	select {
	case <-probed:
	case <-time.After(10 * time.Second):
		t.Fatal("re-probe loop never ran")
	}
	stop()

	// Then: the loop returns, reporting that Docker never arrived.
	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("awaitDockerProbe = %v, want nil after Stop", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("re-probe loop outlived Stop")
	}
}

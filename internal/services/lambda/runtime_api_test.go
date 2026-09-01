package lambda

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
)

func TestRuntimeAPI_OnFirstNextCalledOnce(t *testing.T) {
	// Given: a RuntimeAPI server with an OnFirstNext callback.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListener(ln, addr, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	var callCount atomic.Int32
	var lastARN, lastContainer atomic.Value
	srv.OnFirstNext = func(arn, containerID string) {
		callCount.Add(1)
		lastARN.Store(arn)
		lastContainer.Store(containerID)
	}

	// Register a fake container IP, with the Docker container behind it —
	// what the INIT-burst throttle acts on.
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
		FunctionName: "my-fn",
		ContainerID:  "container-my-fn",
	})

	// Submit an invocation so the first /next returns immediately.
	srv.SubmitInvocation(
		"arn:aws:lambda:us-east-1:000000000000:function:my-fn",
		[]byte(`{}`),
		time.Now().Add(30*time.Second),
	)

	// When: a container polls GET /next.
	resp, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Allow the goroutine to fire.
	time.Sleep(50 * time.Millisecond)

	// Then: the callback was called exactly once with the correct ARN.
	if got := callCount.Load(); got != 1 {
		t.Fatalf("OnFirstNext called %d times, want 1", got)
	}
	if got, _ := lastARN.Load().(string); got != "arn:aws:lambda:us-east-1:000000000000:function:my-fn" {
		t.Fatalf("OnFirstNext ARN = %q, want my-fn ARN", got)
	}
	// And with the environment that reported it: a function can have several
	// in INIT at once, so the ARN alone does not say which container this was.
	if got, _ := lastContainer.Load().(string); got != "container-my-fn" {
		t.Fatalf("OnFirstNext container = %q, want container-my-fn", got)
	}

	// Submit another invocation for a second /next call.
	srv.SubmitInvocation(
		"arn:aws:lambda:us-east-1:000000000000:function:my-fn",
		[]byte(`{}`),
		time.Now().Add(30*time.Second),
	)

	resp2, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	time.Sleep(50 * time.Millisecond)

	// Then: the callback was NOT called again.
	if got := callCount.Load(); got != 1 {
		t.Fatalf("OnFirstNext called %d times after second /next, want still 1", got)
	}
}

func TestRuntimeAPI_ReadyChanClosesOnFirstNext(t *testing.T) {
	// Given: a RuntimeAPI server with a registered container.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListener(ln, addr, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })
	srv.RegisterContainer("127.0.0.1", "arn:aws:lambda:us-east-1:000000000000:function:my-fn")
	ready := srv.ReadyChan("127.0.0.1")
	srv.SubmitInvocation(
		"arn:aws:lambda:us-east-1:000000000000:function:my-fn",
		[]byte(`{}`),
		time.Now().Add(30*time.Second),
	)

	// When: the container polls GET /next for the first time.
	resp, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Then: the ready channel is closed.
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("ready channel was not closed after first /next")
	}
}

func TestRuntimeAPI_ReadyChanResetsAfterUnregister(t *testing.T) {
	// Given: a RuntimeAPI server and an initial registration.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListener(ln, addr, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })
	srv.RegisterContainer("127.0.0.1", "arn:aws:lambda:us-east-1:000000000000:function:my-fn")
	first := srv.ReadyChan("127.0.0.1")

	// When: the container is unregistered and the same IP is registered again.
	srv.UnregisterContainer("127.0.0.1")
	srv.RegisterContainer("127.0.0.1", "arn:aws:lambda:us-east-1:000000000000:function:my-fn")
	second := srv.ReadyChan("127.0.0.1")

	// Then: readiness for the new registration uses a fresh channel.
	if first == second {
		t.Fatal("ReadyChan reused the old channel after unregister/register")
	}
}

func TestRuntimeAPI_OnFirstNextResetsAfterUnregister(t *testing.T) {
	// Given: a RuntimeAPI server with an OnFirstNext callback.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListener(ln, addr, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	var callCount atomic.Int32
	srv.OnFirstNext = func(string, string) {
		callCount.Add(1)
	}

	// First cycle: register, /next, unregister.
	srv.RegisterContainer("127.0.0.1", "arn:aws:lambda:us-east-1:000000000000:function:my-fn")
	srv.SubmitInvocation(
		"arn:aws:lambda:us-east-1:000000000000:function:my-fn",
		[]byte(`{}`),
		time.Now().Add(30*time.Second),
	)
	resp, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	time.Sleep(50 * time.Millisecond)

	if got := callCount.Load(); got != 1 {
		t.Fatalf("first cycle: OnFirstNext called %d times, want 1", got)
	}

	srv.UnregisterContainer("127.0.0.1")

	// Second cycle: re-register the same IP — should fire callback again.
	srv.RegisterContainer("127.0.0.1", "arn:aws:lambda:us-east-1:000000000000:function:my-fn")
	srv.SubmitInvocation(
		"arn:aws:lambda:us-east-1:000000000000:function:my-fn",
		[]byte(`{}`),
		time.Now().Add(30*time.Second),
	)
	resp2, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	time.Sleep(50 * time.Millisecond)

	// Then: callback fired again after re-register.
	if got := callCount.Load(); got != 2 {
		t.Fatalf("second cycle: OnFirstNext called %d times, want 2", got)
	}
}

func TestRuntimeAPI_FirstNextAtRecordedOncePerRegistration(t *testing.T) {
	// Given: a RuntimeAPI server with a registered container.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListener(ln, addr, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })
	arn := "arn:aws:lambda:us-east-1:000000000000:function:my-fn"
	srv.RegisterContainer("127.0.0.1", arn)

	// Then: no timestamp before the RIC's first poll.
	if _, ok := srv.FirstNextAt("127.0.0.1"); ok {
		t.Fatal("FirstNextAt set before any GET /next")
	}

	// When: the container polls GET /next twice.
	srv.SubmitInvocation(arn, []byte(`{}`), time.Now().Add(30*time.Second))
	resp, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	first, ok := srv.FirstNextAt("127.0.0.1")
	if !ok {
		t.Fatal("FirstNextAt not recorded after first GET /next")
	}

	srv.SubmitInvocation(arn, []byte(`{}`), time.Now().Add(30*time.Second))
	resp2, err := http.Get("http://" + addr + "/2018-06-01/runtime/invocation/next")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	// Then: the timestamp pins the FIRST poll — later polls don't move it.
	if second, _ := srv.FirstNextAt("127.0.0.1"); !second.Equal(first) {
		t.Fatalf("FirstNextAt moved on second GET /next: %v → %v", first, second)
	}

	// And: unregistering clears it for the next environment on this IP.
	srv.UnregisterContainer("127.0.0.1")
	if _, ok := srv.FirstNextAt("127.0.0.1"); ok {
		t.Fatal("FirstNextAt survived UnregisterContainer")
	}
}

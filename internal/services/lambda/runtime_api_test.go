package lambda

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
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
	var lastARN atomic.Value
	srv.OnFirstNext = func(arn string) {
		callCount.Add(1)
		lastARN.Store(arn)
	}

	// Register a fake container IP.
	srv.RegisterContainer("127.0.0.1", "arn:aws:lambda:us-east-1:000000000000:function:my-fn")

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
	srv.OnFirstNext = func(arn string) {
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

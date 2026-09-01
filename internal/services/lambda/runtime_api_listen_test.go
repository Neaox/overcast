package lambda

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
)

// runtime_api_listen_test.go covers binding the Runtime API to the set of
// addresses containers can actually reach it on, rather than to every interface.
// containerendpoint.ResolveListen decides the set; these are the mechanics of
// holding it — one port across several addresses, and one server behind them.

// closeAll releases listeners a test opened.
func closeAll(lns []net.Listener) {
	for _, ln := range lns {
		_ = ln.Close()
	}
}

// ipv6LoopbackOrSkip returns "::1" when this machine can bind it, and skips
// otherwise: a second loopback address is the only second address portable
// across Windows, macOS and Linux, and a container with IPv6 disabled has none.
func ipv6LoopbackOrSkip(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback on this machine: %v", err)
	}
	_ = ln.Close()
	return "::1"
}

func TestListenAllOn_bindsEveryAddressOnOnePort(t *testing.T) {
	// Given: two addresses this machine holds, and a port of 0 — which is what
	// the test server uses so parallel packages do not collide.
	hosts := []string{"127.0.0.1", ipv6LoopbackOrSkip(t)}

	// When: the set is bound.
	lns, err := listenAllOn(hosts, 0, zap.NewNop())
	if err != nil {
		t.Fatalf("listenAllOn() error = %v", err)
	}
	defer closeAll(lns)

	// Then: every address is listening, and on the *same* port. Each taking its
	// own OS-assigned port would leave containers pointed at whichever one the
	// container address happened to be built from.
	if len(lns) != len(hosts) {
		t.Fatalf("bound %d listeners, want %d", len(lns), len(hosts))
	}
	first := lns[0].Addr().(*net.TCPAddr).Port
	if first == 0 {
		t.Fatal("port not resolved from the first listener")
	}
	for i, ln := range lns {
		addr := ln.Addr().(*net.TCPAddr)
		if addr.Port != first {
			t.Errorf("listener %d on port %d, want %d", i, addr.Port, first)
		}
	}
}

func TestListenAllOn_dropsASecondaryAddressItCannotBind(t *testing.T) {
	// Given: a bindable primary followed by TEST-NET-1, which is not an address
	// of this machine — standing in for loopback already being held by
	// something else.
	hosts := []string{"127.0.0.1", "192.0.2.1"}

	// When: the set is bound.
	lns, err := listenAllOn(hosts, 0, zap.NewNop())
	if err != nil {
		t.Fatalf("listenAllOn() error = %v, want the primary to carry the run", err)
	}
	defer closeAll(lns)

	// Then: the primary stands. The extra addresses are conveniences; the one
	// containers dial is bound first precisely so a failure on the others is
	// not a reason to leave Lambda without a runtime.
	if len(lns) != 1 {
		t.Fatalf("bound %d listeners, want 1", len(lns))
	}
	if got := lns[0].Addr().(*net.TCPAddr).IP.String(); got != "127.0.0.1" {
		t.Errorf("bound %q, want 127.0.0.1", got)
	}
}

func TestListenAllOn_failsWhenTheAddressContainersDialIsTaken(t *testing.T) {
	// Given: something already holding the port on the primary address.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = held.Close() }()
	port := held.Addr().(*net.TCPAddr).Port

	// When: the same address is asked for.
	lns, err := listenAllOn([]string{"127.0.0.1"}, port, zap.NewNop())

	// Then: it is an error, not a silent partial bind. The caller disables the
	// container runtime and says why, rather than starting containers that
	// cannot report a result.
	if err == nil {
		closeAll(lns)
		t.Fatal("listenAllOn() error = nil, want the primary bind to fail")
	}
	if len(lns) != 0 {
		closeAll(lns)
		t.Errorf("returned %d listeners alongside an error, want none", len(lns))
	}
}

func TestListenAllOn_refusesAnEmptySet(t *testing.T) {
	// Given/When: no address at all — a resolver bug rather than a network one.
	lns, err := listenAllOn(nil, 0, zap.NewNop())

	// Then: it says so instead of binding something arbitrary.
	if err == nil {
		closeAll(lns)
		t.Fatal("listenAllOn(nil) error = nil, want an error")
	}
}

func TestRuntimeAPIServer_answersOnEveryAddressItBound(t *testing.T) {
	// Given: a Runtime API server fronting both loopback addresses.
	hosts := []string{"127.0.0.1", ipv6LoopbackOrSkip(t)}
	lns, err := listenAllOn(hosts, 0, zap.NewNop())
	if err != nil {
		t.Fatalf("listenAllOn() error = %v", err)
	}

	srv, err := NewRuntimeAPIServerFromListeners(lns, "container-addr:9001", defaultLambdaInitTimeout, zap.NewNop(), clock.New())
	if err != nil {
		closeAll(lns)
		t.Fatalf("NewRuntimeAPIServerFromListeners() error = %v", err)
	}
	// Stop is not idempotent (it closes a channel), so the safety net here
	// releases the listeners rather than calling it a second time.
	defer closeAll(lns)

	// When: an unknown path is requested on each of them.
	client := &http.Client{Timeout: 5 * time.Second}
	for _, ln := range lns {
		url := fmt.Sprintf("http://%s/2018-06-01/runtime/nope", ln.Addr().String())
		resp, reqErr := client.Get(url) //nolint:noctx // short-lived probe against a local listener
		if reqErr != nil {
			t.Fatalf("GET %s: %v", url, reqErr)
		}
		// Then: the same mux answers — one server behind every address, so a
		// RIC gets an identical response whichever one it reached.
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d", url, resp.StatusCode, http.StatusNotFound)
		}
		_ = resp.Body.Close()
	}

	// And: shutting down closes all of them, not just the first.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if stopErr := srv.Stop(ctx); stopErr != nil {
		t.Fatalf("Stop() error = %v", stopErr)
	}
	for _, ln := range lns {
		if _, dialErr := net.DialTimeout("tcp", ln.Addr().String(), time.Second); dialErr == nil {
			t.Errorf("%s still accepting connections after Stop", ln.Addr())
		}
	}
}

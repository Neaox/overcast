package router

import (
	"net"
	"strconv"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/listenstatus"
	"github.com/overcast-sh/overcast/internal/services/lambda"
	"github.com/overcast-sh/overcast/internal/smtp"
)

// holdLoopbackPort binds an ephemeral loopback port for the life of the test
// and returns it, standing in for a second Overcast that already owns the
// default.
func holdLoopbackPort(t *testing.T) int {
	t.Helper()
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	return holder.Addr().(*net.TCPAddr).Port
}

func TestListenSMTPMock_defaultPortBusyFallsBackToEphemeral(t *testing.T) {
	// Given: the default port is held by another process
	busy := holdLoopbackPort(t)

	// When: the capture server is bound at that default
	srv, addr, fellBack, err := listenSMTPMock(smtp.NewMailStore(10), busy, busy, zap.NewNop())

	// Then: an ephemeral loopback port was taken instead, and the fallback is reported
	if err != nil {
		t.Fatalf("listenSMTPMock: %v", err)
	}
	defer srv.Close()
	if !fellBack {
		t.Fatal("fellBack = false, want true")
	}
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		t.Fatalf("bound addr %q: %v", addr, splitErr)
	}
	if host != "127.0.0.1" {
		t.Fatalf("bound host = %q, want loopback", host)
	}
	if port == "0" || port == strconv.Itoa(busy) {
		t.Fatalf("bound port = %s, want an ephemeral port other than the busy %d", port, busy)
	}
}

func TestListenSMTPMock_pinnedPortBusyFails(t *testing.T) {
	// Given: a port set explicitly (it is not the default) is held by another process
	busy := holdLoopbackPort(t)

	// When: the capture server is bound at it
	_, _, fellBack, err := listenSMTPMock(smtp.NewMailStore(10), busy, busy+1, zap.NewNop())

	// Then: the deliberate choice is not silently replaced
	if err == nil {
		t.Fatal("listenSMTPMock: expected a bind error for a pinned busy port")
	}
	if fellBack {
		t.Fatal("fellBack = true, want false")
	}
}

func TestListenerStatusFn_nilUntilAnythingReports(t *testing.T) {
	// Given: nothing has bound yet and no Lambda service is wired
	fn := listenerStatusFn(listenstatus.NewTracker(), (*lambda.Service)(nil))

	// When/Then: the health field has nothing to carry, so it can be omitted
	if got := fn(); got != nil {
		t.Fatalf("listeners = %v, want nil", got)
	}
}

func TestListenerStatusFn_carriesTheRouterBoundListeners(t *testing.T) {
	// Given: the SMTP capture server reported a bind
	tracker := listenstatus.NewTracker()
	tracker.Set(listenstatus.SMTP, listenstatus.Status{State: listenstatus.Listening, Addr: "127.0.0.1:1025"})
	fn := listenerStatusFn(tracker, (*lambda.Service)(nil))

	// When/Then: it is what health reports, with no Runtime API entry invented
	got := fn()
	if got[listenstatus.SMTP].Addr != "127.0.0.1:1025" {
		t.Fatalf("listeners[smtp] = %+v, want the reported bind", got[listenstatus.SMTP])
	}
	if _, ok := got[listenstatus.LambdaRuntimeAPI]; ok {
		t.Fatal("listeners carries a Runtime API entry before Lambda reported one")
	}
}

package lambda

import (
	"net"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/listenstatus"
)

// holdPort binds an ephemeral loopback port for the life of the test and
// returns it, standing in for a second Overcast that already owns the default.
func holdPort(t *testing.T) int {
	t.Helper()
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	return holder.Addr().(*net.TCPAddr).Port
}

func TestListenRuntimeAPI_defaultPortBusyFallsBackToEphemeral(t *testing.T) {
	// Given: the default port is held by another process
	busy := holdPort(t)

	// When: the shared listener is bound at that default
	lns, fellBack, err := listenRuntimeAPI([]string{"127.0.0.1"}, busy, busy, zap.NewNop())

	// Then: an ephemeral port was taken instead, and the fallback is reported
	if err != nil {
		t.Fatalf("listenRuntimeAPI: %v", err)
	}
	defer closeAll(lns)
	if !fellBack {
		t.Fatal("fellBack = false, want true")
	}
	got := lns[0].Addr().(*net.TCPAddr).Port
	if got == busy || got == 0 {
		t.Fatalf("bound port = %d, want an ephemeral port other than the busy %d", got, busy)
	}
}

func TestListenRuntimeAPI_pinnedPortBusyFails(t *testing.T) {
	// Given: a port set explicitly (it is not the default) is held by another process
	busy := holdPort(t)

	// When: the shared listener is bound at it
	lns, fellBack, err := listenRuntimeAPI([]string{"127.0.0.1"}, busy, busy+1, zap.NewNop())

	// Then: the deliberate choice is not silently replaced
	if err == nil {
		closeAll(lns)
		t.Fatal("listenRuntimeAPI: expected a bind error for a pinned busy port")
	}
	if fellBack {
		t.Fatal("fellBack = true, want false")
	}
	if len(lns) != 0 {
		t.Fatalf("got %d listeners, want none", len(lns))
	}
}

func TestListenRuntimeAPI_ephemeralIsNotAFallback(t *testing.T) {
	// Given: port 0 was asked for
	// When: the shared listener is bound
	lns, fellBack, err := listenRuntimeAPI([]string{"127.0.0.1"}, 0, 9001, zap.NewNop())

	// Then: it binds an ephemeral port and does not count as having fallen back
	if err != nil {
		t.Fatalf("listenRuntimeAPI: %v", err)
	}
	defer closeAll(lns)
	if fellBack {
		t.Fatal("fellBack = true, want false")
	}
}

func TestService_RuntimeAPIListenStatus_unsetUntilABindWasAttempted(t *testing.T) {
	// Given: a service whose Docker probe has not reported (or found no daemon)
	s := &Service{}

	// When: health asks
	_, ok := s.RuntimeAPIListenStatus()

	// Then: there is nothing to report yet
	if ok {
		t.Fatal("ok = true before any bind was attempted")
	}

	// Given: the bind has happened
	s.setRuntimeAPIListen(listenstatus.Status{State: listenstatus.Listening, Addr: "172.18.0.1:9001", FellBack: true})

	// When/Then: the outcome is reported as recorded
	got, ok := s.RuntimeAPIListenStatus()
	if !ok || got.State != listenstatus.Listening || got.Addr != "172.18.0.1:9001" || !got.FellBack {
		t.Fatalf("RuntimeAPIListenStatus() = %+v, %v; want the recorded listening status", got, ok)
	}

	// And: a nil service — no Lambda wired at all — is safe to ask
	var none *Service
	if _, ok := none.RuntimeAPIListenStatus(); ok {
		t.Fatal("nil service reported a status")
	}
}

package rds

// engine_port_reservation_test.go — the ports the fake engines bind.
//
// TestModifyDBCluster_masterPasswordReachesEveryMember failed on CI with
// "fake engine could not listen on the instance's dial target 127.0.0.1:45724:
// bind: address already in use", on a PR that changed nothing in RDS. The test
// handler probed one ephemeral port with :0, released it, and bound it again
// once the instance had been created — and `go test ./...` runs every package's
// binary at once, most of them standing up httptest servers on ephemeral ports.
//
// These cover the two properties that removes: the ports are held from the
// moment they are chosen, and holding them does not make an engine answer
// before the test says it does.

import (
	"net"
	"strconv"
	"testing"
	"time"
)

// Every port the allocator can hand this handler's instances has to still be
// bindable when the fake engine gets to it. Probing one and letting it go is
// what made that a race; the second member of an Aurora cluster got the next
// port up, which nothing had checked was free in the first place.
func TestReserveEnginePorts_holdsEveryPortItHandsOut(t *testing.T) {
	if !heldPortsSupported {
		t.Skip("ports cannot be held on this platform; tests bind them on demand")
	}
	base := reserveEnginePorts(t)

	for port := base; port < base+enginePortsPerHandler; port++ {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			t.Errorf("port %d was not held — anything else on the machine could take it "+
				"before the fake engine binds it", port)
		}
	}
}

// Held is not the same as answering. The health check calls an instance
// available as soon as something accepts a connection on its dial target, so a
// port held by a listener would promote instances whose engine has not been
// stood up — TestCreateDBInstance_doesNotClaimAvailableBeforeTheEngineAnswers
// is the test that says so, and it would pass for the wrong reason.
func TestReserveEnginePorts_heldPortsRefuseConnections(t *testing.T) {
	if !heldPortsSupported {
		t.Skip("ports cannot be held on this platform; tests bind them on demand")
	}
	base := reserveEnginePorts(t)

	for port := base; port < base+enginePortsPerHandler; port++ {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			t.Errorf("held port %d accepted a connection — an instance pointed at it would be "+
				"called available with no engine behind it", port)
		}
	}
}

// And the promotion works: the socket that was holding the port becomes the
// listener, on that same port, without it ever being free in between.
func TestReserveEnginePorts_heldPortBecomesTheListener(t *testing.T) {
	if !heldPortsSupported {
		t.Skip("ports cannot be held on this platform; tests bind them on demand")
	}
	base := reserveEnginePorts(t)

	engineListenersMu.Lock()
	held, ok := engineHeldPorts[engineAddr(base)]
	engineListenersMu.Unlock()
	if !ok {
		t.Fatalf("no port held for the base %d the reservation returned", base)
	}

	ln, err := held.listen()
	if err != nil {
		t.Fatalf("promoting the held socket to a listener: %v", err)
	}
	defer ln.Close() //nolint:errcheck

	if got := ln.Addr().(*net.TCPAddr).Port; got != base {
		t.Errorf("listener came up on port %d, want the held one %d", got, base)
	}
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("promoted listener does not answer: %v", err)
	}
	_ = conn.Close()
}

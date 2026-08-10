package dns

import (
	"net"
	"net/netip"
	"strconv"
	"testing"
)

// TestListen_ephemeralDrawsCandidatesRatherThanWalking pins the property that
// makes ":0" dependable on Windows.
//
// RFC 1035 §4.2 needs both protocols on one port, and the two port spaces are
// independent, so an ephemeral port is taken on one side and matched on the
// other. Retrying that is only useful if the retries land somewhere new.
//
// Windows reserves contiguous blocks of the dynamic range (Hyper-V and WinNAT;
// `netsh interface ipv4 show excludedportrange protocol=tcp`), 100-400 ports
// wide and different per protocol, and a bind inside one fails with WSAEACCES.
// The kernel hands out ephemeral ports in sequence, so a search that keeps
// asking for "any port" stays inside whichever block it walked into: ten
// attempts failed on 127.0.0.1:63774 inside the reserved 63683-63782, and a
// hundred consecutive ports still failed inside the 400-wide 62652-63051.
// Drawing candidates independently is what makes the budget mean anything.
func TestListen_ephemeralDrawsCandidatesRatherThanWalking(t *testing.T) {
	// Given: a TCP bind that refuses everything, so the whole candidate
	// sequence is observable.
	var asked []int
	server := newBindTestServer(t)
	server.listenTCP = func(_, address string) (net.Listener, error) {
		asked = append(asked, portOf(t, address))
		return nil, &net.OpError{Op: "listen", Net: "tcp", Err: errPortUnavailable}
	}

	// When: it searches for an ephemeral port.
	if err := server.Listen(); err == nil {
		t.Fatal("Listen() succeeded with a TCP bind that refuses every port")
	}

	// Then: it asked the kernel first — the polite path, and the only one taken
	// where nothing is reserved.
	if len(asked) != ephemeralBindAttempts {
		t.Fatalf("Listen() made %d attempts, want %d", len(asked), ephemeralBindAttempts)
	}
	if asked[0] != 0 {
		t.Errorf("Listen() opened with port %d, want 0 so the kernel chooses", asked[0])
	}

	// And every retry named its own candidate from the dynamic range, drawn
	// rather than walked. Independent draws are what a reserved block cannot
	// swallow whole; consecutive ones it can.
	retries := asked[1:]
	distinct := map[int]bool{}
	for _, port := range retries {
		if port < firstDynamicPort || port >= 65536 {
			t.Errorf("Listen() tried port %d, outside the dynamic range %d-65535", port, firstDynamicPort)
		}
		distinct[port] = true
	}
	if len(distinct) < 2 {
		t.Errorf("Listen() drew %d distinct candidates from %d retries; they must be independent, not a consecutive walk",
			len(distinct), len(retries))
	}
}

// TestListen_ephemeralRetriesWhenTheOtherProtocolRefuses covers the case the
// search exists for: a port usable for TCP and not for UDP.
func TestListen_ephemeralRetriesWhenTheOtherProtocolRefuses(t *testing.T) {
	// Given: the first port the search settles on has its UDP side occupied, so
	// the matching bind fails exactly as a reserved port would.
	server := newBindTestServer(t)
	var blocker *net.UDPConn
	blocked := false
	server.listenTCP = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err != nil || blocked {
			return listener, err
		}
		blocked = true
		bound := listener.Addr().(*net.TCPAddr)
		// A failure here means the port is unusable for UDP already, which is
		// the same condition from the server's point of view.
		blocker, _ = net.ListenUDP("udp", &net.UDPAddr{IP: bound.IP, Port: bound.Port})
		return listener, nil
	}

	// When: it searches for an ephemeral port.
	err := server.Listen()
	if blocker != nil {
		t.Cleanup(func() { _ = blocker.Close() })
	}

	// Then: it moved past the refused port and bound one port on both.
	if err != nil {
		t.Fatalf("Listen() = %v, want the search to move past a port UDP refused", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if udp, tcp := portOf(t, server.UDPAddr()), portOf(t, server.TCPAddr()); udp != tcp {
		t.Errorf("Listen() bound udp %s and tcp %s; RFC 1035 §4.2 needs one port", server.UDPAddr(), server.TCPAddr())
	}
}

// TestListen_namedPortDoesNotSearch keeps the other path honest: a caller who
// named a port wants that port or an error, never a different one.
func TestListen_namedPortDoesNotSearch(t *testing.T) {
	// Given: a port already held, so binding it must fail.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	t.Cleanup(func() { _ = held.Close() })

	// When: a server is told to bind that exact port.
	var attempts int
	server := NewServer(held.Addr().String(), testZone(), nil)
	server.listenTCP = func(network, address string) (net.Listener, error) {
		attempts++
		return net.Listen(network, address)
	}
	err = server.Listen()

	// Then: it fails without searching, and leaves no socket behind.
	if err == nil {
		t.Fatal("Listen() succeeded on a port already held")
	}
	if attempts > 1 {
		t.Errorf("Listen() made %d TCP binds for a named port; it must not search", attempts)
	}
	if server.UDPAddr() != "" || server.TCPAddr() != "" {
		t.Errorf("Listen() failed but left sockets bound (udp %q, tcp %q)", server.UDPAddr(), server.TCPAddr())
	}
}

func newBindTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer("127.0.0.1:0", testZone(), nil)
}

func testZone() *Zone {
	return NewZone(netip.MustParseAddr("127.0.0.1"), "localhost.overcast.sh")
}

func portOf(t *testing.T, address string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", address, err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port %q: %v", port, err)
	}
	return number
}

// errPortUnavailable stands in for the platform error a bind returns for a port
// it may not have: WSAEACCES on Windows, EADDRINUSE anywhere.
var errPortUnavailable = portError("bind: port unavailable")

type portError string

func (e portError) Error() string { return string(e) }

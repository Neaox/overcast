//go:build unix

package rds

// held_port_unix_test.go — reserving a TCP port without answering on it.
//
// A test that has to bind a specific port later needs that port held now, and
// the two obvious ways of holding one are both wrong here. Letting it go after
// probing leaves a window anything else on the machine can take it in, which is
// what made the RDS lifecycle tests fail on CI with "address already in use".
// Holding it with a listener closes the window but answers connections, and an
// engine that answers before the test says so is precisely what the health-check
// tests are written to catch.
//
// A socket that is bound but not listening is neither: bind reserves the port
// against every other binder, and without listen the kernel refuses connections
// to it exactly as if nothing were there. listen(2) then promotes that same
// socket in place when the fake engine is meant to start answering, so the port
// is never given up.

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// heldPortsSupported: listen(2) on an already-bound descriptor, and adopting
// that descriptor as a net.Listener, are both available here.
const heldPortsSupported = true

// boundSocket holds a port with a bound, unlistening TCP socket.
//
// SO_REUSEADDR is deliberately not set. Linux lets two sockets that both set it
// share an address as long as neither is listening — which is exactly the theft
// this is here to prevent, and the competing binder (anything using net.Listen)
// always sets it.
type boundSocket struct {
	fd    int
	bound int
}

// holdPort binds 127.0.0.1:p without listening on it. p may be 0, which asks
// the OS for whichever port it is willing to give.
func holdPort(p int) (heldPort, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}
	syscall.CloseOnExec(fd)

	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Port: p, Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("bind 127.0.0.1:%d: %w", p, err)
	}

	sa, err := syscall.Getsockname(fd)
	if err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("read the bound address: %w", err)
	}
	in4, ok := sa.(*syscall.SockaddrInet4)
	if !ok {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("bound socket is not IPv4 but %T", sa)
	}
	return &boundSocket{fd: fd, bound: in4.Port}, nil
}

func (s *boundSocket) port() int { return s.bound }

// listen turns the held socket into a listening one. It is the same socket, so
// the port is not released and re-bound and nothing can take it in between.
func (s *boundSocket) listen() (net.Listener, error) {
	if s.fd < 0 {
		return nil, fmt.Errorf("the socket holding port %d has already been handed over", s.bound)
	}
	if err := syscall.Listen(s.fd, 16); err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w", s.bound, err)
	}

	// os.File takes ownership of the descriptor and net.FileListener duplicates
	// it, so closing the file afterwards releases this copy and leaves the
	// listener holding its own.
	f := os.NewFile(uintptr(s.fd), fmt.Sprintf("held-port-%d", s.bound))
	s.fd = -1
	defer f.Close() //nolint:errcheck

	ln, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("adopt the socket holding 127.0.0.1:%d: %w", s.bound, err)
	}
	return ln, nil
}

func (s *boundSocket) close() {
	if s.fd >= 0 {
		_ = syscall.Close(s.fd)
		s.fd = -1
	}
}

package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// udpBufSize holds any datagram worth answering: EDNS0 advertisements in
	// practice top out around 4096, and anything larger arrives over TCP.
	udpBufSize = 4096

	// maxInFlightForwards and maxTCPConns bound the work one server can be made
	// to do at once, so a burst of queries cannot exhaust file descriptors or
	// goroutines and starve the emulator's HTTP listeners.
	maxInFlightForwards = 128
	maxTCPConns         = 64

	tcpIdleTimeout = 10 * time.Second
	forwardTimeout = 5 * time.Second

	// maxAcceptBackoff caps the retry delay after a transient accept failure,
	// which stops a persistent error from becoming a spin loop.
	maxAcceptBackoff = 500 * time.Millisecond

	// ephemeralBindAttempts bounds the search for a port free on both UDP and
	// TCP when the caller asked for any port. Every attempt after the first
	// draws independently, so the odds compound: even where a fifth of the
	// dynamic range is unusable, twenty draws miss it about once in 10^14.
	ephemeralBindAttempts = 20

	// The IANA dynamic range, which is also Windows' default. Candidates are
	// drawn from it once the kernel's own choice has been refused.
	firstDynamicPort = 49152
	dynamicPortCount = 65536 - firstDynamicPort
)

// Server answers A queries for a Zone and relays everything else to a
// Forwarder. It serves UDP and TCP on the same port, as RFC 1035 §4.2 requires:
// a client whose UDP answer is truncated retries over TCP, and a server absent
// from TCP fails that retry.
//
// Usage mirrors the other auxiliary listeners in this repo:
//
//	srv := dns.NewServer("172.18.0.2:53", zone, fwd)
//	if err := srv.Listen(); err != nil { ... }  // bind synchronously
//	go srv.Serve(ctx)                           // serve until ctx is done
type Server struct {
	addr string
	zone *Zone
	fwd  Forwarder

	locator Locator
	guard   atomic.Pointer[Guard]

	// listenTCP is net.Listen, replaced only by tests so the ephemeral-port
	// search can be driven without depending on which ports this machine
	// happens to have free.
	listenTCP func(network, address string) (net.Listener, error)

	mu  sync.Mutex
	udp *net.UDPConn
	tcp net.Listener

	closeOnce sync.Once
	pool      sync.Pool
}

// answerAddr is the address to hand a caller at peer: the one that caller can
// reach Overcast on, falling back to the zone's when it cannot be determined
// (a host-local caller, most often).
func (s *Server) answerAddr(peer netip.Addr) netip.Addr {
	if s.locator != nil {
		if addr := s.locator.AddrFor(peer); addr.IsValid() {
			return addr
		}
	}
	return s.zone.Addr()
}

// forwardablePeer reports whether queries from peer may be relayed upstream.
//
// Overcast resolves for the containers it starts and for its own host, not for
// the internet. Without this, publishing the resolver (`docker run -p 53:53`,
// an easy mistake once a port is involved) would expose an open forwarding
// resolver — the reflector half of a DNS amplification attack. Names Overcast
// owns are still answered for anyone, since those replies are small and carry
// nothing but a private address.
func forwardablePeer(peer netip.Addr) bool {
	return peer.IsValid() &&
		(peer.IsLoopback() || peer.IsPrivate() || peer.IsLinkLocalUnicast())
}

// peerAddr extracts the netip form of a net address, or the zero Addr.
func peerAddr(addr net.Addr) netip.Addr {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.AddrPort().Addr().Unmap()
	case *net.TCPAddr:
		return a.AddrPort().Addr().Unmap()
	default:
		return netip.Addr{}
	}
}

// NewServer returns a Server that will bind addr ("ip:port"; use ":0" for an
// OS-assigned port). A nil fwd means queries outside the zone are REFUSED
// rather than answered — never forged, because a client caches a synthetic
// NXDOMAIN as proof the name does not exist.
func NewServer(addr string, zone *Zone, fwd Forwarder) *Server {
	return &Server{addr: addr, zone: zone, fwd: fwd, locator: &KernelLocator{}, listenTCP: net.Listen}
}

// WithLocator replaces how the server picks the address it answers with. The
// default consults the kernel's routing table; tests substitute their own.
func (s *Server) WithLocator(l Locator) *Server {
	s.locator = l
	return s
}

// Listen binds both sockets. It must be called before Serve, and it reports the
// bind failure that matters most in practice: port 53 needs privilege outside a
// container, so callers should treat an error as "run without DNS" rather than
// as fatal.
func (s *Server) Listen() error {
	udpAddr, err := net.ResolveUDPAddr("udp", s.addr)
	if err != nil {
		return fmt.Errorf("dns: resolve %s: %w", s.addr, err)
	}
	if udpAddr.Port != 0 {
		return s.listenOn(udpAddr)
	}

	// One port is needed on both protocols and the two port spaces are
	// independent, so an ephemeral port is taken on one side and matched on the
	// other. The first attempt lets the kernel choose, which is the polite path
	// and the only one taken on a machine with nothing reserved.
	//
	// After that it probes named candidates instead, because on Windows the
	// kernel's choice is the problem. Hyper-V and WinNAT reserve contiguous
	// blocks of the dynamic range, 100-400 ports wide and different for each
	// protocol (`netsh interface ipv4 show excludedportrange protocol=tcp`),
	// and a bind inside one fails with WSAEACCES rather than "address in use".
	// The allocator hands out ports in sequence, so once it is inside a block
	// every consecutive retry is inside it too — a ten-attempt search failed on
	// 127.0.0.1:63774, inside the reserved 63683-63782, and widening it to a
	// hundred consecutive ports still failed inside the 400-wide 62652-63051.
	// Consecutive ports share a block; independently drawn ones do not, which
	// is what makes the retry budget mean something.
	var lastErr error
	for attempt := range ephemeralBindAttempts {
		port := 0
		if attempt > 0 {
			port = firstDynamicPort + rand.IntN(dynamicPortCount)
		}
		tcp, err := s.listenTCP("tcp", net.JoinHostPort(udpAddr.IP.String(), strconv.Itoa(port)))
		if err != nil {
			lastErr = err
			continue
		}
		bound := tcp.Addr().(*net.TCPAddr)
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: udpAddr.IP, Port: bound.Port, Zone: udpAddr.Zone})
		if err == nil {
			s.mu.Lock()
			s.udp, s.tcp = udp, tcp
			s.mu.Unlock()
			return nil
		}
		_ = tcp.Close()
		lastErr = err
	}
	return fmt.Errorf("dns: listen %s: no port free on both protocols in %d attempts: %w", s.addr, ephemeralBindAttempts, lastErr)
}

// listenOn binds both sockets on a port the caller named, where there is
// nothing to search for and a failure is the caller's to hear about.
func (s *Server) listenOn(udpAddr *net.UDPAddr) error {
	udp, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("dns: listen udp %s: %w", s.addr, err)
	}
	tcp, err := s.listenTCP("tcp", net.JoinHostPort(udpAddr.IP.String(), strconv.Itoa(udpAddr.Port)))
	if err != nil {
		_ = udp.Close()
		return fmt.Errorf("dns: listen tcp %s: %w", s.addr, err)
	}
	s.mu.Lock()
	s.udp, s.tcp = udp, tcp
	s.mu.Unlock()
	return nil
}

// UDPAddr and TCPAddr report the bound addresses, or "" before Listen.
func (s *Server) UDPAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.udp == nil {
		return ""
	}
	return s.udp.LocalAddr().String()
}

func (s *Server) TCPAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tcp == nil {
		return ""
	}
	return s.tcp.Addr().String()
}

// Serve answers queries until ctx is done, then returns once every in-flight
// query has finished. Listen must have been called first.
func (s *Server) Serve(ctx context.Context) {
	s.mu.Lock()
	udp, tcp := s.udp, s.tcp
	s.mu.Unlock()
	if udp == nil || tcp == nil {
		return
	}

	// The watcher exits with Serve, so a Serve that returns for any other
	// reason does not leak a goroutine waiting on a context nobody will cancel.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-stopped:
		}
	}()

	var loops sync.WaitGroup
	loops.Add(2)
	go func() { defer loops.Done(); s.serveUDP(ctx, udp) }()
	go func() { defer loops.Done(); s.serveTCP(ctx, tcp) }()
	loops.Wait()
}

// Close releases both sockets. It is idempotent and safe to call concurrently
// with Serve, which is what makes shutdown prompt: closing the sockets is what
// unblocks the accept and read loops.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		udp, tcp := s.udp, s.tcp
		s.mu.Unlock()
		if udp != nil {
			_ = udp.Close()
		}
		if tcp != nil {
			_ = tcp.Close()
		}
	})
	return nil
}

// serveUDP owns the datagram loop. Each handler goroutine is tracked so the
// loop cannot return while one is still writing to the socket.
func (s *Server) serveUDP(ctx context.Context, conn *net.UDPConn) {
	var handlers sync.WaitGroup
	defer handlers.Wait()

	sem := make(chan struct{}, maxInFlightForwards)
	for {
		buf := s.getBuf()
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			s.putBuf(buf)
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			continue
		}

		query := buf[:n]
		// The reply is built after the query in the same buffer, so answering
		// costs no allocation at all.
		reply, forward := s.respond(ctx, query, buf[n:n:len(buf)], peer.AddrPort().Addr().Unmap())
		if !forward {
			if len(reply) > 0 {
				_, _ = conn.WriteToUDP(reply, peer)
			}
			s.putBuf(buf)
			continue
		}

		select {
		case sem <- struct{}{}:
		default:
			// Saturated. Dropping is the correct DNS behaviour under load: the
			// client retries, whereas a queued answer arrives after it gave up.
			s.putBuf(buf)
			continue
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer func() { <-sem }()
			defer s.putBuf(buf)
			s.forwardUDP(ctx, conn, peer, query)
		}()
	}
}

func (s *Server) forwardUDP(ctx context.Context, conn *net.UDPConn, peer *net.UDPAddr, query []byte) {
	ctx, cancel := context.WithTimeout(ctx, forwardTimeout)
	defer cancel()

	resp, err := s.fwd.Forward(ctx, query)
	if err != nil || len(resp) == 0 {
		if reply, ok := s.failureReply(query, rcodeServFail); ok {
			_, _ = conn.WriteToUDP(reply, peer)
		}
		return
	}
	_, _ = conn.WriteToUDP(resp, peer)
}

func (s *Server) serveTCP(ctx context.Context, ln net.Listener) {
	var conns sync.WaitGroup
	defer conns.Wait()

	sem := make(chan struct{}, maxTCPConns)
	backoff := 5 * time.Millisecond
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			// Transient (fd exhaustion, say): back off rather than spin.
			time.Sleep(backoff)
			if backoff *= 2; backoff > maxAcceptBackoff {
				backoff = maxAcceptBackoff
			}
			continue
		}
		backoff = 5 * time.Millisecond

		select {
		case sem <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			defer func() { <-sem }()
			defer conn.Close()
			s.handleTCP(ctx, conn)
		}()
	}
}

// handleTCP serves one connection, which may carry several length-prefixed
// queries in sequence (RFC 7766 pipelining).
func (s *Server) handleTCP(ctx context.Context, conn net.Conn) {
	buf := s.getBuf()
	defer s.putBuf(buf)

	peer := peerAddr(conn.RemoteAddr())
	var framing [2]byte
	for {
		if err := conn.SetReadDeadline(time.Now().Add(tcpIdleTimeout)); err != nil {
			return
		}
		if _, err := io.ReadFull(conn, framing[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(framing[:]))
		if n == 0 || n > len(buf) {
			return
		}
		if _, err := io.ReadFull(conn, buf[:n]); err != nil {
			return
		}

		query := buf[:n]
		reply, forward := s.respond(ctx, query, buf[n:n:len(buf)], peer)
		if forward {
			fctx, cancel := context.WithTimeout(ctx, forwardTimeout)
			resp, err := s.fwd.Forward(fctx, query)
			cancel()
			if err != nil || len(resp) == 0 {
				var ok bool
				if reply, ok = s.failureReply(query, rcodeServFail); !ok {
					return
				}
			} else {
				reply = resp
			}
		}
		if len(reply) == 0 || len(reply) > 0xffff {
			return
		}

		binary.BigEndian.PutUint16(framing[:], uint16(len(reply)))
		if err := conn.SetWriteDeadline(time.Now().Add(tcpIdleTimeout)); err != nil {
			return
		}
		if _, err := (&net.Buffers{framing[:], reply}).WriteTo(conn); err != nil {
			return
		}
	}
}

// respond decides the reply to a query from peer. It returns forward=true when
// the name belongs to somebody else and must be relayed upstream; in that case
// reply is nil. A nil reply with forward=false means the query was too
// malformed to answer at all and should be dropped.
func (s *Server) respond(ctx context.Context, query, dst []byte, peer netip.Addr) (reply []byte, forward bool) {
	var scratch [maxNameLen]byte
	q, ok := parseQuestion(query, scratch[:])
	if !ok {
		if len(query) >= headerLen && query[2]&0x80 == 0 {
			return appendHeaderOnlyError(dst, query, rcodeFormErr), false
		}
		return nil, false
	}

	if q.qclass != classIN || !s.zone.ownsWire(q.name) {
		if s.fwd == nil || !forwardablePeer(peer) {
			return answer{rcode: rcodeRefused}.appendTo(dst, query, q), false
		}
		return nil, true
	}

	// The name is ours, so nothing upstream will be consulted — which is
	// precisely why answering it wrongly hangs rather than fails. Give the
	// guard the chance to say this names a container the caller cannot reach.
	// The string conversion is the one allocation on this path and happens only
	// when a guard is wired.
	if s.loadGuard() != nil && s.refuseByGuard(ctx, string(trimTrailingDot(q.name)), peer) {
		return answer{rcode: rcodeRefused}.appendTo(dst, query, q), false
	}

	addr := s.answerAddr(peer)
	a := answer{rcode: rcodeNoError, authoritative: true}
	switch {
	case q.qtype != typeA && q.qtype != typeANY:
		// NODATA below.
	case addr.IsValid():
		a.addr = addr
	default:
		// The name is ours but we have no address to give — better to say so
		// than to answer NODATA, which a client caches as "there is no A here".
		a.rcode = rcodeServFail
		a.authoritative = false
	}
	// Every other type is NODATA rather than a referral: we are authoritative
	// for this name and hold only an A record. AAAA is the case that matters —
	// the public record for these names is ::1, so forwarding it would hand a
	// dual-stack client its own loopback, which is the bug this package fixes.
	return a.appendTo(dst, query, q), false
}

// failureReply builds an rcode-only response to a query that could not be
// served, reporting false when the query cannot be answered at all.
func (s *Server) failureReply(query []byte, rcode byte) ([]byte, bool) {
	if len(query) < headerLen {
		return nil, false
	}
	var scratch [maxNameLen]byte
	q, ok := parseQuestion(query, scratch[:])
	if !ok {
		return appendHeaderOnlyError(nil, query, rcode), true
	}
	return answer{rcode: rcode}.appendTo(nil, query, q), true
}

func (s *Server) getBuf() []byte {
	if b, ok := s.pool.Get().(*[]byte); ok {
		return (*b)[:cap(*b)]
	}
	return make([]byte, udpBufSize)
}

func (s *Server) putBuf(b []byte) {
	b = b[:cap(b)]
	s.pool.Put(&b)
}

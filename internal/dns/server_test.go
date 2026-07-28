package dns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

const overcastAddr = "172.18.0.2"

// newTestServer starts a server on an ephemeral loopback port and returns its
// address. It is torn down when the test ends.
func newTestServer(t *testing.T, fwd Forwarder) *Server {
	t.Helper()
	z := NewZone(netip.MustParseAddr(overcastAddr), "localhost.overcast.sh", "localhost.localstack.cloud")
	srv := NewServer("127.0.0.1:0", z, fwd)
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return within 5s of cancellation")
		}
	})
	return srv
}

// resolverFor builds a stdlib resolver that talks to srv — an implementation
// independent of the code under test, so the wire format is genuinely verified
// rather than round-tripped through our own encoder.
func resolverFor(srv *Server) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			if network == "tcp" {
				return (&net.Dialer{}).DialContext(ctx, "tcp", srv.TCPAddr())
			}
			return (&net.Dialer{}).DialContext(ctx, "udp", srv.UDPAddr())
		},
	}
}

// The bug in one test: a subdomain of a split-horizon name must resolve to
// Overcast, not to the public 127.0.0.1 that /etc/hosts could not shadow.
func TestServer_AnswersWildcardSubdomains(t *testing.T) {
	srv := newTestServer(t, nil)
	res := resolverFor(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, name := range []string{
		"localhost.overcast.sh",
		"mybucket.s3.localhost.overcast.sh",
		"abc123.execute-api.us-east-1.localhost.overcast.sh",
		"deep.a.b.c.localhost.localstack.cloud",
	} {
		addrs, err := res.LookupHost(ctx, name)
		if err != nil {
			t.Errorf("LookupHost(%q): %v", name, err)
			continue
		}
		if len(addrs) != 1 || addrs[0] != overcastAddr {
			t.Errorf("LookupHost(%q) = %v, want [%s]", name, addrs, overcastAddr)
		}
	}
}

// AAAA for a name we own must be answered NODATA by us, never forwarded. The
// public record is ::1, and a dual-stack client that prefers IPv6 would take it
// and dial its own loopback — the exact failure this package removes.
func TestServer_AAAAForOwnedNameIsNodataNotForwarded(t *testing.T) {
	fwd := &recordingForwarder{err: errors.New("forwarder must not be consulted")}
	srv := newTestServer(t, fwd)
	res := resolverFor(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addrs, err := res.LookupIP(ctx, "ip6", "mybucket.s3.localhost.overcast.sh")
	if len(addrs) != 0 {
		t.Errorf("LookupIP(ip6) = %v, want no addresses", addrs)
	}
	var dnsErr *net.DNSError
	if err != nil && !errors.As(err, &dnsErr) {
		t.Errorf("LookupIP(ip6) error = %v, want a DNS not-found error", err)
	}
	// Asserting on the names forwarded, not the call count: a resolver with
	// search domains configured also asks for
	// "…localhost.overcast.sh.<search>", which we genuinely do not own and
	// must forward. The contract is that no name we *do* own is ever forwarded.
	for _, name := range fwd.forwardedNames(t) {
		if srv.zone.Owns(name) {
			t.Errorf("forwarded %q, a name we own; its public AAAA is ::1, which is the caller's own loopback", name)
		}
	}

	// The A record for the same name still resolves, so NODATA is scoped to the
	// record type rather than the name.
	if got, err := res.LookupHost(ctx, "mybucket.s3.localhost.overcast.sh"); err != nil || len(got) != 1 {
		t.Errorf("LookupHost after AAAA = %v, %v; want the A record intact", got, err)
	}
}

// Everything we do not own must keep working, or we break the internet from
// inside every Lambda.
func TestServer_ForwardsUnownedQueries(t *testing.T) {
	const canned = "upstream-response-bytes"
	fwd := &recordingForwarder{reply: []byte(canned)}
	srv := newTestServer(t, fwd)

	conn, err := net.Dial("udp", srv.UDPAddr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	query := buildQuery(t, "example.com", typeA)
	if _, err := conn.Write(query); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(buf[:n]); got != canned {
		t.Errorf("relayed response = %q, want the upstream bytes %q", got, canned)
	}
	if got := fwd.lastQuery(); string(got) != string(query) {
		t.Errorf("forwarded query was modified before relay")
	}
}

// With no upstream configured we must say so rather than answer wrongly: a
// forged NXDOMAIN would be cached by the client as "this name does not exist".
func TestServer_WithoutForwarderRefusesUnownedQueries(t *testing.T) {
	srv := newTestServer(t, nil)

	conn, err := net.Dial("udp", srv.UDPAddr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(buildQuery(t, "example.com", typeA)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n < headerLen {
		t.Fatalf("short response: %d bytes", n)
	}
	if rcode := buf[3] & 0x0f; rcode != rcodeRefused {
		t.Errorf("rcode = %d, want REFUSED (%d)", rcode, rcodeRefused)
	}
}

// TCP is not optional: a client that gets a truncated UDP answer retries over
// TCP, and a server that does not listen there fails the retry.
func TestServer_AnswersOverTCP(t *testing.T) {
	srv := newTestServer(t, nil)
	res := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", srv.TCPAddr())
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addrs, err := res.LookupHost(ctx, "mybucket.s3.localhost.overcast.sh")
	if err != nil {
		t.Fatalf("LookupHost over TCP: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != overcastAddr {
		t.Errorf("LookupHost over TCP = %v, want [%s]", addrs, overcastAddr)
	}
}

// Shutdown must be prompt and must not hang when traffic arrives while the
// server is stopping — the failure mode the SMTP mock server had.
func TestServer_ShutdownUnderLoadDoesNotHang(t *testing.T) {
	z := NewZone(netip.MustParseAddr(overcastAddr), "localhost.overcast.sh")
	srv := NewServer("127.0.0.1:0", z, nil)
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() { defer close(served); srv.Serve(ctx) }()

	// Keep both transports busy across the cancellation.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := buildQuery(t, "mybucket.s3.localhost.overcast.sh", typeA)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if c, err := net.Dial("udp", srv.UDPAddr()); err == nil {
					_, _ = c.Write(query)
					_ = c.Close()
				}
				if c, err := net.Dial("tcp", srv.TCPAddr()); err == nil {
					_ = c.Close()
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		close(stop)
		wg.Wait()
		t.Fatal("Serve did not return within 5s of cancellation")
	}
	close(stop)
	wg.Wait()

	// Close is idempotent and safe after Serve has already returned.
	if err := srv.Close(); err != nil {
		t.Errorf("Close after Serve: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// A malformed or truncated datagram must not take the server down.
func TestServer_IgnoresMalformedQueries(t *testing.T) {
	srv := newTestServer(t, nil)

	for _, junk := range [][]byte{
		{},
		{0x00},
		make([]byte, headerLen),                  // header only, no question
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},     // nonsense
		append(make([]byte, headerLen), 0xc0, 0), // compression pointer as a name
	} {
		conn, err := net.Dial("udp", srv.UDPAddr())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		_, _ = conn.Write(junk)
		_ = conn.Close()
	}

	// The server is still answering.
	res := resolverFor(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := res.LookupHost(ctx, "localhost.overcast.sh"); err != nil {
		t.Fatalf("server stopped answering after malformed input: %v", err)
	}
}

// Overcast attaches to overcast_lambda and overcast_ecs by default, so it has
// an address on each. A container on one network cannot reach its address on
// the other, so the answer has to depend on who asked.
func TestServer_AnswersWithTheAddressReachableFromTheCaller(t *testing.T) {
	const ecsSideAddr = "172.19.0.5"
	z := NewZone(netip.MustParseAddr(overcastAddr), "localhost.overcast.sh")
	srv := NewServer("127.0.0.1:0", z, nil).
		WithLocator(fixedLocator{addr: netip.MustParseAddr(ecsSideAddr)})
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	lookupCtx, lookupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer lookupCancel()
	addrs, err := resolverFor(srv).LookupHost(lookupCtx, "mybucket.s3.localhost.overcast.sh")
	if err != nil {
		t.Fatalf("LookupHost: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != ecsSideAddr {
		t.Errorf("LookupHost = %v, want [%s] — the caller's side of the network, not the zone fallback %s",
			addrs, ecsSideAddr, overcastAddr)
	}
}

// When no address can be determined at all, SERVFAIL is the honest answer: a
// NODATA reply would be cached as proof the name has no address.
func TestServer_WithoutAnyAddressFailsRatherThanDenying(t *testing.T) {
	z := NewZone(netip.Addr{}, "localhost.overcast.sh")
	srv := NewServer("127.0.0.1:0", z, nil).WithLocator(fixedLocator{})
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	conn, err := net.Dial("udp", srv.UDPAddr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(buildQuery(t, "b.localhost.overcast.sh", typeA)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n < headerLen {
		t.Fatalf("short response: %d bytes", n)
	}
	if rcode := buf[3] & 0x0f; rcode != rcodeServFail {
		t.Errorf("rcode = %d, want SERVFAIL (%d)", rcode, rcodeServFail)
	}
}

// The kernel locator must not offer a loopback address: handing "127.0.0.1" to
// a container names the container itself, which is the bug being fixed.
func TestKernelLocator_RejectsLoopback(t *testing.T) {
	var l KernelLocator
	if got := l.AddrFor(netip.MustParseAddr("127.0.0.1")); got.IsValid() {
		t.Errorf("AddrFor(127.0.0.1) = %v, want the zero Addr", got)
	}
	if got := l.AddrFor(netip.Addr{}); got.IsValid() {
		t.Errorf("AddrFor(invalid) = %v, want the zero Addr", got)
	}
	// Cached lookups agree with uncached ones.
	if got := l.AddrFor(netip.MustParseAddr("127.0.0.1")); got.IsValid() {
		t.Errorf("cached AddrFor(127.0.0.1) = %v, want the zero Addr", got)
	}
}

type fixedLocator struct{ addr netip.Addr }

func (f fixedLocator) AddrFor(netip.Addr) netip.Addr { return f.addr }

// recordingForwarder is a Forwarder test double.
type recordingForwarder struct {
	mu      sync.Mutex
	last    []byte
	queries [][]byte
	reply   []byte
	err     error
}

func (f *recordingForwarder) Forward(_ context.Context, query []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = append([]byte(nil), query...)
	f.queries = append(f.queries, append([]byte(nil), query...))
	return f.reply, f.err
}

// forwardedNames decodes the question name out of every relayed query.
func (f *recordingForwarder) forwardedNames(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.queries))
	for _, q := range f.queries {
		var scratch [maxNameLen]byte
		parsed, ok := parseQuestion(q, scratch[:])
		if !ok {
			t.Fatalf("forwarded a query that is not a parseable question: %v", q)
		}
		names = append(names, string(parsed.name))
	}
	return names
}

func (f *recordingForwarder) lastQuery() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

// buildQuery encodes a standard recursive query, independently of the encoder
// under test.
func buildQuery(t *testing.T, name string, qtype uint16) []byte {
	t.Helper()
	msg := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for label := range splitLabels(name) {
		if len(label) == 0 || len(label) > 63 {
			t.Fatalf("bad label %q in %q", label, name)
		}
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0)
	msg = append(msg, byte(qtype>>8), byte(qtype), 0, 1)
	return msg
}

// splitLabels yields the dot-separated labels of name, ignoring a trailing dot.
func splitLabels(name string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for len(name) > 0 {
			i := 0
			for i < len(name) && name[i] != '.' {
				i++
			}
			if i > 0 && !yield(name[:i]) {
				return
			}
			if i == len(name) {
				return
			}
			name = name[i+1:]
		}
	}
}

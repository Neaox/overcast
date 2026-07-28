package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"
)

// DefaultResolvConf is where Unix systems record their resolvers. Inside
// Overcast's own container this names Docker's embedded resolver, which is
// exactly the right upstream: it answers container-name service discovery and
// recurses to the daemon's resolvers for everything else.
const DefaultResolvConf = "/etc/resolv.conf"

// defaultForwardTimeout bounds one upstream exchange. Short, because a Lambda
// blocked on DNS is a Lambda timing out.
const defaultForwardTimeout = 3 * time.Second

// Forwarder resolves queries for names Overcast does not own. Overcast must
// never answer those itself — a function that cannot reach the internet, or
// its sibling containers, is worse than one that cannot reach a bucket by its
// virtual-hosted name.
type Forwarder interface {
	// Forward relays query, a complete DNS message, and returns the upstream
	// response verbatim.
	Forward(ctx context.Context, query []byte) ([]byte, error)
}

// UpstreamForwarder relays queries to a fixed list of resolvers, in order,
// falling through to the next on failure. It treats messages as opaque, so
// every query type and protocol extension keeps working without this package
// having to understand it.
type UpstreamForwarder struct {
	servers []string // host:port
	timeout time.Duration
}

// NewUpstreamForwarder returns a forwarder for servers, each "ip" or "ip:port"
// (port 53 assumed). Entries that are not addresses are dropped.
func NewUpstreamForwarder(servers ...string) *UpstreamForwarder {
	f := &UpstreamForwarder{timeout: defaultForwardTimeout}
	for _, s := range servers {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(s); err != nil {
			s = net.JoinHostPort(s, "53")
		}
		if !slices.Contains(f.servers, s) {
			f.servers = append(f.servers, s)
		}
	}
	return f
}

// Servers returns the upstreams in the order they are tried.
func (f *UpstreamForwarder) Servers() []string {
	if f == nil {
		return nil
	}
	return slices.Clone(f.servers)
}

// Forward implements Forwarder.
func (f *UpstreamForwarder) Forward(ctx context.Context, query []byte) ([]byte, error) {
	if f == nil || len(f.servers) == 0 {
		return nil, errors.New("dns: no upstream resolvers configured")
	}
	var lastErr error
	for _, server := range f.servers {
		if ctx.Err() != nil {
			break
		}
		resp, err := f.exchange(ctx, server, query)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = ctx.Err()
	}
	return nil, fmt.Errorf("dns: forward failed: %w", lastErr)
}

func (f *UpstreamForwarder) exchange(ctx context.Context, server string, query []byte) ([]byte, error) {
	conn, err := (&net.Dialer{Timeout: f.timeout}).DialContext(ctx, "udp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	deadline := time.Now().Add(f.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}

	buf := make([]byte, udpBufSize)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	// The socket is connected, so only this server's datagrams arrive; matching
	// the transaction ID additionally rejects a stale reply to an earlier query.
	if n < headerLen || len(query) < 2 || buf[0] != query[0] || buf[1] != query[1] {
		return nil, errors.New("dns: upstream reply did not match the query")
	}
	return buf[:n], nil
}

// SystemResolvers reads the nameservers from a resolv.conf-format file,
// skipping any address in exclude. Excluding Overcast's own address matters:
// forwarding to ourselves is an infinite loop.
//
// Returns nil when the file cannot be read, which callers should treat as "no
// forwarding available" rather than an error — a resolver that answers only
// Overcast's own names is still better than none.
func SystemResolvers(path string, exclude ...netip.Addr) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.Lines(string(data)) {
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		addr, err := netip.ParseAddr(fields[1])
		if err != nil {
			continue
		}
		if slices.Contains(exclude, addr.Unmap()) {
			continue
		}
		if s := net.JoinHostPort(addr.String(), "53"); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

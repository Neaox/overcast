// Package dns serves the split-horizon hostnames Overcast advertises, to the
// containers Overcast starts.
//
// Why a resolver rather than more /etc/hosts entries: Docker's --add-host
// writes an exact-match table, and DNS names are hierarchical. The apex
// (localhost.overcast.sh) can be shadowed that way, but a subdomain cannot —
// and the URLs AWS SDKs build are overwhelmingly subdomains: virtual-hosted S3
// (bucket.s3.host), API Gateway (id.execute-api.region.host), AppSync, Lambda
// function URLs. Those names miss the table, fall through to public DNS, and
// resolve to 127.0.0.1 by design, which inside a container is the container
// itself. The lookup succeeds, so the failure surfaces as a refused connection
// rather than an unknown host.
//
// Enumeration cannot close the gap either: bucket names and API IDs are minted
// after a container starts, and a container's /etc/hosts is fixed at creation.
//
// Docker's embedded resolver (127.0.0.11) forwards names it cannot answer to
// the servers set by HostConfig.Dns, and stays in front for container-name
// service discovery. Pointing that at this package gives real wildcard matching
// without displacing anything Docker already does.
//
// /etc/hosts entries are still written (see internal/containerendpoint): they
// cover the apex names with no dependency on this server being reachable.
package dns

import (
	"net"
	"net/netip"
	"slices"
	"strings"
)

// unusableDomains can never be claimed. A container needs its own loopback, so
// shadowing "localhost" would point a container's own address at Overcast —
// the precise failure this package exists to remove, inverted.
var unusableDomains = []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "[::1]"}

// Zone is the set of domains Overcast answers for, and the address it answers
// with. It is immutable after construction and safe for concurrent use.
type Zone struct {
	domains []string // normalised: trimmed, no trailing dot, deduplicated
	addr    netip.Addr
}

// NewZone returns the zone claiming every domain and subdomain of domains,
// with addr as the fallback answer. Entries that cannot usefully be served —
// blank, an IP literal, or a loopback name — are dropped rather than rejected,
// because the list is assembled from user configuration and one bad entry
// should not disable the resolver. Use Valid to test whether anything usable
// survived.
//
// addr is a fallback rather than the answer: Overcast sits on several Docker
// networks and therefore has several addresses, so the server prefers the one
// reachable from the caller (see Locator). A zero or non-IPv4 addr is accepted
// and simply leaves no fallback — an A record is the only record served, so an
// IPv6 address could not be one.
func NewZone(addr netip.Addr, domains ...string) *Zone {
	z := &Zone{}
	if addr = addr.Unmap(); addr.Is4() {
		z.addr = addr
	}
	for _, d := range domains {
		d = strings.TrimSuffix(strings.TrimSpace(d), ".")
		if d == "" || net.ParseIP(d) != nil || slices.ContainsFunc(unusableDomains, func(u string) bool {
			return strings.EqualFold(u, d)
		}) {
			continue
		}
		if slices.ContainsFunc(z.domains, func(e string) bool { return strings.EqualFold(e, d) }) {
			continue
		}
		z.domains = append(z.domains, d)
	}
	return z
}

// Valid reports whether the zone claims anything. An address is not required:
// the server resolves one per caller and falls back to Addr only when it
// cannot.
func (z *Zone) Valid() bool {
	return z != nil && len(z.domains) > 0
}

// Addr is the fallback address, which may be the zero Addr.
func (z *Zone) Addr() netip.Addr {
	if z == nil {
		return netip.Addr{}
	}
	return z.addr
}

// Domains returns the normalised domains the zone claims.
func (z *Zone) Domains() []string {
	if z == nil {
		return nil
	}
	return slices.Clone(z.domains)
}

// Owns reports whether name is one of the zone's domains or a subdomain of one.
// It does not allocate.
func (z *Zone) Owns(name string) bool { return zoneOwns(z, name) }

// ownsWire is Owns for a name still in its decoded wire buffer, so the query
// path matches without converting to a string.
func (z *Zone) ownsWire(name []byte) bool { return zoneOwns(z, name) }

func zoneOwns[T ~string | ~[]byte](z *Zone, name T) bool {
	if !z.Valid() {
		return false
	}
	n := trimTrailingDot(name)
	for _, d := range z.domains {
		if underDomain(n, d) {
			return true
		}
	}
	return false
}

func trimTrailingDot[T ~string | ~[]byte](n T) T {
	if len(n) > 0 && n[len(n)-1] == '.' {
		return n[:len(n)-1]
	}
	return n
}

// underDomain reports whether name is domain or a subdomain of it. The boundary
// must fall on a label separator: "notlocalhost.overcast.sh" shares a suffix
// with "localhost.overcast.sh" but belongs to somebody else.
func underDomain[T ~string | ~[]byte](name T, domain string) bool {
	switch {
	case len(name) == len(domain):
		return equalFoldASCII(name, domain)
	case len(name) > len(domain)+1 && name[len(name)-len(domain)-1] == '.':
		return equalFoldASCII(name[len(name)-len(domain):], domain)
	default:
		return false
	}
}

// equalFoldASCII compares case-insensitively over ASCII, which is the whole of
// DNS name comparison — RFC 4343 defines it on octets, and non-ASCII names
// travel as punycode.
func equalFoldASCII[T ~string | ~[]byte](a T, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(b) {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

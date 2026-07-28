package dns

import (
	"net/netip"
	"testing"
)

// The defect this package exists to fix: /etc/hosts is an exact-match table, so
// only the apex names resolved to Overcast inside a container. A subdomain fell
// through to public DNS, which answers 127.0.0.1 by design — the container
// itself. Virtual-hosted S3 and execute-api URLs were therefore undialable from
// function code.
func TestZone_OwnsApexAndEverySubdomain(t *testing.T) {
	z := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh", "localhost.localstack.cloud")

	owned := []string{
		"localhost.overcast.sh",                              // apex
		"mybucket.s3.localhost.overcast.sh",                  // S3 virtual-hosted
		"abc123.execute-api.us-east-1.localhost.overcast.sh", // API Gateway invoke
		"deep.a.b.c.localhost.localstack.cloud",              // arbitrary depth
		"LOCALHOST.OVERCAST.SH",                              // RFC 4343: names are case-insensitive
		"MyBucket.S3.Localhost.Overcast.SH",
	}
	for _, name := range owned {
		if !z.Owns(name) {
			t.Errorf("Owns(%q) = false, want true", name)
		}
	}
}

func TestZone_DoesNotOwnLookalikes(t *testing.T) {
	z := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh")

	// A suffix match alone is not ownership — the boundary must fall on a label
	// separator, or we would hijack names belonging to somebody else.
	notOwned := []string{
		"notlocalhost.overcast.sh",       // shares the suffix, different label
		"localhost.overcast.sh.evil.com", // our name as a prefix of theirs
		"overcast.sh",                    // parent of our apex, not ours
		"example.com",
		"",
		".",
		"sh",
	}
	for _, name := range notOwned {
		if z.Owns(name) {
			t.Errorf("Owns(%q) = true, want false", name)
		}
	}
}

// A container needs its own loopback, so "localhost" can never be claimed — and
// neither can a bare IP, which is already an address. Callers assemble the
// domain list from user configuration, so the zone filters rather than trusting.
func TestNewZone_RejectsUnusableDomains(t *testing.T) {
	z := NewZone(netip.MustParseAddr("172.18.0.2"),
		"localhost", "127.0.0.1", "::1", "  ", "", "localhost.overcast.sh")

	if got := z.Domains(); len(got) != 1 || got[0] != "localhost.overcast.sh" {
		t.Fatalf("Domains() = %v, want only [localhost.overcast.sh]", got)
	}
	for _, name := range []string{"localhost", "anything.localhost", "127.0.0.1"} {
		if z.Owns(name) {
			t.Errorf("Owns(%q) = true; claiming it would break the container's own loopback", name)
		}
	}
}

// Trailing dots are legal in a fully-qualified name and must not change the answer.
func TestZone_NormalisesTrailingDots(t *testing.T) {
	z := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh.")

	for _, name := range []string{"localhost.overcast.sh", "localhost.overcast.sh.", "b.localhost.overcast.sh."} {
		if !z.Owns(name) {
			t.Errorf("Owns(%q) = false, want true", name)
		}
	}
}

func TestNewZone_WithoutUsableDomainsIsInert(t *testing.T) {
	if z := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost"); z.Valid() {
		t.Error("a zone with no usable domains reported Valid() = true")
	}
}

// The address is a fallback, not the answer: the server prefers the one
// reachable from the caller, so a zone without one is still useful.
func TestNewZone_AddressIsOptionalAndMustBeIPv4(t *testing.T) {
	z := NewZone(netip.Addr{}, "localhost.overcast.sh")
	if !z.Valid() || !z.Owns("b.localhost.overcast.sh") {
		t.Error("a zone with no fallback address should still claim its domains")
	}
	if z.Addr().IsValid() {
		t.Errorf("Addr() = %v, want the zero Addr", z.Addr())
	}

	// An A record is the only record served, so an IPv6 fallback could never be
	// used; dropping it keeps Addr() honest rather than storing an unusable one.
	if got := NewZone(netip.MustParseAddr("::1"), "localhost.overcast.sh").Addr(); got.IsValid() {
		t.Errorf("Addr() = %v, want the zero Addr for a non-IPv4 address", got)
	}
	// An IPv4-mapped IPv6 address is an IPv4 address and is kept, unmapped.
	if got := NewZone(netip.MustParseAddr("::ffff:172.18.0.2"), "localhost.overcast.sh").Addr(); got.String() != "172.18.0.2" {
		t.Errorf("Addr() = %v, want 172.18.0.2", got)
	}
}

// Owns runs on every query, so it must not allocate.
func TestZone_OwnsDoesNotAllocate(t *testing.T) {
	z := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh", "localhost.localstack.cloud")
	name := "mybucket.s3.localhost.overcast.sh"

	if got := testing.AllocsPerRun(100, func() { z.Owns(name) }); got != 0 {
		t.Errorf("Owns allocated %v times per call, want 0", got)
	}
}

func BenchmarkZoneOwns(b *testing.B) {
	z := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh", "localhost.localstack.cloud", "localhost.floci.io")
	name := "abc123.execute-api.us-east-1.localhost.overcast.sh"
	b.ReportAllocs()
	for b.Loop() {
		z.Owns(name)
	}
}

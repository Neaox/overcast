package route53

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/dns"
	"github.com/Neaox/overcast/internal/state"
)

// newTestService returns a Service backed by a fresh in-memory store, ready
// to seed zones and record sets directly (bypassing the REST/typed handlers,
// since this file tests Lookup, not the control plane in front of it).
func newTestService(t *testing.T) *Service {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	return New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
}

// seedZone creates a hosted zone (with its default apex NS/SOA records) and
// returns it.
func seedZone(t *testing.T, s *Service, name string, private bool) *HostedZone {
	t.Helper()
	ref := name + "-ref"
	if private {
		ref += "-private"
	}
	zone, _, aerr := s.createHostedZoneCore(context.Background(), createZoneInput{
		Name: name, CallerReference: ref, PrivateZone: private,
	})
	if aerr != nil {
		t.Fatalf("createHostedZoneCore(%q): %v", name, aerr)
	}
	return zone
}

// putRR stores a record set directly, skipping ChangeResourceRecordSets'
// validation — this file is testing Lookup, not the batch-change contract.
func putRR(t *testing.T, s *Service, zone *HostedZone, name, rrType string, ttl int64, values ...string) {
	t.Helper()
	if err := s.putRRSet(context.Background(), zone.Id, &ResourceRecordSet{
		Name: normalizeDNSName(name), Type: rrType, TTL: ttl, ResourceRecords: values,
	}); err != nil {
		t.Fatalf("putRRSet(%s %s): %v", name, rrType, err)
	}
}

func putAlias(t *testing.T, s *Service, zone *HostedZone, name, rrType string, ttl int64, aliasDNSName string) {
	t.Helper()
	if err := s.putRRSet(context.Background(), zone.Id, &ResourceRecordSet{
		Name: normalizeDNSName(name), Type: rrType, TTL: ttl, AliasDNSName: aliasDNSName,
	}); err != nil {
		t.Fatalf("putRRSet alias(%s %s): %v", name, rrType, err)
	}
}

func lookup(t *testing.T, s *Service, name string, qtype uint16) (dns.Route53Answer, bool) {
	t.Helper()
	return s.Lookup(context.Background(), name, qtype)
}

// No zone in the store claims the name at all: the caller (internal/dns)
// must fall back to its own zone/forwarding behaviour rather than treat this
// as NXDOMAIN — this package is not authoritative for it.
func TestLookup_NoZoneClaims(t *testing.T) {
	s := newTestService(t)
	seedZone(t, s, "example.com.", false)

	if _, found := lookup(t, s, "unrelated.org", dns.TypeA); found {
		t.Fatalf("found = true for a name no hosted zone claims")
	}
}

func TestLookup_A(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "app.example.com.", "A", 123, "10.0.0.5")

	ans, found := lookup(t, s, "app.example.com", dns.TypeA)
	if !found {
		t.Fatal("found = false")
	}
	if ans.Rcode != dns.RcodeNoError || len(ans.Answers) != 1 {
		t.Fatalf("ans = %+v", ans)
	}
	rr := ans.Answers[0]
	if rr.Type != dns.TypeA || rr.Value != "10.0.0.5" || rr.TTL != 123 {
		t.Errorf("rr = %+v, want A 10.0.0.5 TTL 123", rr)
	}
}

func TestLookup_AAAA(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "app.example.com.", "AAAA", 60, "::1")

	ans, found := lookup(t, s, "app.example.com", dns.TypeAAAA)
	if !found || len(ans.Answers) != 1 || ans.Answers[0].Type != dns.TypeAAAA || ans.Answers[0].Value != "::1" {
		t.Fatalf("ans = %+v, found = %v", ans, found)
	}
}

func TestLookup_MX(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "example.com.", "MX", 300, "10 mail.example.com.")

	ans, found := lookup(t, s, "example.com", dns.TypeMX)
	if !found || len(ans.Answers) != 1 || ans.Answers[0].Value != "10 mail.example.com." {
		t.Fatalf("ans = %+v, found = %v", ans, found)
	}
}

func TestLookup_TXT(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "example.com.", "TXT", 300, `"v=spf1 include:_spf.example.com ~all"`)

	ans, found := lookup(t, s, "example.com", dns.TypeTXT)
	if !found || len(ans.Answers) != 1 {
		t.Fatalf("ans = %+v, found = %v", ans, found)
	}
}

// NS at a delegated subdomain, not just the zone apex.
func TestLookup_NSAtSubdomain(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "sub.example.com.", "NS", 172800, "ns1.elsewhere.test.")

	ans, found := lookup(t, s, "sub.example.com", dns.TypeNS)
	if !found || len(ans.Answers) != 1 || ans.Answers[0].Value != "ns1.elsewhere.test." {
		t.Fatalf("ans = %+v, found = %v", ans, found)
	}
}

// The zone's own default apex SOA record, queried directly.
func TestLookup_SOAAtApex(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)

	ans, found := lookup(t, s, "example.com", dns.TypeSOA)
	if !found || len(ans.Answers) != 1 || ans.Answers[0].Type != dns.TypeSOA {
		t.Fatalf("ans = %+v, found = %v", ans, found)
	}
	_ = zone
}

// A CNAME chain that crosses from one hosted zone into another must be
// followed in full, in one Lookup call.
func TestLookup_CNAMEChainAcrossZones(t *testing.T) {
	s := newTestService(t)
	zoneA := seedZone(t, s, "a.example.", false)
	zoneB := seedZone(t, s, "b.example.", false)
	putRR(t, s, zoneA, "www.a.example.", "CNAME", 300, "target.b.example.")
	putRR(t, s, zoneB, "target.b.example.", "A", 60, "10.0.0.9")

	ans, found := lookup(t, s, "www.a.example", dns.TypeA)
	if !found {
		t.Fatal("found = false")
	}
	if len(ans.Answers) != 2 {
		t.Fatalf("answers = %+v, want CNAME + A", ans.Answers)
	}
	if ans.Answers[0].Type != dns.TypeCNAME || ans.Answers[0].Value != "target.b.example." {
		t.Errorf("answers[0] = %+v", ans.Answers[0])
	}
	if ans.Answers[1].Type != dns.TypeA || ans.Answers[1].Value != "10.0.0.9" {
		t.Errorf("answers[1] = %+v", ans.Answers[1])
	}
}

// A CNAME chain that leaves the store entirely still answers with the chain
// built so far, rather than failing the whole query.
func TestLookup_CNAMEChainLeavesTheStore(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "www.example.com.", "CNAME", 300, "outside.other-domain.test.")

	ans, found := lookup(t, s, "www.example.com", dns.TypeA)
	if !found {
		t.Fatal("found = false")
	}
	if len(ans.Answers) != 1 || ans.Answers[0].Type != dns.TypeCNAME {
		t.Fatalf("answers = %+v, want just the CNAME", ans.Answers)
	}
}

// A CNAME cycle must terminate rather than loop the store scan forever.
func TestLookup_CNAMELoopTerminates(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "a.example.com.", "CNAME", 300, "b.example.com.")
	putRR(t, s, zone, "b.example.com.", "CNAME", 300, "a.example.com.")

	done := make(chan struct{})
	var ans dns.Route53Answer
	var found bool
	go func() {
		ans, found = lookup(t, s, "a.example.com", dns.TypeA)
		close(done)
	}()
	select {
	case <-done:
	case <-timeoutCh():
		t.Fatal("Lookup did not terminate on a CNAME cycle")
	}
	if !found {
		t.Fatal("found = false")
	}
	_ = ans
}

// A wildcard answers a name with no record of its own, replacing exactly one
// label — AWS's documented wildcard scope, not RFC 1034's "any depth" reading.
func TestLookup_WildcardMatchesOneLabel(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "*.example.com.", "A", 30, "10.0.0.9")

	if ans, found := lookup(t, s, "anything.example.com", dns.TypeA); !found || len(ans.Answers) != 1 {
		t.Fatalf("one-label match: ans = %+v, found = %v", ans, found)
	} else if ans.Answers[0].Name != "anything.example.com." {
		t.Errorf("answer owner = %q, want the queried name, not the literal wildcard", ans.Answers[0].Name)
	}

	if ans, found := lookup(t, s, "two.levels.example.com", dns.TypeA); !found {
		t.Fatal("found = false")
	} else if len(ans.Answers) != 0 {
		t.Errorf("two-label name matched a one-label wildcard: %+v", ans.Answers)
	}
}

// An exact record always wins over a wildcard at the same level.
func TestLookup_ExactRecordBeatsWildcard(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "*.example.com.", "A", 30, "10.0.0.9")
	putRR(t, s, zone, "specific.example.com.", "A", 30, "10.0.0.1")

	ans, found := lookup(t, s, "specific.example.com", dns.TypeA)
	if !found || len(ans.Answers) != 1 || ans.Answers[0].Value != "10.0.0.1" {
		t.Fatalf("ans = %+v, found = %v, want the specific record", ans, found)
	}
}

// A name that exists but has no record of the queried type is NODATA
// (NOERROR, no answers), distinct from a name that does not exist at all.
func TestLookup_NODATA(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putRR(t, s, zone, "app.example.com.", "A", 60, "10.0.0.5")

	ans, found := lookup(t, s, "app.example.com", dns.TypeMX)
	if !found {
		t.Fatal("found = false")
	}
	if ans.Rcode != dns.RcodeNoError || len(ans.Answers) != 0 {
		t.Fatalf("ans = %+v, want NOERROR/NODATA", ans)
	}
	if len(ans.Authority) != 1 || ans.Authority[0].Type != dns.TypeSOA {
		t.Fatalf("authority = %+v, want the zone's SOA for negative caching", ans.Authority)
	}
}

// A name that does not exist anywhere under the zone is NXDOMAIN.
func TestLookup_NXDOMAIN(t *testing.T) {
	s := newTestService(t)
	seedZone(t, s, "example.com.", false)

	ans, found := lookup(t, s, "nope.example.com", dns.TypeA)
	if !found {
		t.Fatal("found = false")
	}
	if ans.Rcode != dns.RcodeNXDomain {
		t.Fatalf("rcode = %d, want NXDOMAIN", ans.Rcode)
	}
	if len(ans.Authority) != 1 || ans.Authority[0].Type != dns.TypeSOA {
		t.Fatalf("authority = %+v, want the zone's SOA", ans.Authority)
	}
}

// An ALIAS record is reported back rather than resolved here — the address
// it should answer with is a fact only internal/dns can compute.
func TestLookup_AliasARecordDefersToCaller(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putAlias(t, s, zone, "elb.example.com.", "A", 60, "my-load-balancer.us-east-1.elb.amazonaws.com.")

	ans, found := lookup(t, s, "elb.example.com", dns.TypeA)
	if !found {
		t.Fatal("found = false")
	}
	if !ans.Alias || ans.AliasName != "elb.example.com." || ans.AliasTTL != 60 {
		t.Fatalf("ans = %+v, want an Alias answer for elb.example.com.", ans)
	}
}

// An AAAA-typed alias has no address this environment can supply (Overcast
// has no IPv6 address), so it degrades to NODATA rather than a fabricated
// answer.
func TestLookup_AliasAAAAIsNodata(t *testing.T) {
	s := newTestService(t)
	zone := seedZone(t, s, "example.com.", false)
	putAlias(t, s, zone, "elb6.example.com.", "AAAA", 60, "my-load-balancer.us-east-1.elb.amazonaws.com.")

	ans, found := lookup(t, s, "elb6.example.com", dns.TypeAAAA)
	if !found {
		t.Fatal("found = false")
	}
	if ans.Alias || len(ans.Answers) != 0 || ans.Rcode != dns.RcodeNoError {
		t.Fatalf("ans = %+v, want NODATA, not a fabricated alias answer", ans)
	}
}

// When a public and a private zone share a name, the private zone wins —
// "keep it honest and simple": every container gets the private zone's view.
func TestLookup_PrivateZoneWinsOnNameTie(t *testing.T) {
	s := newTestService(t)
	pub := seedZone(t, s, "example.com.", false)
	priv := seedZone(t, s, "example.com.", true)
	putRR(t, s, pub, "app.example.com.", "A", 60, "1.1.1.1")
	putRR(t, s, priv, "app.example.com.", "A", 60, "10.0.0.1")

	ans, found := lookup(t, s, "app.example.com", dns.TypeA)
	if !found || len(ans.Answers) != 1 || ans.Answers[0].Value != "10.0.0.1" {
		t.Fatalf("ans = %+v, found = %v, want the private zone's record", ans, found)
	}
}

// The most specific zone answers, not merely any zone whose name is a
// suffix.
func TestLookup_MostSpecificZoneWins(t *testing.T) {
	s := newTestService(t)
	parent := seedZone(t, s, "example.com.", false)
	child := seedZone(t, s, "sub.example.com.", false)
	putRR(t, s, parent, "app.sub.example.com.", "A", 60, "1.1.1.1")
	putRR(t, s, child, "app.sub.example.com.", "A", 60, "2.2.2.2")

	ans, found := lookup(t, s, "app.sub.example.com", dns.TypeA)
	if !found || len(ans.Answers) != 1 || ans.Answers[0].Value != "2.2.2.2" {
		t.Fatalf("ans = %+v, found = %v, want the more specific zone's record", ans, found)
	}
}

func timeoutCh() <-chan time.Time {
	return time.After(2 * time.Second)
}

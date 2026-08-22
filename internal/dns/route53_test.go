package dns

import (
	"context"
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"
)

// fakeRoute53 answers exactly what it was seeded with, keyed by (name, qtype).
type fakeRoute53 struct {
	answers map[string]map[uint16]Route53Answer
	asked   []string
}

func (f *fakeRoute53) set(name string, qtype uint16, ans Route53Answer) {
	if f.answers == nil {
		f.answers = map[string]map[uint16]Route53Answer{}
	}
	if f.answers[name] == nil {
		f.answers[name] = map[uint16]Route53Answer{}
	}
	f.answers[name][qtype] = ans
}

func (f *fakeRoute53) Lookup(_ context.Context, qname string, qtype uint16) (Route53Answer, bool) {
	f.asked = append(f.asked, qname)
	byType, ok := f.answers[qname]
	if !ok {
		return Route53Answer{}, false
	}
	ans, ok := byType[qtype]
	return ans, ok
}

// wireRR is one resource record as decoded straight off the wire, independent
// of the RR type this package's encoder produces from.
type wireRR struct {
	Name  string
	Type  uint16
	TTL   uint32
	RData []byte
}

// decodeName reads a length-prefixed label sequence starting at off. It does
// not follow compression pointers: appendRoute53Answer (rr.go) never emits
// one for anything it writes, so a pointer byte here would mean the test is
// decoding something this package did not produce.
func decodeName(t *testing.T, msg []byte, off int) (string, int) {
	t.Helper()
	var labels []string
	for {
		if off >= len(msg) {
			t.Fatalf("name runs past end of message")
		}
		l := int(msg[off])
		off++
		if l == 0 {
			break
		}
		if l&0xc0 != 0 {
			t.Fatalf("unexpected compression pointer at offset %d", off-1)
		}
		if off+l > len(msg) {
			t.Fatalf("label runs past end of message")
		}
		labels = append(labels, string(msg[off:off+l]))
		off += l
	}
	return strings.Join(labels, "."), off
}

// decodeSections decodes the ANSWER and AUTHORITY sections of reply.
func decodeSections(t *testing.T, reply []byte) (answers, authority []wireRR) {
	t.Helper()
	if len(reply) < headerLen {
		t.Fatalf("short reply: %d bytes", len(reply))
	}
	ancount := int(binary.BigEndian.Uint16(reply[6:8]))
	nscount := int(binary.BigEndian.Uint16(reply[8:10]))

	_, off := decodeName(t, reply, headerLen)
	off += 4 // QTYPE + QCLASS

	readRR := func() wireRR {
		name, o := decodeName(t, reply, off)
		off = o
		if off+10 > len(reply) {
			t.Fatalf("truncated RR header")
		}
		typ := binary.BigEndian.Uint16(reply[off : off+2])
		off += 2
		off += 2 // CLASS
		ttl := binary.BigEndian.Uint32(reply[off : off+4])
		off += 4
		rdlen := int(binary.BigEndian.Uint16(reply[off : off+2]))
		off += 2
		if off+rdlen > len(reply) {
			t.Fatalf("truncated RDATA")
		}
		rdata := reply[off : off+rdlen]
		off += rdlen
		return wireRR{Name: name, Type: typ, TTL: ttl, RData: rdata}
	}
	for range ancount {
		answers = append(answers, readRR())
	}
	for range nscount {
		authority = append(authority, readRR())
	}
	return answers, authority
}

func decodeA(t *testing.T, rr wireRR) netip.Addr {
	t.Helper()
	if len(rr.RData) != 4 {
		t.Fatalf("A rdata length = %d, want 4", len(rr.RData))
	}
	return netip.AddrFrom4([4]byte(rr.RData))
}

// route53TestServer builds a server with no split-horizon zone of its own —
// isolating these tests to Route 53's own answers — and the given source
// wired in from the start.
func route53TestServer(src Route53Source) *Server {
	srv := NewServer(":0", NewZone(netip.Addr{}), nil).
		WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})
	srv.SetRoute53(src)
	return srv
}

// A hosted zone's A record must answer with exactly what was stored, TTL
// included — the core of "real DNS serving from hosted-zone records".
func TestRespond_route53AnswersA(t *testing.T) {
	fake := &fakeRoute53{}
	fake.set("app.example.com", TypeA, Route53Answer{
		Rcode:   RcodeNoError,
		Answers: []RR{{Name: "app.example.com", Type: TypeA, TTL: 123, Value: "10.0.0.5"}},
	})
	srv := route53TestServer(fake)

	reply, forward := srv.respond(context.Background(), buildQuery(t, "app.example.com", typeA), nil, netip.MustParseAddr("172.18.0.9"))
	if forward {
		t.Fatalf("a name a hosted zone claims must never be forwarded")
	}
	if got := rcodeOf(reply); got != rcodeNoError {
		t.Fatalf("rcode = %d, want NOERROR", got)
	}
	answers, _ := decodeSections(t, reply)
	if len(answers) != 1 {
		t.Fatalf("answers = %d, want 1", len(answers))
	}
	if got := decodeA(t, answers[0]); got.String() != "10.0.0.5" {
		t.Errorf("A = %s, want 10.0.0.5", got)
	}
	if answers[0].TTL != 123 {
		t.Errorf("TTL = %d, want 123 (the record's own TTL, not the split-horizon default)", answers[0].TTL)
	}
}

// A CNAME chain must be returned in full: the CNAME record(s) followed by
// whatever finally answers the query, all in one response.
func TestRespond_route53AnswersCNAMEChain(t *testing.T) {
	fake := &fakeRoute53{}
	fake.set("www.example.com", TypeA, Route53Answer{
		Rcode: RcodeNoError,
		Answers: []RR{
			{Name: "www.example.com", Type: TypeCNAME, TTL: 300, Value: "app.example.com"},
			{Name: "app.example.com", Type: TypeA, TTL: 60, Value: "10.0.0.5"},
		},
	})
	srv := route53TestServer(fake)

	reply, _ := srv.respond(context.Background(), buildQuery(t, "www.example.com", typeA), nil, netip.MustParseAddr("172.18.0.9"))
	answers, _ := decodeSections(t, reply)
	if len(answers) != 2 {
		t.Fatalf("answers = %d, want 2 (CNAME + A)", len(answers))
	}
	if answers[0].Type != TypeCNAME || answers[0].Name != "www.example.com" {
		t.Errorf("answers[0] = %+v, want the CNAME at the queried name", answers[0])
	}
	if answers[1].Type != TypeA || answers[1].Name != "app.example.com" {
		t.Errorf("answers[1] = %+v, want the A record at the CNAME target", answers[1])
	}
	if got := decodeA(t, answers[1]); got.String() != "10.0.0.5" {
		t.Errorf("chased A = %s, want 10.0.0.5", got)
	}
}

// A wildcard record answers a name that has none of its own, using the
// queried name as the answer's owner rather than the literal "*." record.
func TestRespond_route53AnswersWildcard(t *testing.T) {
	fake := &fakeRoute53{}
	fake.set("anything.example.com", TypeA, Route53Answer{
		Rcode:   RcodeNoError,
		Answers: []RR{{Name: "anything.example.com", Type: TypeA, TTL: 30, Value: "10.0.0.9"}},
	})
	srv := route53TestServer(fake)

	reply, _ := srv.respond(context.Background(), buildQuery(t, "anything.example.com", typeA), nil, netip.MustParseAddr("172.18.0.9"))
	if got := rcodeOf(reply); got != rcodeNoError {
		t.Fatalf("rcode = %d, want NOERROR", got)
	}
	answers, _ := decodeSections(t, reply)
	if len(answers) != 1 || answers[0].Name != "anything.example.com" {
		t.Fatalf("answers = %+v, want one record owned by the queried name", answers)
	}
}

// A name no zone in the store claims at all is NXDOMAIN, with the zone's SOA
// attached in the authority section for negative caching.
func TestRespond_route53NXDOMAIN(t *testing.T) {
	fake := &fakeRoute53{}
	fake.set("missing.example.com", typeA, Route53Answer{
		Rcode:     RcodeNXDomain,
		Authority: []RR{{Name: "example.com", Type: TypeSOA, TTL: 900, Value: "ns-1.awsdns-00.com. awsdns-hostmaster.amazon.com. 1 7200 900 1209600 86400"}},
	})
	srv := route53TestServer(fake)

	reply, _ := srv.respond(context.Background(), buildQuery(t, "missing.example.com", typeA), nil, netip.MustParseAddr("172.18.0.9"))
	if got := rcodeOf(reply); got != rcodeNXDomain {
		t.Fatalf("rcode = %d, want NXDOMAIN (%d)", got, rcodeNXDomain)
	}
	answers, authority := decodeSections(t, reply)
	if len(answers) != 0 {
		t.Fatalf("answers = %+v, want none for NXDOMAIN", answers)
	}
	if len(authority) != 1 || authority[0].Type != TypeSOA {
		t.Fatalf("authority = %+v, want the zone's SOA", authority)
	}
}

// An ALIAS record resolves to Overcast's own reachable address — the only
// address that could be correct for an emulated ELB/CloudFront/API Gateway
// target in this environment — using the address the caller can actually
// reach, exactly like the split-horizon zone's own A answers.
func TestRespond_route53AliasResolvesToEmulatorAddress(t *testing.T) {
	fake := &fakeRoute53{}
	fake.set("alias.example.com", typeA, Route53Answer{
		Rcode: RcodeNoError,
		Alias: true, AliasName: "alias.example.com", AliasTTL: 60,
	})
	const ecsSideAddr = "172.19.0.5"
	srv := NewServer(":0", NewZone(netip.Addr{}), nil).
		WithLocator(fixedLocator{addr: netip.MustParseAddr(ecsSideAddr)})
	srv.SetRoute53(fake)

	reply, _ := srv.respond(context.Background(), buildQuery(t, "alias.example.com", typeA), nil, netip.MustParseAddr("172.18.0.9"))
	if got := rcodeOf(reply); got != rcodeNoError {
		t.Fatalf("rcode = %d, want NOERROR", got)
	}
	answers, _ := decodeSections(t, reply)
	if len(answers) != 1 {
		t.Fatalf("answers = %+v, want one A record", answers)
	}
	if got := decodeA(t, answers[0]); got.String() != ecsSideAddr {
		t.Errorf("alias resolved to %s, want the caller-reachable address %s", got, ecsSideAddr)
	}
	if answers[0].TTL != 60 {
		t.Errorf("TTL = %d, want the alias record's own TTL 60", answers[0].TTL)
	}
}

// A name a hosted zone claims must win over a name the split-horizon zone
// would otherwise have answered — Route 53's own records are always
// authoritative for their own zone, never shadowed by Overcast's emulator
// hostnames.
func TestRespond_route53TakesPrecedenceOverSplitHorizonZone(t *testing.T) {
	fake := &fakeRoute53{}
	fake.set("shared.localhost.overcast.sh", typeA, Route53Answer{
		Rcode:   RcodeNoError,
		Answers: []RR{{Name: "shared.localhost.overcast.sh", Type: TypeA, TTL: 45, Value: "10.1.2.3"}},
	})
	zone := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh")
	srv := NewServer(":0", zone, nil).WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})
	srv.SetRoute53(fake)

	reply, _ := srv.respond(context.Background(), buildQuery(t, "shared.localhost.overcast.sh", typeA), nil, netip.MustParseAddr("172.18.0.9"))
	answers, _ := decodeSections(t, reply)
	if len(answers) != 1 {
		t.Fatalf("answers = %+v, want one record", answers)
	}
	if got := decodeA(t, answers[0]); got.String() != "10.1.2.3" {
		t.Errorf("A = %s, want the Route 53 record 10.1.2.3, not the split-horizon fallback", got)
	}
	if answers[0].TTL != 45 {
		t.Errorf("TTL = %d, want the Route 53 record's own TTL 45, not the split-horizon default", answers[0].TTL)
	}
}

// With no Route53Source wired at all, behaviour must be exactly what it was
// before this feature existed: nothing to consult, so nothing changes for
// the split-horizon zone or forwarding.
func TestRespond_withoutARoute53SourceBehaviourIsUnchanged(t *testing.T) {
	zone := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh")
	srv := NewServer(":0", zone, nil).WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})

	reply, forward := srv.respond(context.Background(), buildQuery(t, "mybucket.s3.localhost.overcast.sh", typeA), nil, netip.MustParseAddr("172.18.0.9"))
	if forward {
		t.Fatalf("an owned name must not be forwarded")
	}
	if got := rcodeOf(reply); got != rcodeNoError {
		t.Fatalf("rcode = %d, want NOERROR", got)
	}
}

// SetRoute53 runs after Serve has started, same as SetGuard, because the
// resolver binds before the Route 53 service is constructed. Storing it must
// be race-free.
func TestSetRoute53_isSafeWhileServing(t *testing.T) {
	zone := NewZone(netip.MustParseAddr("172.18.0.2"), "localhost.overcast.sh")
	srv := NewServer(":0", zone, nil).WithLocator(fixedLocator{addr: netip.MustParseAddr("172.18.0.2")})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 50 {
			srv.respond(context.Background(), buildQuery(t, "mybucket.s3.localhost.overcast.sh", typeA), nil, netip.MustParseAddr("172.18.0.9"))
		}
	}()
	for range 50 {
		srv.SetRoute53(&fakeRoute53{})
		srv.SetRoute53(nil)
	}
	<-done
}

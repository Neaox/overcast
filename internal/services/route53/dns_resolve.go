package route53

import (
	"context"
	"strings"

	"github.com/overcast-sh/overcast/internal/dns"
)

// This file makes Route 53 an authoritative data source for Overcast's own
// DNS resolver (internal/dns): the data plane half of real DNS serving. The
// control plane above (core.go, handler_*.go) is unchanged by it.
//
// Design, in one paragraph: a query lands on the hosted zone whose name is
// the longest suffix match for the query name (the "most specific" zone,
// exactly as real delegation works); ties between a public and a private
// zone of the same name favour the private one. Record lookup walks CNAME
// chains across zones, expands one-label wildcards, and falls back to
// NODATA (the name exists, wrong type) or NXDOMAIN (it does not exist at
// all) with the zone's SOA attached for negative caching. ALIAS records are
// reported back to internal/dns rather than resolved here, because the only
// address that could ever be correct for one — Overcast's own, as seen by
// whichever peer asked — is a fact internal/dns holds and this package does
// not.
//
// Documented simplification: every container Overcast starts answers
// against every zone the store holds, public or private alike, with no VPC
// filtering. Real Route 53 only serves a private zone's records to resolvers
// inside an associated VPC; Overcast does not model "inside a VPC" as a
// property of a DNS query, and AssociateVPCWithHostedZone is not implemented
// at all yet (see docs/services/route53.md). Treating every zone as
// universally visible is the honest, simple choice named in issue #1189:
// a query that would get NXDOMAIN against real AWS gets an answer here, but
// nothing that resolves in the emulator ever fails to resolve against real
// AWS for a container correctly associated with the zone's VPC.
//
// Also documented: this only serves record types internal/dns knows how to
// put on the wire (A, AAAA, CNAME, MX, TXT, NS, SOA, plus ALIAS). A query for
// NAPTR/PTR/SRV/SPF/CAA/DS/TLSA/SSHFP/SVCB/HTTPS — all storable via
// ChangeResourceRecordSets — gets NODATA/NXDOMAIN like any other record type
// absent at that name, even when one is actually stored there.

// maxCNAMEHops bounds a CNAME chase. Real zones are never anywhere near this
// deep; the limit exists so a record set that (deliberately or not) points
// its CNAME at itself, directly or through a cycle, terminates the query
// instead of looping the store scan forever.
const maxCNAMEHops = 8

// dnsTypes maps Route 53's stored type strings to the wire values
// internal/dns understands. Types Route 53 accepts but internal/dns cannot
// encode (NAPTR, PTR, SRV, SPF, CAA, DS, TLSA, SSHFP, SVCB, HTTPS) are
// intentionally absent: a query for one of them is answered NODATA/NXDOMAIN
// like any other type that has no data at the name, per the package doc above.
var dnsTypes = map[string]uint16{
	"A":     dns.TypeA,
	"AAAA":  dns.TypeAAAA,
	"CNAME": dns.TypeCNAME,
	"NS":    dns.TypeNS,
	"SOA":   dns.TypeSOA,
	"MX":    dns.TypeMX,
	"TXT":   dns.TypeTXT,
}

// dnsTypeName is dnsTypes in reverse.
func dnsTypeName(t uint16) string {
	for name, v := range dnsTypes {
		if v == t {
			return name
		}
	}
	return ""
}

// Lookup implements dns.Route53Source: it is what makes the DNS resolver
// treat this service's store as authoritative for the zones it holds.
func (s *Service) Lookup(ctx context.Context, qname string, qtype uint16) (dns.Route53Answer, bool) {
	qname = normalizeDNSName(qname)
	zones, err := s.listAllZones(ctx)
	if err != nil || len(zones) == 0 {
		return dns.Route53Answer{}, false
	}
	zone := bestZone(zones, qname)
	if zone == nil {
		return dns.Route53Answer{}, false
	}

	var answers []dns.RR
	cur, curZone := qname, zone
	visited := map[string]bool{}
	for hop := 0; hop < maxCNAMEHops; hop++ {
		if visited[cur] {
			// A CNAME cycle: stand on whatever chain was already built rather
			// than loop the store forever.
			return dns.Route53Answer{Rcode: dns.RcodeNoError, Answers: answers}, true
		}
		visited[cur] = true

		rrsets, err := s.listRRSets(ctx, curZone.Id)
		if err != nil {
			return dns.Route53Answer{Rcode: dns.RcodeServFail}, true
		}

		if match, aliasName, aliasTTL, hasAlias := matchAt(rrsets, cur, qtype); len(match) > 0 || hasAlias {
			if hasAlias {
				return dns.Route53Answer{Rcode: dns.RcodeNoError, Answers: answers, Alias: true, AliasName: aliasName, AliasTTL: aliasTTL}, true
			}
			answers = append(answers, match...)
			return dns.Route53Answer{Rcode: dns.RcodeNoError, Answers: answers}, true
		}

		if qtype != dns.TypeCNAME && qtype != dns.TypeANY {
			if cname := findExact(rrsets, cur, "CNAME"); cname != nil && !cname.isAlias() {
				target := normalizeDNSName(firstValue(cname))
				answers = append(answers, dns.RR{Name: cur, Type: dns.TypeCNAME, TTL: ttl32(cname.TTL), Value: target})
				next := bestZone(zones, target)
				if next == nil {
					// The chain continues into a name nothing here owns —
					// that is the whole answer; the client re-queries for it.
					return dns.Route53Answer{Rcode: dns.RcodeNoError, Answers: answers}, true
				}
				cur, curZone = target, next
				continue
			}
		}

		if match, aliasName, aliasTTL, hasAlias := matchWildcard(rrsets, cur, qtype); len(match) > 0 || hasAlias {
			if hasAlias {
				return dns.Route53Answer{Rcode: dns.RcodeNoError, Answers: answers, Alias: true, AliasName: aliasName, AliasTTL: aliasTTL}, true
			}
			answers = append(answers, match...)
			return dns.Route53Answer{Rcode: dns.RcodeNoError, Answers: answers}, true
		}

		soa := soaAuthority(rrsets, curZone)
		if existsAtName(rrsets, cur) {
			return dns.Route53Answer{Rcode: dns.RcodeNoError, Answers: answers, Authority: soa}, true // NODATA
		}
		return dns.Route53Answer{Rcode: dns.RcodeNXDomain, Authority: soa}, true
	}
	return dns.Route53Answer{Rcode: dns.RcodeNoError, Answers: answers}, true
}

// bestZone returns the most specific hosted zone claiming qname (the apex or
// a subdomain of it), preferring a private zone over a public one when two
// zones of the same specificity both claim it, and breaking any further tie
// on zone ID so the choice is stable across scans.
func bestZone(zones []*HostedZone, qname string) *HostedZone {
	var best *HostedZone
	for _, z := range zones {
		if !inZone(qname, z.Name) {
			continue
		}
		switch {
		case best == nil:
			best = z
		case len(z.Name) > len(best.Name):
			best = z
		case len(z.Name) < len(best.Name):
			// less specific than what's already found; keep best.
		case z.PrivateZone && !best.PrivateZone:
			best = z
		case z.PrivateZone == best.PrivateZone && z.Id < best.Id:
			best = z
		}
	}
	return best
}

// matchAt returns the records at the exact owner name cur matching qtype (or
// every type dns can encode, for ANY), expanded to wire RRs. If an ALIAS
// record matches instead, hasAlias reports that and match is nil — the
// caller resolves the address itself.
func matchAt(rrsets []*ResourceRecordSet, cur string, qtype uint16) (match []dns.RR, aliasName string, aliasTTL uint32, hasAlias bool) {
	for _, rr := range rrsets {
		if rr.Name != cur || !typeMatches(rr.Type, qtype) {
			continue
		}
		if rr.isAlias() {
			// Only an A-typed alias has an address this environment can
			// supply; anything else (AAAA, CNAME-shaped aliases) is treated
			// as present-but-unservable, which falls through to NODATA.
			if rr.Type == "A" {
				return nil, cur, ttl32(rr.TTL), true
			}
			continue
		}
		match = append(match, expand(rr, cur)...)
	}
	return match, "", 0, false
}

// matchWildcard is matchAt against the zone's wildcard record sets, with the
// synthesized answer's owner name set to the query name per RFC 1034 §4.3.3.
func matchWildcard(rrsets []*ResourceRecordSet, cur string, qtype uint16) (match []dns.RR, aliasName string, aliasTTL uint32, hasAlias bool) {
	for _, rr := range rrsets {
		if !strings.HasPrefix(rr.Name, "*.") || !wildcardCovers(rr.Name, cur) || !typeMatches(rr.Type, qtype) {
			continue
		}
		if rr.isAlias() {
			if rr.Type == "A" {
				return nil, cur, ttl32(rr.TTL), true
			}
			continue
		}
		match = append(match, expand(rr, cur)...)
	}
	return match, "", 0, false
}

// wildcardCovers reports whether wildcardName ("*.suffix") matches qname:
// AWS wildcards replace exactly one label, so qname must have precisely one
// more label than suffix. See docs/services/route53.md.
func wildcardCovers(wildcardName, qname string) bool {
	suffix := wildcardName[2:] // strip "*."
	labels := strings.SplitN(strings.TrimSuffix(qname, "."), ".", 2)
	if len(labels) != 2 {
		return false
	}
	return strings.EqualFold(normalizeDNSName(labels[1]), suffix)
}

// findExact returns the (non-wildcard) record set at name with the given
// type, or nil.
func findExact(rrsets []*ResourceRecordSet, name, rrType string) *ResourceRecordSet {
	for _, rr := range rrsets {
		if rr.Name == name && rr.Type == rrType {
			return rr
		}
	}
	return nil
}

// existsAtName reports whether any record set at all owns name — the
// NODATA/NXDOMAIN distinction.
func existsAtName(rrsets []*ResourceRecordSet, name string) bool {
	for _, rr := range rrsets {
		if rr.Name == name {
			return true
		}
	}
	return false
}

// soaAuthority returns the zone's apex SOA record as a one-element Authority
// slice, or nil if it is somehow missing (every zone gets one at creation
// and it cannot be deleted, but persisted data can always be malformed).
func soaAuthority(rrsets []*ResourceRecordSet, zone *HostedZone) []dns.RR {
	soa := findExact(rrsets, zone.Name, "SOA")
	if soa == nil || len(soa.ResourceRecords) == 0 {
		return nil
	}
	return []dns.RR{{Name: zone.Name, Type: dns.TypeSOA, TTL: ttl32(soa.TTL), Value: soa.ResourceRecords[0]}}
}

// typeMatches reports whether a stored record type string should be
// considered for qtype: an exact match, or any encodable type for ANY.
func typeMatches(storedType string, qtype uint16) bool {
	if qtype == dns.TypeANY {
		_, ok := dnsTypes[storedType]
		return ok
	}
	return dnsTypeName(qtype) == storedType
}

// expand turns one non-alias record set into wire RRs, one per stored value,
// with owner set explicitly so a wildcard match reports the queried name
// rather than the literal "*.example.com" record.
func expand(rr *ResourceRecordSet, owner string) []dns.RR {
	t, ok := dnsTypes[rr.Type]
	if !ok {
		return nil
	}
	out := make([]dns.RR, 0, len(rr.ResourceRecords))
	for _, v := range rr.ResourceRecords {
		out = append(out, dns.RR{Name: owner, Type: t, TTL: ttl32(rr.TTL), Value: v})
	}
	return out
}

// firstValue returns a record set's first stored value, or "".
func firstValue(rr *ResourceRecordSet) string {
	if len(rr.ResourceRecords) == 0 {
		return ""
	}
	return rr.ResourceRecords[0]
}

// ttl32 clamps a stored TTL (int64, per the AWS wire shape) to the uint32 the
// wire format uses; Route 53 itself caps TTL well within range (max
// 2147483647), so this only guards against a negative or corrupt value.
func ttl32(ttl int64) uint32 {
	if ttl < 0 {
		return 0
	}
	if ttl > 0xffffffff {
		return 0xffffffff
	}
	return uint32(ttl)
}

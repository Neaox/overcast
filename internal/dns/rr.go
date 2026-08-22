package dns

import (
	"context"
	"encoding/binary"
	"net/netip"
	"strconv"
	"strings"
)

// Record type wire values, exported for a Route53Source implementation
// outside this package: it has to label the RR values it hands back with the
// same numbers this package parses out of the question section.
const (
	TypeA     = typeA
	TypeNS    = typeNS
	TypeCNAME = typeCNAME
	TypeSOA   = typeSOA
	TypeMX    = typeMX
	TypeTXT   = typeTXT
	TypeAAAA  = typeAAAA
	TypeANY   = typeANY
)

// Response codes a Route53Source may set on Route53Answer.Rcode, exported for
// the same reason the type constants above are: a caller outside this
// package has to build the values, not just receive them.
const (
	RcodeNoError  = rcodeNoError
	RcodeServFail = rcodeServFail
	RcodeNXDomain = rcodeNXDomain
)

// RR is one resource record ready to be placed in a DNS answer: an owner
// name, RFC 1035 type, TTL, and rdata already in the record's AWS-format
// text. That text is exactly what Route 53 already stores in
// ResourceRecords/AliasTarget, so a Route53Source needs no translation
// beyond deciding which records answer the query — this package owns turning
// the text into wire rdata.
//
// Value's shape depends on Type: an IP literal for A/AAAA; a domain name for
// CNAME/NS; "<preference> <exchange>" for MX; one or more (optionally
// quoted) character-strings for TXT; and
// "mname rname serial refresh retry expire minimum" for SOA — the same text
// Route 53's own default apex SOA record is stored as.
type RR struct {
	Name  string
	Type  uint16
	TTL   uint32
	Value string
}

// Route53Answer is what a Route53Source says about one query. It is always
// interpreted as authoritative: the server never forwards a name a
// Route53Source claims, whatever the answer.
type Route53Answer struct {
	// Rcode is rcodeNoError (a positive answer, or NODATA when Answers is
	// empty) or rcodeNXDomain.
	Rcode byte

	// Answers go in the response's ANSWER section, in order — a CNAME chain
	// followed by whichever record finally answered it, or nothing at all
	// for NODATA/NXDOMAIN.
	Answers []RR

	// Authority carries the zone's SOA record for a negative response
	// (NODATA or NXDOMAIN) per RFC 2308, so a resolver has something to key
	// negative caching on. Empty for a positive answer.
	Authority []RR

	// Alias means the query landed on an ALIAS record whose target is a
	// hostname the emulator's host-routing layer understands, rather than
	// something outside it. There is exactly one correct address to give in
	// that case — Overcast's own, reachable from whichever peer asked — so
	// the server fills it in itself rather than the Route53Source guessing
	// at an address it has no way to compute. AliasName/AliasTTL name the
	// owner and TTL to answer with; only meaningful when Alias is true.
	Alias     bool
	AliasName string
	AliasTTL  uint32
}

// Route53Source resolves a DNS query against Route 53 hosted-zone data. It is
// consulted before s.zone and before forwarding: a name some hosted zone in
// the store claims is answered from that zone in full and never relayed
// upstream — Overcast IS that zone's name server, the same way AWS's own
// would be.
//
// Implementations must be safe for concurrent use; Lookup runs on the query
// path.
type Route53Source interface {
	// Lookup reports whether some hosted zone claims qname (the apex or any
	// subdomain of one), and if so, the authoritative answer for qtype.
	// found=false means no hosted zone in the store owns this name at all,
	// and the caller should fall back to its own zone/forwarding behaviour.
	Lookup(ctx context.Context, qname string, qtype uint16) (ans Route53Answer, found bool)
}

// appendRoute53Answer encodes ans as the reply to msg's query. Unlike the
// allocation-free fast path in message.go, this builds each record set into
// its own scratch buffer first — Route 53 answers are rare enough (a store
// scan already happened to produce ans) that the extra allocation is not
// worth avoiding, and it lets a record with unencodable rdata be dropped
// without corrupting the counts already written to the header.
func appendRoute53Answer(dst, msg []byte, q question, ans Route53Answer) []byte {
	answers := encodeRRs(ans.Answers)
	authority := encodeRRs(ans.Authority)

	flags := flagResponse | flagRecursOK | flagAuthority
	if msg[2]&0x01 != 0 {
		flags |= flagRecursWant
	}
	flags |= uint16(ans.Rcode) & 0x000f

	dst = append(dst, msg[0], msg[1])
	dst = binary.BigEndian.AppendUint16(dst, flags)
	dst = binary.BigEndian.AppendUint16(dst, 1) // QDCOUNT
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(answers)))
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(authority)))
	dst = binary.BigEndian.AppendUint16(dst, 0) // ARCOUNT
	dst = append(dst, msg[headerLen:q.end]...)
	for _, b := range answers {
		dst = append(dst, b...)
	}
	for _, b := range authority {
		dst = append(dst, b...)
	}
	return dst
}

// encodeRRs encodes every RR that can be encoded, silently dropping any that
// cannot — malformed stored data should degrade the answer, not corrupt the
// packet or crash the query path.
func encodeRRs(rrs []RR) [][]byte {
	if len(rrs) == 0 {
		return nil
	}
	out := make([][]byte, 0, len(rrs))
	for _, rr := range rrs {
		if buf, ok := encodeRR(rr); ok {
			out = append(out, buf)
		}
	}
	return out
}

func encodeRR(rr RR) ([]byte, bool) {
	rdata, ok := encodeRData(rr)
	if !ok {
		return nil, false
	}
	buf := appendName(nil, rr.Name)
	buf = binary.BigEndian.AppendUint16(buf, rr.Type)
	buf = binary.BigEndian.AppendUint16(buf, classIN)
	buf = binary.BigEndian.AppendUint32(buf, rr.TTL)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(rdata)))
	buf = append(buf, rdata...)
	return buf, true
}

func encodeRData(rr RR) ([]byte, bool) {
	switch rr.Type {
	case typeA:
		addr, err := netip.ParseAddr(strings.TrimSpace(rr.Value))
		if err != nil {
			return nil, false
		}
		addr = addr.Unmap()
		if !addr.Is4() {
			return nil, false
		}
		b := addr.As4()
		return b[:], true
	case typeAAAA:
		addr, err := netip.ParseAddr(strings.TrimSpace(rr.Value))
		if err != nil {
			return nil, false
		}
		if addr.Is4() {
			return nil, false
		}
		b := addr.As16()
		return b[:], true
	case typeCNAME, typeNS:
		name := strings.TrimSpace(rr.Value)
		if name == "" {
			return nil, false
		}
		return appendName(nil, name), true
	case typeMX:
		pref, exch, ok := splitMX(rr.Value)
		if !ok {
			return nil, false
		}
		buf := binary.BigEndian.AppendUint16(nil, pref)
		return appendName(buf, exch), true
	case typeSOA:
		return encodeSOA(rr.Value)
	case typeTXT:
		return encodeTXT(rr.Value), true
	default:
		return nil, false
	}
}

// splitMX parses Route 53's stored MX value, "<preference> <exchange>".
func splitMX(v string) (uint16, string, bool) {
	v = strings.TrimSpace(v)
	i := strings.IndexAny(v, " \t")
	if i < 0 {
		return 0, "", false
	}
	pref, err := strconv.ParseUint(strings.TrimSpace(v[:i]), 10, 16)
	if err != nil {
		return 0, "", false
	}
	exch := strings.TrimSpace(v[i+1:])
	if exch == "" {
		return 0, "", false
	}
	return uint16(pref), exch, true
}

// encodeSOA parses Route 53's stored SOA value —
// "mname rname serial refresh retry expire minimum", exactly the text
// defaultRecordSets writes for a zone's apex SOA record — into wire form.
func encodeSOA(v string) ([]byte, bool) {
	fields := strings.Fields(v)
	if len(fields) != 7 {
		return nil, false
	}
	var nums [5]uint32
	for i := range nums {
		n, err := strconv.ParseUint(fields[2+i], 10, 32)
		if err != nil {
			return nil, false
		}
		nums[i] = uint32(n)
	}
	buf := appendName(nil, fields[0])
	buf = appendName(buf, fields[1])
	for _, n := range nums {
		buf = binary.BigEndian.AppendUint32(buf, n)
	}
	return buf, true
}

// encodeTXT renders a TXT record's stored value as one or more RFC 1035
// character-strings.
func encodeTXT(v string) []byte {
	var buf []byte
	for _, s := range splitTXTStrings(v) {
		data := []byte(s)
		for len(data) > 255 {
			buf = append(buf, 255)
			buf = append(buf, data[:255]...)
			data = data[255:]
		}
		buf = append(buf, byte(len(data)))
		buf = append(buf, data...)
	}
	return buf
}

// splitTXTStrings splits a TXT value into the character-strings it encodes.
// AWS's convention wraps each one in double quotes (with \" and \\ escapes),
// possibly several concatenated with spaces in between; a value with no
// quoting at all is treated as a single string, chunked to fit on the wire.
func splitTXTStrings(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return []string{""}
	}
	if v[0] != '"' {
		return []string{v}
	}
	var out []string
	for len(v) > 0 {
		v = strings.TrimSpace(v)
		if len(v) == 0 || v[0] != '"' {
			break
		}
		var sb strings.Builder
		i := 1
		for i < len(v) {
			c := v[i]
			if c == '\\' && i+1 < len(v) {
				sb.WriteByte(v[i+1])
				i += 2
				continue
			}
			if c == '"' {
				i++
				break
			}
			sb.WriteByte(c)
			i++
		}
		out = append(out, sb.String())
		v = v[i:]
	}
	if len(out) == 0 {
		return []string{v}
	}
	return out
}

// appendName encodes name as a sequence of length-prefixed labels. Unlike
// appendTo's single compression pointer back to the question, names here are
// written out in full: an RR's owner may differ from the question (a CNAME
// chain's later links, an MX exchange, an SOA's mname/rname), and there is no
// general compression table to point them at.
func appendName(dst []byte, name string) []byte {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" {
		return append(dst, 0)
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			continue
		}
		if len(label) > maxLabel {
			label = label[:maxLabel]
		}
		dst = append(dst, byte(len(label)))
		dst = append(dst, label...)
	}
	return append(dst, 0)
}

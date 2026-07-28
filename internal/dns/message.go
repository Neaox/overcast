package dns

import (
	"encoding/binary"
	"net/netip"
)

// Just enough of RFC 1035's wire format to answer an A query and echo a
// question back. Anything this package does not own is relayed as opaque
// bytes, so there is no need for a general-purpose message parser — and not
// having one keeps the query path allocation-free.
const (
	headerLen  = 12
	maxNameLen = 255 // RFC 1035 §2.3.4
	maxLabel   = 63

	typeA    uint16 = 1
	typeAAAA uint16 = 28
	typeANY  uint16 = 255
	classIN  uint16 = 1

	rcodeNoError  byte = 0
	rcodeFormErr  byte = 1
	rcodeServFail byte = 2
	rcodeRefused  byte = 5

	// flagResponse etc. are the header bits this package sets. RD is copied
	// from the query, as RFC 1035 §4.1.1 requires.
	flagResponse   uint16 = 0x8000 // QR
	flagAuthority  uint16 = 0x0400 // AA
	flagRecursOK   uint16 = 0x0080 // RA
	flagRecursWant uint16 = 0x0100 // RD

	// defaultTTL is deliberately short: Overcast's address changes whenever its
	// container is recreated, and a stale cached answer is unreachable.
	defaultTTL uint32 = 30
)

// question is the parsed question section of a query. name points into a
// caller-supplied scratch buffer and is only valid while that buffer is.
type question struct {
	name   []byte // dot-separated, no trailing dot
	qtype  uint16
	qclass uint16
	end    int // offset just past the question, where an answer may begin
}

// parseQuestion decodes the single question of a standard query, writing the
// decoded name into scratch. It reports false for anything it will not answer:
// a response, a non-query opcode, a message without exactly one question, or a
// malformed name.
//
// Compression pointers are rejected rather than followed. They are illegal in
// a query's question section, and refusing them means the decoder cannot be
// driven into a pointer loop by a hostile datagram.
func parseQuestion(msg, scratch []byte) (question, bool) {
	if len(msg) < headerLen {
		return question{}, false
	}
	if msg[2]&0x80 != 0 { // QR: a response, not a query
		return question{}, false
	}
	if msg[2]&0x78 != 0 { // OPCODE must be QUERY
		return question{}, false
	}
	if binary.BigEndian.Uint16(msg[4:6]) != 1 { // QDCOUNT
		return question{}, false
	}

	off, n := headerLen, 0
	for {
		if off >= len(msg) {
			return question{}, false
		}
		l := int(msg[off])
		off++
		if l == 0 {
			break
		}
		if l > maxLabel { // also catches the 0xc0 pointer form
			return question{}, false
		}
		if off+l > len(msg) {
			return question{}, false
		}
		if n > 0 {
			if n+1 >= len(scratch) {
				return question{}, false
			}
			scratch[n] = '.'
			n++
		}
		if n+l > len(scratch) {
			return question{}, false
		}
		copy(scratch[n:], msg[off:off+l])
		n += l
		off += l
	}
	if off+4 > len(msg) {
		return question{}, false
	}
	return question{
		name:   scratch[:n],
		qtype:  binary.BigEndian.Uint16(msg[off : off+2]),
		qclass: binary.BigEndian.Uint16(msg[off+2 : off+4]),
		end:    off + 4,
	}, true
}

// answer is what the server decided to say. An invalid addr means no answer
// record — NODATA when rcode is NOERROR, an error otherwise.
type answer struct {
	rcode         byte
	authoritative bool
	addr          netip.Addr
}

// appendTo encodes the reply to msg's query into dst and returns it. The
// question is echoed verbatim, so the answer's owner name can be the two-byte
// compression pointer to offset 12 that RFC 1035 §4.1.4 provides for.
func (a answer) appendTo(dst, msg []byte, q question) []byte {
	flags := flagResponse | flagRecursOK
	if a.authoritative {
		flags |= flagAuthority
	}
	if msg[2]&0x01 != 0 {
		flags |= flagRecursWant
	}
	flags |= uint16(a.rcode) & 0x000f

	var ancount uint16
	if a.addr.IsValid() {
		ancount = 1
	}

	dst = append(dst, msg[0], msg[1]) // transaction ID
	dst = binary.BigEndian.AppendUint16(dst, flags)
	dst = binary.BigEndian.AppendUint16(dst, 1) // QDCOUNT
	dst = binary.BigEndian.AppendUint16(dst, ancount)
	dst = binary.BigEndian.AppendUint16(dst, 0) // NSCOUNT
	dst = binary.BigEndian.AppendUint16(dst, 0) // ARCOUNT
	dst = append(dst, msg[headerLen:q.end]...)

	if ancount == 1 {
		v4 := a.addr.As4()
		dst = append(dst, 0xc0, headerLen) // NAME: pointer to the question
		dst = binary.BigEndian.AppendUint16(dst, typeA)
		dst = binary.BigEndian.AppendUint16(dst, classIN)
		dst = binary.BigEndian.AppendUint32(dst, defaultTTL)
		dst = binary.BigEndian.AppendUint16(dst, 4) // RDLENGTH
		dst = append(dst, v4[:]...)
	}
	return dst
}

// appendHeaderOnlyError encodes a reply carrying nothing but an rcode, for a
// query too malformed to echo. Callers must have checked len(msg) >= headerLen.
func appendHeaderOnlyError(dst, msg []byte, rcode byte) []byte {
	dst = append(dst, msg[0], msg[1])
	dst = binary.BigEndian.AppendUint16(dst, flagResponse|flagRecursOK|uint16(rcode)&0x000f)
	dst = binary.BigEndian.AppendUint16(dst, 0) // QDCOUNT
	dst = binary.BigEndian.AppendUint16(dst, 0) // ANCOUNT
	dst = binary.BigEndian.AppendUint16(dst, 0) // NSCOUNT
	dst = binary.BigEndian.AppendUint16(dst, 0) // ARCOUNT
	return dst
}

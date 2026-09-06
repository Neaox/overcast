package sqs

// messageattrmd5.go computes MD5OfMessageAttributes, the digest SQS returns
// beside MD5OfMessageBody whenever a message carries attributes. Several AWS
// SDKs verify it against a digest they compute themselves and raise on a
// mismatch, so it is a checksum a client acts on rather than a decorative
// field.

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"maps"
	"slices"
)

// Transport-type markers AWS writes between an attribute's data type and its
// value, distinguishing which of the two value members carries it.
const (
	attrTransportString byte = 1
	attrTransportBinary byte = 2
)

// md5OfMessageAttributes returns the MD5 digest AWS documents for a message's
// attributes, hex-encoded, or "" when there are none — AWS omits the member
// entirely rather than sending the digest of an empty buffer.
//
// The encoding, from AWS's "calculating the MD5 message digest for message
// attributes": visit the attributes in ascending name order and, for each,
// append the length-prefixed name, the length-prefixed data type, a
// transport-type byte saying which value member is present, and the
// length-prefixed value. Lengths are 4-byte big-endian; strings are UTF-8.
// The data type is the full string, so a custom label ("Number.int") is
// digested as written.
func md5OfMessageAttributes(attrs map[string]MessageAttribute) string {
	if len(attrs) == 0 {
		return ""
	}

	names := slices.Sorted(maps.Keys(attrs))

	var buf bytes.Buffer
	for _, name := range names {
		attr := attrs[name]
		writeLengthPrefixed(&buf, []byte(name))
		writeLengthPrefixed(&buf, []byte(attr.DataType))
		// A binary attribute is the one with bytes in BinaryValue; everything
		// else — including an explicitly empty string value — is a string
		// attribute, which is how AWS's own encoder branches.
		if len(attr.BinaryValue) > 0 {
			buf.WriteByte(attrTransportBinary)
			writeLengthPrefixed(&buf, attr.BinaryValue)
			continue
		}
		buf.WriteByte(attrTransportString)
		writeLengthPrefixed(&buf, []byte(attr.StringValue))
	}

	sum := md5.Sum(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// writeLengthPrefixed appends b to buf behind its 4-byte big-endian length.
func writeLengthPrefixed(buf *bytes.Buffer, b []byte) {
	_ = binary.Write(buf, binary.BigEndian, uint32(len(b)))
	buf.Write(b)
}

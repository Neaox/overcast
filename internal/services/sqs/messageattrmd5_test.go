package sqs

import "testing"

// TestMD5OfMessageAttributes_digests pins md5OfMessageAttributes against
// digests computed independently from AWS's documented encoding (see
// md5OfMessageAttributes' own comment for the rules): each attribute
// contributes length-prefixed name and data-type bytes, a transport-type byte
// (1 = string value, 2 = binary value) and the length-prefixed value, with
// attributes visited in ascending name order.
//
// The expectations are literals rather than a second Go implementation on
// purpose: a reimplementation in the test would agree with the code under test
// by construction and prove nothing about the algorithm.
func TestMD5OfMessageAttributes_digests(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]MessageAttribute
		want  string
	}{
		{
			name:  "no attributes",
			attrs: nil,
			want:  "",
		},
		{
			name: "single string attribute",
			attrs: map[string]MessageAttribute{
				"attr1": {DataType: "String", StringValue: "value1"},
			},
			want: "3bc3f392bdd1097ba0b434f65d468d2e",
		},
		{
			name: "attributes are digested in name order, not map order",
			attrs: map[string]MessageAttribute{
				"b": {DataType: "String", StringValue: "2"},
				"a": {DataType: "Number", StringValue: "1"},
			},
			want: "02bc784682167881b554c14e8156ce95",
		},
		{
			name: "binary attribute uses the binary transport type",
			attrs: map[string]MessageAttribute{
				"bin": {DataType: "Binary", BinaryValue: []byte("hello")},
			},
			want: "697e1b13993f891cb8a58a8d5639c06e",
		},
		{
			name: "mixed string and binary attributes",
			attrs: map[string]MessageAttribute{
				"s": {DataType: "String", StringValue: "v"},
				"b": {DataType: "Binary", BinaryValue: []byte{0, 1, 2, 255}},
			},
			want: "1d7f5f7bfd1309e889cb15d7eafabd81",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the digest is computed over the attribute set.
			got := md5OfMessageAttributes(tc.attrs)

			// Then: it matches AWS's documented digest.
			if got != tc.want {
				t.Errorf("md5OfMessageAttributes = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMD5OfMessageAttributes_valueChangesDigest proves the digest actually
// covers the value, so a client verifying it would notice a mangled attribute
// rather than only a mangled name.
func TestMD5OfMessageAttributes_valueChangesDigest(t *testing.T) {
	// Given: two attribute sets differing only in one value.
	before := md5OfMessageAttributes(map[string]MessageAttribute{
		"attr1": {DataType: "String", StringValue: "value1"},
	})

	// When: the value changes.
	after := md5OfMessageAttributes(map[string]MessageAttribute{
		"attr1": {DataType: "String", StringValue: "value2"},
	})

	// Then: the digest changes with it.
	if before == after {
		t.Errorf("digest %q unchanged after the attribute value changed", before)
	}
}

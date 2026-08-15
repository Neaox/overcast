package serviceutil_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Neaox/overcast/internal/serviceutil"
)

func TestTailBytes(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		maxBytes int
		want     string
	}{{
		name:     "shorter than the bound is returned whole",
		in:       "boot\nready\n",
		maxBytes: 64,
		want:     "boot\nready\n",
	}, {
		name:     "exactly the bound is returned whole",
		in:       "abcde",
		maxBytes: 5,
		want:     "abcde",
	}, {
		// The tail is the point: a log's diagnosis is its last lines, so what
		// goes is the head. Every truncate helper in the repo does the
		// opposite, which is why this one is not one of them.
		name:     "the head is dropped, not the tail",
		in:       "one\ntwo\nthree\nfour\nfive\n",
		maxBytes: 12,
		want:     "four\nfive\n",
	}, {
		// The cut lands wherever the byte count falls, and a first line
		// starting mid-word reads as corruption rather than as a bound.
		name:     "a part line at the front is trimmed to the line boundary",
		in:       "first line\nsecond line\nthird line\n",
		maxBytes: 20,
		want:     "third line\n",
	}, {
		// One enormous line — a serialized object, a base64 blob — has no
		// boundary to trim to, and trimming to the newline that ends it would
		// leave nothing at all.
		name:     "a single long line keeps its tail rather than emptying",
		in:       strings.Repeat("x", 40) + "yyyy\n",
		maxBytes: 5,
		want:     "yyyy\n",
	}, {
		// Cutting mid-rune leaves continuation bytes at the front, which every
		// consumer renders as U+FFFD. Only whole runes come out.
		name:     "a part rune at the front is dropped",
		in:       strings.Repeat("x", 20) + "héllo",
		maxBytes: 4,
		want:     "llo",
	}, {
		// A cut that lands on a line boundary needs no trimming, and trimming
		// it anyway would throw away a whole line for nothing.
		name:     "a cut on a line boundary keeps the line it lands on",
		in:       "one\ntwo\nthree\nfour\n",
		maxBytes: 11,
		want:     "three\nfour\n",
	}, {
		name:     "a non-positive bound keeps nothing",
		in:       "anything",
		maxBytes: 0,
		want:     "",
	}, {
		name:     "a negative bound keeps nothing rather than panicking",
		in:       "anything",
		maxBytes: -1,
		want:     "",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serviceutil.TailBytes(tt.in, tt.maxBytes)
			if got != tt.want {
				t.Fatalf("TailBytes(%q, %d) = %q, want %q", tt.in, tt.maxBytes, got, tt.want)
			}
			if tt.maxBytes > 0 && len(got) > tt.maxBytes {
				t.Fatalf("TailBytes returned %d bytes, over the %d-byte bound", len(got), tt.maxBytes)
			}
			if !utf8.ValidString(got) && utf8.ValidString(tt.in) {
				t.Fatalf("TailBytes(%q, %d) = %q, which is not valid UTF-8", tt.in, tt.maxBytes, got)
			}
		})
	}
}

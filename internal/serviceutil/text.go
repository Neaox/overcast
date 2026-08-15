package serviceutil

// text.go — bounding text that a service keeps a copy of.

import (
	"strings"
	"unicode/utf8"
)

// TailBytes returns at most maxBytes from the end of s.
//
// The end is what survives, which is the opposite of everything named
// truncate* in this codebase and is the whole reason this exists separately.
// It bounds captured output — a container's last words before it exited, an
// engine's account of why it would not start — and the diagnosis in output is
// always at the end of it. Keeping the head of a crash log retains the boot
// banner and discards the stack trace.
//
// The cut lands wherever maxBytes falls, so unless it happened to land on a
// line boundary the result is trimmed forward before it is returned: past the
// first newline in the window, since a first line beginning mid-word reads as
// corruption rather than as a bound; and, when the window holds no line
// boundary to trim to, past the partial rune at the front, which would
// otherwise render as U+FFFD. A window whose only newline is its last byte is
// left as it is — trimming past that one would return nothing, and a truncated
// line beats an empty pane.
func TailBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := len(s) - maxBytes
	if s[cut-1] == '\n' {
		return s[cut:] // the cut already fell on a line boundary
	}
	s = s[cut:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

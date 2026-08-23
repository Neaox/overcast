package containerlogs

import (
	"bufio"
	"io"
	"strings"
	"time"
)

const (
	// maxMessageBytes is CloudWatch Logs' per-event payload limit: 256 KiB less
	// the 26 bytes it charges for the event's own overhead. A message longer
	// than this is truncated rather than dropped — the start of a stack trace
	// is worth more than nothing.
	maxMessageBytes = 262144 - 26
	// maxTimestampBytes covers the "<RFC3339Nano> " prefix the daemon writes
	// when the logs endpoint is called with timestamps=true, with room to spare
	// over the longest form it emits.
	maxTimestampBytes = 64
	// maxLineBytes is what line assembly reads before it starts discarding: a
	// full-size message plus the timestamp in front of it.
	maxLineBytes = maxMessageBytes + maxTimestampBytes
)

// readBoundedLine reads one newline-terminated line, truncating anything past
// maxBytes instead of buffering it.
//
// The bound is what keeps a container that writes without ever printing a
// newline — a progress bar, a corrupt binary on stdout — from growing the
// follower's memory until the process dies. Discarded bytes are still consumed,
// so the line after an oversized one comes back intact rather than as its tail.
func readBoundedLine(r *bufio.Reader, maxBytes int) (string, error) {
	var out []byte
	truncated := false
	for {
		part, err := r.ReadSlice('\n')
		if len(part) > 0 && !truncated {
			remaining := maxBytes - len(out)
			if remaining > 0 {
				if len(part) > remaining {
					out = append(out, part[:remaining]...)
					truncated = true
				} else {
					out = append(out, part...)
				}
			} else {
				truncated = true
			}
		}
		if err == nil {
			return strings.TrimRight(string(out), "\r\n"), nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(out) > 0 {
			return strings.TrimRight(string(out), "\r\n"), nil
		}
		return "", err
	}
}

// parseTimestamp splits the RFC3339Nano prefix the daemon writes when the logs
// endpoint is called with timestamps=true from the message behind it. A line
// carrying no timestamp — every line after the first inside one Docker frame —
// comes back whole, with a zero time.
func parseTimestamp(line string) (time.Time, string) {
	// Docker's format is "2006-01-02T15:04:05.999999999Z message..." or
	// "2006-01-02T15:04:05.999999999+07:00 message...". The shortest stamp it
	// can write is 20 bytes, the longest 35, so a separator outside that range
	// rules one out before the parse is paid for.
	spaceIdx := strings.IndexByte(line, ' ')
	if spaceIdx < 20 || spaceIdx > 40 {
		return time.Time{}, line
	}
	t, err := time.Parse(time.RFC3339Nano, line[:spaceIdx])
	if err != nil {
		return time.Time{}, line
	}
	return t, line[spaceIdx+1:]
}

// truncateMessage cuts a message down to what CloudWatch will take in one event.
func truncateMessage(msg string) string {
	if len(msg) <= maxMessageBytes {
		return msg
	}
	return msg[:maxMessageBytes]
}

// eventTimestampMillis is when a line happened, in CloudWatch's units: the
// daemon's own timestamp where there is one, and the caller's clock only for a
// line that carried none.
func eventTimestampMillis(ts time.Time, fallback time.Time) int64 {
	if ts.IsZero() {
		return fallback.UnixMilli()
	}
	return ts.UnixMilli()
}

// admitLine turns one raw line into a Line, or reports that it should be
// dropped: a duplicate the cursor has already delivered, or an empty message
// CloudWatch would refuse.
func admitLine(raw string, adm *cursorAdmission, backfill bool) (Line, bool) {
	ts, msg := parseTimestamp(raw)
	if !adm.Admit(ts) {
		return Line{}, false
	}
	msg = truncateMessage(msg)
	if msg == "" {
		return Line{}, false
	}
	return Line{Time: ts, Message: msg, Backfill: backfill}, true
}

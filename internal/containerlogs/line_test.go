package containerlogs

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/docker"
)

// The frame-level behaviour of the demux reader itself — TTY passthrough,
// zero-length frames, truncated headers, stderr frames — is covered in
// internal/docker. What is tested here is what the follower builds on top of
// it: timestamped lines, reassembled across frames, split back apart.

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantTS  bool
		wantMsg string
	}{
		{
			name:    "standard UTC timestamp",
			input:   "2024-01-15T10:30:45.123456789Z hello world",
			wantTS:  true,
			wantMsg: "hello world",
		},
		{
			name:    "timestamp with timezone offset",
			input:   "2024-01-15T10:30:45.123456789+05:30 some log",
			wantTS:  true,
			wantMsg: "some log",
		},
		{
			name:    "no timestamp",
			input:   "just a plain log line",
			wantTS:  false,
			wantMsg: "just a plain log line",
		},
		{
			name:    "empty line",
			input:   "",
			wantTS:  false,
			wantMsg: "",
		},
		{
			name:    "short timestamp (no nanos)",
			input:   "2024-01-15T10:30:45Z message",
			wantTS:  true,
			wantMsg: "message",
		},
		{
			name:    "message with spaces",
			input:   "2024-01-15T10:30:45.000000000Z   leading spaces preserved",
			wantTS:  true,
			wantMsg: "  leading spaces preserved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, msg := parseTimestamp(tt.input)
			if tt.wantTS && ts.IsZero() {
				t.Errorf("expected non-zero timestamp, got zero")
			}
			if !tt.wantTS && !ts.IsZero() {
				t.Errorf("expected zero timestamp, got %v", ts)
			}
			if msg != tt.wantMsg {
				t.Errorf("message = %q, want %q", msg, tt.wantMsg)
			}
		})
	}

	// Verify the actual parsed time value for one case.
	ts, _ := parseTimestamp("2024-01-15T10:30:45.123456789Z hello")
	expected := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	if !ts.Equal(expected) {
		t.Errorf("parsed time = %v, want %v", ts, expected)
	}
}

func TestLogLines_timestampPerFrame(t *testing.T) {
	// Given: three log lines, each in its own frame, each stamped.
	var buf bytes.Buffer
	buf.Write(dockerFrame(1, "2024-01-15T10:30:45.123456789Z line one\n"))
	buf.Write(dockerFrame(1, "2024-01-15T10:30:46.123456789Z line two\n"))
	buf.Write(dockerFrame(1, "2024-01-15T10:30:47.123456789Z line three\n"))

	// When: the follower's line assembly reads them back.
	got := readAll(t, &buf)

	// Then: each carries its own timestamp and message.
	if len(got) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(got), got)
	}
	for i, line := range got {
		ts, msg := parseTimestamp(line)
		if ts.IsZero() {
			t.Errorf("line %d (%q): expected timestamp, got zero", i, line)
		}
		if msg == "" {
			t.Errorf("line %d (%q): expected message, got empty", i, line)
		}
	}
	want := []string{"line one", "line two", "line three"}
	for i, w := range want {
		if _, msg := parseTimestamp(got[i]); msg != w {
			t.Errorf("line %d = %q, want %q", i, msg, w)
		}
	}
}

func TestLogLines_lineSpansFrames(t *testing.T) {
	// Given: one line split across two frames — a partial write.
	var buf bytes.Buffer
	buf.Write(dockerFrame(1, "2024-01-15T10:30:45.123456789Z partial"))
	buf.Write(dockerFrame(1, " line\n"))

	// When: the follower reads it back.
	got := readAll(t, &buf)

	// Then: it is one line again, and only the opening timestamp is stripped.
	if len(got) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(got), got)
	}
	ts, msg := parseTimestamp(got[0])
	if ts.IsZero() {
		t.Errorf("expected timestamp, got zero")
	}
	if msg != "partial line" {
		t.Errorf("got %q, want %q", msg, "partial line")
	}
}

func TestLogLines_multipleLinesInOneFrame(t *testing.T) {
	// Given: one frame carrying two lines.
	var buf bytes.Buffer
	buf.Write(dockerFrame(1, "2024-01-15T10:30:45.123456789Z line1\nline2\n"))

	// When: the follower reads it back.
	got := readAll(t, &buf)

	// Then: the frame's stamp belongs to the first line only.
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(got), got)
	}
	ts1, msg1 := parseTimestamp(got[0])
	if ts1.IsZero() || msg1 != "line1" {
		t.Errorf("first line = (%v, %q), want a timestamp and line1", ts1, msg1)
	}
	ts2, msg2 := parseTimestamp(got[1])
	if !ts2.IsZero() || msg2 != "line2" {
		t.Errorf("second line = (%v, %q), want no timestamp and line2", ts2, msg2)
	}
}

func TestLogLines_largePayload(t *testing.T) {
	// Given: a 100 KB line, larger than any single read.
	payload := strings.Repeat("x", 100*1024)
	var buf bytes.Buffer
	buf.Write(dockerFrame(1, "2024-01-15T10:30:45.123456789Z "+payload+"\n"))

	// When: the follower reads it back.
	got := readAll(t, &buf)

	// Then: it is one line, whole.
	if len(got) != 1 {
		t.Fatalf("expected 1 line, got %d", len(got))
	}
	if _, msg := parseTimestamp(got[0]); msg != payload {
		t.Errorf("message length = %d, want %d", len(msg), len(payload))
	}
}

// A log line longer than Docker's 16 KiB chunk size arrives as several frames,
// each stamped with its own timestamp. Reassembling them naively splices those
// stamps through the middle of the message — a serialized error object comes
// back with a timestamp cutting a number in half. What reaches CloudWatch must
// be the line the function actually wrote.
func TestLogLines_chunkedLineHasNoEmbeddedTimestamp(t *testing.T) {
	// Given: one message split at Docker's chunk boundary, both frames stamped.
	const (
		head = "2026-08-10T02:27:36.830921293Z "
		cont = "2026-08-10T02:27:36.830921294Z "
	)
	payload := strings.Repeat("A", 16384) + strings.Repeat("B", 4000)

	var buf bytes.Buffer
	buf.Write(dockerFrame(1, head+payload[:16384]))
	buf.Write(dockerFrame(1, cont+payload[16384:]+"\n"))

	// When: the follower reads the line back.
	r := bufio.NewReaderSize(docker.NewLogDemuxReader(&buf), readBufferSize)
	line, err := readBoundedLine(r, maxLineBytes)
	if err != nil {
		t.Fatalf("readBoundedLine: %v", err)
	}
	ts, msg := parseTimestamp(line)

	// Then: the message is exactly what the function wrote, and the timestamp
	// is the one that opened it.
	if msg != payload {
		if idx := strings.Index(msg, "2026-08-10T"); idx >= 0 {
			t.Fatalf("message carries an embedded Docker timestamp at byte %d", idx)
		}
		t.Fatalf("message = %d bytes, want %d", len(msg), len(payload))
	}
	if want := time.Date(2026, 8, 10, 2, 27, 36, 830921293, time.UTC); !ts.Equal(want) {
		t.Errorf("timestamp = %v, want %v", ts, want)
	}
}

func TestReadBoundedLine_truncatesAndContinues(t *testing.T) {
	// Given: one oversized line followed by a normal line.
	r := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 20)+"\nnext\n"), 4)

	// When: lines are read through the bounded reader.
	first, err := readBoundedLine(r, 8)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	second, err := readBoundedLine(r, 8)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}

	// Then: the oversized line is truncated and the following line is intact.
	if first != strings.Repeat("x", 8) {
		t.Fatalf("first = %q, want 8 x's", first)
	}
	if second != "next" {
		t.Fatalf("second = %q, want next", second)
	}
}

// readAll runs the follower's line assembly over a framed payload.
func readAll(t *testing.T, framed *bytes.Buffer) []string {
	t.Helper()
	r := bufio.NewReaderSize(docker.NewLogDemuxReader(framed), readBufferSize)
	var got []string
	for {
		line, err := readBoundedLine(r, maxLineBytes)
		if err != nil {
			return got
		}
		got = append(got, line)
	}
}

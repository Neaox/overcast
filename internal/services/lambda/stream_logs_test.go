package lambda

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
	"time"
)

func makeDockerFrame(payload []byte) []byte {
	hdr := make([]byte, 8)
	hdr[0] = 1 // stdout
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	return append(hdr, payload...)
}

func makeDockerFrameStderr(payload []byte) []byte {
	hdr := make([]byte, 8)
	hdr[0] = 2 // stderr
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	return append(hdr, payload...)
}

func TestDockerLogStripper_basic(t *testing.T) {
	// Simulate a Docker log stream with multiplex frames containing
	// timestamped log lines (timestamps=true).

	var buf bytes.Buffer
	// Docker sends each log line as a separate frame: [8-byte header][timestamp+message]
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z line one\n")))
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:46.123456789Z line two\n")))
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:47.123456789Z line three\n")))

	stripped := &dockerLogStripper{r: &buf}
	scanner := bufio.NewScanner(stripped)

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(got), got)
	}

	// Each line should have a Docker timestamp prefix.
	for i, line := range got {
		ts, msg := parseDockerTimestamp(line)
		if ts.IsZero() {
			t.Errorf("line %d (%q): expected timestamp, got zero", i, line)
		}
		if msg == "" {
			t.Errorf("line %d (%q): expected message, got empty", i, line)
		}
	}

	if _, msg := parseDockerTimestamp(got[0]); msg != "line one" {
		t.Errorf("got %q, want 'line one'", msg)
	}
	if _, msg := parseDockerTimestamp(got[1]); msg != "line two" {
		t.Errorf("got %q, want 'line two'", msg)
	}
	if _, msg := parseDockerTimestamp(got[2]); msg != "line three" {
		t.Errorf("got %q, want 'line three'", msg)
	}
}

func TestDockerLogStripper_linesSpanFrames(t *testing.T) {
	// A log line split across two Docker frames (e.g., partial write).
	// After stripping, the scanner should reassemble the line.
	var buf bytes.Buffer
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z partial")))
	buf.Write(makeDockerFrame([]byte(" line\n")))

	stripped := &dockerLogStripper{r: &buf}
	scanner := bufio.NewScanner(stripped)

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(got), got)
	}

	ts, msg := parseDockerTimestamp(got[0])
	if ts.IsZero() {
		t.Errorf("expected timestamp, got zero")
	}
	// The message will include the second frame's raw data (no timestamp prefix
	// because parseDockerTimestamp only strips the first one).
	// After stripping the first timestamp, msg = "partial line"
	expected := "partial line"
	if msg != expected {
		t.Errorf("got %q, want %q", msg, expected)
	}
}

func TestDockerLogStripper_multipleLinesInOneFrame(t *testing.T) {
	// Docker sends one frame with multiple log lines.
	var buf bytes.Buffer
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z line1\nline2\n")))

	stripped := &dockerLogStripper{r: &buf}
	scanner := bufio.NewScanner(stripped)

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(got), got)
	}

	// First line has the Docker timestamp.
	ts1, msg1 := parseDockerTimestamp(got[0])
	if ts1.IsZero() {
		t.Errorf("first line: expected timestamp")
	}
	if msg1 != "line1" {
		t.Errorf("first line: got %q, want 'line1'", msg1)
	}

	// Second line has no timestamp prefix.
	ts2, msg2 := parseDockerTimestamp(got[1])
	if !ts2.IsZero() {
		t.Errorf("second line: expected no timestamp, got %v", ts2)
	}
	if msg2 != "line2" {
		t.Errorf("second line: got %q, want 'line2'", msg2)
	}
}

func TestDockerLogStripper_zeroLengthFrame(t *testing.T) {
	// Zero-length frames should be skipped silently.
	var buf bytes.Buffer
	buf.Write(makeDockerFrame([]byte{}))                                         // zero-length
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z hello\n"))) // real data

	stripped := &dockerLogStripper{r: &buf}
	scanner := bufio.NewScanner(stripped)

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(got), got)
	}
	_, msg := parseDockerTimestamp(got[0])
	if msg != "hello" {
		t.Errorf("got %q, want 'hello'", msg)
	}
}

func TestDockerLogStripper_emptyStream(t *testing.T) {
	// Empty stream: no frames. Scanner should return nothing.
	var buf bytes.Buffer
	stripped := &dockerLogStripper{r: &buf}
	scanner := bufio.NewScanner(stripped)

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 lines, got %d: %v", len(got), got)
	}
}

func TestDockerLogStripper_eofMidHeader(t *testing.T) {
	// EOF in the middle of a frame header: scanner should error.
	buf := bytes.NewBuffer([]byte{0x01, 0x00, 0x00, 0x00}) // incomplete header (4 of 8 bytes)
	stripped := &dockerLogStripper{r: buf}
	scanner := bufio.NewScanner(stripped)

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if scanner.Err() == nil {
		t.Error("expected scanner error for incomplete header, got nil")
	}
	// The error should be io.ErrUnexpectedEOF or io.EOF.
	err := scanner.Err()
	if err != io.ErrUnexpectedEOF && err != io.EOF {
		t.Errorf("expected io.ErrUnexpectedEOF or io.EOF, got %v", err)
	}
}

func TestDockerLogStripper_stderrFrames(t *testing.T) {
	// Frames from stderr should also be processed (stream type 2).
	var buf bytes.Buffer
	buf.Write(makeDockerFrameStderr([]byte("2024-01-15T10:30:45.123456789Z stderr line\n")))

	stripped := &dockerLogStripper{r: &buf}
	scanner := bufio.NewScanner(stripped)

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(got), got)
	}
	_, msg := parseDockerTimestamp(got[0])
	if msg != "stderr line" {
		t.Errorf("got %q, want 'stderr line'", msg)
	}
}

func TestParseDockerTimestamp(t *testing.T) {
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
			ts, msg := parseDockerTimestamp(tt.input)
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
	ts, _ := parseDockerTimestamp("2024-01-15T10:30:45.123456789Z hello")
	expected := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	if !ts.Equal(expected) {
		t.Errorf("parsed time = %v, want %v", ts, expected)
	}
}

func TestMakeDockerFrame(t *testing.T) {
	// Verify the frame format.
	payload := []byte("hello")
	frame := makeDockerFrame(payload)
	if len(frame) != 8+len(payload) {
		t.Fatalf("frame length = %d, want %d", len(frame), 8+len(payload))
	}
	if frame[0] != 1 {
		t.Errorf("stream type = %d, want 1 (stdout)", frame[0])
	}
	if frame[1] != 0 || frame[2] != 0 || frame[3] != 0 {
		t.Errorf("padding bytes not zero: %v", frame[1:4])
	}
	size := binary.BigEndian.Uint32(frame[4:8])
	if size != uint32(len(payload)) {
		t.Errorf("payload size = %d, want %d", size, len(payload))
	}
	if !bytes.Equal(frame[8:], payload) {
		t.Errorf("payload = %q, want %q", frame[8:], payload)
	}
}

func TestDockerLogStripper_largePayload(t *testing.T) {
	// Large payload spanning multiple reads.
	payload := strings.Repeat("x", 100*1024) // 100KB
	var buf bytes.Buffer
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z " + payload + "\n")))

	stripped := &dockerLogStripper{r: &buf}
	// Use a small buffer to force multiple reads within dockerLogStripper.
	scanner := bufio.NewScanner(stripped)
	scanner.Buffer(make([]byte, 16*1024), 128*1024) // 64KB initial, 128KB max

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 line, got %d", len(got))
	}
	_, msg := parseDockerTimestamp(got[0])
	if msg != payload {
		t.Errorf("message length = %d, want %d", len(msg), len(payload))
	}
}

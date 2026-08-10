package lambda

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
)

func makeDockerFrame(payload []byte) []byte {
	hdr := make([]byte, 8)
	hdr[0] = 1 // stdout
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	return append(hdr, payload...)
}

// The frame-level behaviour of the demux reader itself — TTY passthrough,
// zero-length frames, truncated headers, stderr frames — is covered in
// internal/docker. What is tested here is what Lambda builds on top of it:
// timestamped lines, reassembled across frames, parsed back into events.

func TestLambdaLogLines_timestampPerFrame(t *testing.T) {
	// Simulate a Docker log stream with multiplex frames containing
	// timestamped log lines (timestamps=true).

	var buf bytes.Buffer
	// Docker sends each log line as a separate frame: [8-byte header][timestamp+message]
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z line one\n")))
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:46.123456789Z line two\n")))
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:47.123456789Z line three\n")))

	stripped := docker.NewLogDemuxReader(&buf)
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

func TestLambdaLogLines_lineSpansFrames(t *testing.T) {
	// A log line split across two Docker frames (e.g., partial write).
	// After stripping, the scanner should reassemble the line.
	var buf bytes.Buffer
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z partial")))
	buf.Write(makeDockerFrame([]byte(" line\n")))

	stripped := docker.NewLogDemuxReader(&buf)
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
	// The continuation frame carries no timestamp of its own, so its bytes join
	// the line as they are; the opening timestamp is the only one stripped.
	expected := "partial line"
	if msg != expected {
		t.Errorf("got %q, want %q", msg, expected)
	}
}

func TestLambdaLogLines_multipleLinesInOneFrame(t *testing.T) {
	// Docker sends one frame with multiple log lines.
	var buf bytes.Buffer
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z line1\nline2\n")))

	stripped := docker.NewLogDemuxReader(&buf)
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

func TestLambdaLogLines_largePayload(t *testing.T) {
	// Large payload spanning multiple reads.
	payload := strings.Repeat("x", 100*1024) // 100KB
	var buf bytes.Buffer
	buf.Write(makeDockerFrame([]byte("2024-01-15T10:30:45.123456789Z " + payload + "\n")))

	stripped := docker.NewLogDemuxReader(&buf)
	// Use a small buffer to force multiple reads within the demux reader.
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

func TestLogCursor_equalTimestampReplay(t *testing.T) {
	// Given: two log lines with the exact same Docker timestamp were accepted.
	ts := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	var cursor logCursor
	live := cursor.NewAdmission(false)
	first := live.Admit(ts)
	second := live.Admit(ts)
	if !first || !second {
		t.Fatal("live equal-timestamp lines should both be accepted")
	}

	// When: Docker reconnect replays those same two lines plus a third with the
	// same timestamp.
	replay := cursor.NewAdmission(true)

	// Then: only the already accepted equal-timestamp lines are skipped.
	if replay.Admit(ts) {
		t.Fatal("first replayed equal-timestamp line was accepted, want skipped")
	}
	if replay.Admit(ts) {
		t.Fatal("second replayed equal-timestamp line was accepted, want skipped")
	}
	if !replay.Admit(ts) {
		t.Fatal("third equal-timestamp line was skipped, want accepted")
	}
}

func TestLogCursor_sinceReplaysEqualTimestamp(t *testing.T) {
	// Given: a cursor has accepted a timestamped log line.
	ts := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	var cursor logCursor
	if !cursor.NewAdmission(false).Admit(ts) {
		t.Fatal("line was not accepted")
	}

	// When: the Docker since cursor is computed for reconnect/reconcile.
	since := cursor.Since()

	// Then: it asks Docker to replay from just before the high watermark so
	// equal-timestamp lines can be disambiguated by count.
	if want := ts.Add(-time.Nanosecond); !since.Equal(want) {
		t.Fatalf("Since = %v, want %v", since, want)
	}
}

func TestReadBoundedLogLine_truncatesAndContinues(t *testing.T) {
	// Given: one oversized line followed by a normal line.
	r := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 20)+"\nnext\n"), 4)

	// When: lines are read through the bounded reader.
	first, err := readBoundedLogLine(r, 8)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	second, err := readBoundedLogLine(r, 8)
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

// A log line longer than Docker's 16 KiB chunk size arrives as several frames,
// each stamped with its own timestamp. Reassembling them naively splices those
// stamps through the middle of the message — a serialized error object comes
// back with a timestamp cutting a number in half. What reaches CloudWatch must
// be the line the function actually wrote.
func TestLambdaLogLines_chunkedLineHasNoEmbeddedTimestamp(t *testing.T) {
	// Given: one message split at Docker's chunk boundary, both frames stamped.
	const (
		head = "2026-08-10T02:27:36.830921293Z "
		cont = "2026-08-10T02:27:36.830921294Z "
	)
	payload := strings.Repeat("A", 16384) + strings.Repeat("B", 4000)

	var buf bytes.Buffer
	buf.Write(makeDockerFrame([]byte(head + payload[:16384])))
	buf.Write(makeDockerFrame([]byte(cont + payload[16384:] + "\n")))

	// When: the streaming path reads the line back.
	r := bufio.NewReaderSize(docker.NewLogDemuxReader(&buf), 64*1024)
	line, err := readBoundedLogLine(r, maxDockerLogLineBytes)
	if err != nil {
		t.Fatalf("readBoundedLogLine: %v", err)
	}
	ts, msg := parseDockerTimestamp(line)

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

type fakeLogWriter struct {
	mu          sync.Mutex
	failWrites  int
	writes      [][]events.LogEntry
	ensureCalls int
}

func (w *fakeLogWriter) EnsureLogGroup(context.Context, string) error { return nil }

func (w *fakeLogWriter) EnsureLogStream(context.Context, string, string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ensureCalls++
	return nil
}

func (w *fakeLogWriter) WriteLogEvents(_ context.Context, _, _ string, entries []events.LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failWrites > 0 {
		w.failWrites--
		return errors.New("transient write failure")
	}
	clone := append([]events.LogEntry(nil), entries...)
	w.writes = append(w.writes, clone)
	return nil
}

func TestWriteEventsWithRetry_transientFailure(t *testing.T) {
	// Given: a log writer that fails once, then succeeds.
	writer := &fakeLogWriter{failWrites: 1}
	ci := &containerInstance{
		id:           "container123456",
		functionARN:  "arn:aws:lambda:us-east-1:000000000000:function:fn",
		logGroupName: "/aws/lambda/fn",
		logStream:    "stream",
		logWriter:    writer,
		logger:       zap.NewNop(),
		clk:          clock.New(),
	}

	// When: events are written durably.
	ok := ci.writeEventsWithRetry(context.Background(), []events.LogEntry{{Timestamp: 1, Message: "hello"}})

	// Then: the transient error is retried after ensuring the stream.
	if !ok {
		t.Fatal("writeEventsWithRetry returned false")
	}
	if len(writer.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writer.writes))
	}
	if writer.ensureCalls == 0 {
		t.Fatal("EnsureLogStream was not called on retry")
	}
}

func TestStreamOnce_equalTimestampLinesAndEmitTimestamps(t *testing.T) {
	// Given: Docker returns three user log lines with identical timestamps.
	emit := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	var payload bytes.Buffer
	for _, msg := range []string{"one", "two", "three"} {
		payload.Write(makeDockerFrame([]byte(emit.Format(time.RFC3339Nano) + " " + msg + "\n")))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/logs") {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload.Bytes())
	}))
	defer server.Close()
	writer := &fakeLogWriter{}
	ci := newStreamOnceTestInstance(server, writer)

	// When: streamOnce processes the Docker log response.
	ci.streamOnce(context.Background(), time.Time{})

	// Then: no equal-timestamp line is dropped and event timestamps use Docker's
	// emit timestamp, not scan time.
	got := writer.allMessages()
	if strings.Join(got, ",") != "one,two,three" {
		t.Fatalf("messages = %v, want [one two three]", got)
	}
	for _, batch := range writer.writes {
		for _, entry := range batch {
			if entry.Timestamp != emit.UnixMilli() {
				t.Fatalf("entry timestamp = %d, want %d", entry.Timestamp, emit.UnixMilli())
			}
		}
	}
}

func TestStreamOnce_emptyMessageDropped(t *testing.T) {
	// Given: Docker returns an empty log message and then a non-empty one.
	emit := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var payload bytes.Buffer
	payload.Write(makeDockerFrame([]byte(emit.Format(time.RFC3339Nano) + " \n")))
	payload.Write(makeDockerFrame([]byte(emit.Add(time.Nanosecond).Format(time.RFC3339Nano) + " kept\n")))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload.Bytes())
	}))
	defer server.Close()
	writer := &fakeLogWriter{}
	ci := newStreamOnceTestInstance(server, writer)

	// When: streamOnce processes the Docker log response.
	ci.streamOnce(context.Background(), time.Time{})

	// Then: the empty CloudWatch event is dropped but following logs are delivered.
	got := writer.allMessages()
	if len(got) != 1 || got[0] != "kept" {
		t.Fatalf("messages = %v, want [kept]", got)
	}
}

func newStreamOnceTestInstance(server *httptest.Server, writer *fakeLogWriter) *containerInstance {
	dc := docker.NewClient("tcp://"+server.Listener.Addr().String(), zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	return &containerInstance{
		id:           "container123456",
		functionARN:  "arn:aws:lambda:us-east-1:000000000000:function:fn",
		logGroupName: "/aws/lambda/fn",
		logStream:    "stream",
		docker:       dc,
		logWriter:    writer,
		logger:       zap.NewNop(),
		clk:          clock.New(),
		logCtx:       ctx,
		logCancel:    cancel,
	}
}

func (w *fakeLogWriter) allMessages() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var messages []string
	for _, batch := range w.writes {
		for _, entry := range batch {
			messages = append(messages, entry.Message)
		}
	}
	return messages
}

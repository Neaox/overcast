package docker

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// frame builds one Docker multiplex frame around payload.
func frame(streamType byte, payload string) []byte {
	out := make([]byte, frameHeaderLen, frameHeaderLen+len(payload))
	out[0] = streamType
	binary.BigEndian.PutUint32(out[4:8], uint32(len(payload)))
	return append(out, payload...)
}

func stdout(payload string) []byte { return frame(1, payload) }
func stderr(payload string) []byte { return frame(2, payload) }

// readAllDemuxed runs the streaming reader over raw and returns what it emitted.
func readAllDemuxed(t *testing.T, raw []byte) string {
	t.Helper()
	got, err := io.ReadAll(NewDemuxReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(got)
}

func TestDemux_framedStream(t *testing.T) {
	raw := bytes.Join([][]byte{
		stdout("line one\n"),
		stderr("line two\n"),
		stdout("line three\n"),
	}, nil)
	want := "line one\nline two\nline three\n"

	if got := string(DemuxStream(raw)); got != want {
		t.Errorf("DemuxStream = %q, want %q", got, want)
	}
	if got := readAllDemuxed(t, raw); got != want {
		t.Errorf("DemuxReader = %q, want %q", got, want)
	}
}

// A TTY-allocated container or exec writes no frame headers at all, so its
// first 8 bytes are output. Eating them is the failure this guards.
func TestDemux_unframedStreamPassesThrough(t *testing.T) {
	raw := []byte("mysqld: ready for connections\nsecond line\n")

	if got := string(DemuxStream(raw)); got != string(raw) {
		t.Errorf("DemuxStream = %q, want it unchanged (%q)", got, raw)
	}
	if got := readAllDemuxed(t, raw); got != string(raw) {
		t.Errorf("DemuxReader = %q, want it unchanged (%q)", got, raw)
	}
}

// Output that begins with a low byte is still not a frame unless the three
// padding bytes are zero — that pairing is what makes the test worth anything.
func TestDemux_lowFirstByteWithoutPaddingIsNotAFrame(t *testing.T) {
	raw := []byte{0x01, 'a', 'b', 'c', 0x00, 0x00, 0x00, 0x05, 'h', 'e', 'l', 'l', 'o'}

	if got := string(DemuxStream(raw)); got != string(raw) {
		t.Errorf("DemuxStream = %q, want it unchanged", got)
	}
	if got := readAllDemuxed(t, raw); got != string(raw) {
		t.Errorf("DemuxReader = %q, want it unchanged", got)
	}
}

// A stream type above stderr is not one Docker emits.
func TestDemux_unknownStreamTypeIsNotAFrame(t *testing.T) {
	raw := append(frame(7, "payload"), "trailing"...)

	if got := string(DemuxStream(raw)); got != string(raw) {
		t.Errorf("DemuxStream = %q, want it unchanged", got)
	}
}

// ContainerLogs bounds its read, so the last frame of a long log routinely
// arrives cut short. Those bytes are the newest lines — the ones being read for.
func TestDemux_truncatedFinalFrameKeepsWhatArrived(t *testing.T) {
	raw := append(stdout("complete\n"), stdout("cut off here, honest")[:frameHeaderLen+7]...)
	want := "complete\ncut off"

	if got := string(DemuxStream(raw)); got != want {
		t.Errorf("DemuxStream = %q, want %q", got, want)
	}
	if got := readAllDemuxed(t, raw); got != want {
		t.Errorf("DemuxReader = %q, want %q", got, want)
	}
}

// A frame boundary that stops validating part-way through is a stream that
// desynchronised, and the bytes after it are still the container's own. They
// are appended as they are rather than walked as though they were framed —
// re-parsing them reads a payload length out of the output itself and chops
// the rest of the log at whatever offset that happens to spell.
func TestDemuxStream_desyncedRemainderIsKeptVerbatim(t *testing.T) {
	const tail = "no header in front of this\n"
	raw := append(stdout("framed and fine\n"), tail...)
	want := "framed and fine\n" + tail

	if got := string(DemuxStream(raw)); got != want {
		t.Errorf("DemuxStream = %q, want %q", got, want)
	}
}

// A remainder too short to hold a header is a header the read limit cut in
// half: binary junk rather than output, so it is dropped and not printed.
func TestDemuxStream_partialTrailingHeaderIsDropped(t *testing.T) {
	raw := append(stdout("complete\n"), 0x01, 0x00, 0x00)
	want := "complete\n"

	if got := string(DemuxStream(raw)); got != want {
		t.Errorf("DemuxStream = %q, want %q", got, want)
	}
}

// The streaming reader reports the same case as the error it is, rather than
// handing the caller half a header as though it were a log line.
func TestDemuxReader_truncatedHeaderErrors(t *testing.T) {
	raw := append(stdout("complete\n"), 0x01, 0x00, 0x00)

	r := NewDemuxReader(bytes.NewReader(raw))
	got, err := io.ReadAll(r)
	if err != io.ErrUnexpectedEOF {
		t.Errorf("error = %v, want io.ErrUnexpectedEOF", err)
	}
	if string(got) != "complete\n" {
		t.Errorf("read %q before the error, want %q", got, "complete\n")
	}
}

// Docker emits zero-length frames; they carry nothing and must not end the stream.
func TestDemux_zeroLengthFramesAreSkipped(t *testing.T) {
	raw := bytes.Join([][]byte{stdout(""), stdout("hello\n"), stdout("")}, nil)
	want := "hello\n"

	if got := string(DemuxStream(raw)); got != want {
		t.Errorf("DemuxStream = %q, want %q", got, want)
	}
	if got := readAllDemuxed(t, raw); got != want {
		t.Errorf("DemuxReader = %q, want %q", got, want)
	}
}

func TestDemux_emptyInput(t *testing.T) {
	if got := DemuxStream(nil); len(got) != 0 {
		t.Errorf("DemuxStream = %q, want empty", got)
	}
	if got := readAllDemuxed(t, nil); got != "" {
		t.Errorf("DemuxReader = %q, want empty", got)
	}
}

// One log line is not one frame: a JSON record from a structured logger spans
// several, and the reader exists so a bufio.Scanner can put it back together.
func TestDemuxReader_lineSpansFrames(t *testing.T) {
	raw := bytes.Join([][]byte{
		stdout(`{"level":"INFO",`),
		stdout(`"message":"split across frames"}`),
		stdout("\nsecond\n"),
	}, nil)

	scanner := bufio.NewScanner(NewDemuxReader(bytes.NewReader(raw)))
	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	want := []string{`{"level":"INFO","message":"split across frames"}`, "second"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A payload larger than one Read must come out whole, in order.
func TestDemuxReader_payloadSpansManyReads(t *testing.T) {
	payload := strings.Repeat("x", 100*1024)
	raw := stdout(payload + "\n")

	// One byte per Read is the extreme of the same case a 4 KiB buffer hits.
	got, err := io.ReadAll(iotest.OneByteReader(NewDemuxReader(bytes.NewReader(raw))))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != payload+"\n" {
		t.Errorf("read %d bytes, want %d", len(got), len(payload)+1)
	}
}

// The two helpers are one behaviour with two shapes; they must not drift.
func TestDemux_readerAndSliceAgree(t *testing.T) {
	cases := map[string][]byte{
		"framed":      bytes.Join([][]byte{stdout("a\n"), stderr("b\n")}, nil),
		"unframed":    []byte("plain tty output\n"),
		"zero length": bytes.Join([][]byte{stdout(""), stdout("c\n")}, nil),
		"truncated":   append(stdout("d\n"), stdout("efgh")[:frameHeaderLen+2]...),
		"empty":       nil,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if got, want := readAllDemuxed(t, raw), string(DemuxStream(raw)); got != want {
				t.Errorf("DemuxReader = %q, DemuxStream = %q", got, want)
			}
		})
	}
}

// The reader is on the path of every line of every Lambda invocation, so it
// serves reads from the caller's buffer and allocates nothing per Read.
func TestDemuxReader_doesNotAllocatePerRead(t *testing.T) {
	raw := bytes.Repeat(stdout("a log line of roughly typical length\n"), 64)
	buf := make([]byte, 4096)
	// Source and wrapper are built once and reset per run, so what is counted
	// is the reads and nothing else.
	src := bytes.NewReader(raw)
	r := NewDemuxReader(src)

	allocs := testing.AllocsPerRun(100, func() {
		src.Reset(raw)
		*r = DemuxReader{r: src}
		for {
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
	})
	if allocs != 0 {
		t.Errorf("allocations per run = %v, want 0", allocs)
	}
}

// Docker splits any log message longer than 16 KiB into separate frames, and
// with timestamps=true it stamps *every* frame, not every message. The
// continuation stamps land in the middle of the caller's line, which is how a
// serialized error object ends up with an RFC3339Nano timestamp spliced through
// the middle of a number. NewLogDemuxReader is the reader that drops them.
func TestLogDemuxReader_dropsContinuationTimestamps(t *testing.T) {
	const (
		head = "2026-08-10T02:27:36.830921293Z "
		cont = "2026-08-10T02:27:36.830921294Z "
	)
	first := strings.Repeat("A", 16384)
	rest := strings.Repeat("B", 4000)

	raw := bytes.Join([][]byte{
		stdout(head + first),
		stdout(cont + rest + "\n"),
	}, nil)

	got, err := io.ReadAll(NewLogDemuxReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := head + first + rest + "\n"
	if string(got) != want {
		if idx := strings.Index(string(got[len(head):]), "2026-08-10T"); idx >= 0 {
			t.Fatalf("continuation timestamp survived at byte %d of the message", idx)
		}
		t.Fatalf("output = %d bytes, want %d", len(got), len(want))
	}
}

// A timestamp at the head of a frame that begins a *new* line is the message's
// own, and dropping it would cost the log event its time.
func TestLogDemuxReader_keepsPerMessageTimestamps(t *testing.T) {
	raw := bytes.Join([][]byte{
		stdout("2026-08-10T02:27:36.000000001Z first\n"),
		stderr("2026-08-10T02:27:36.000000002Z second\n"),
		stdout("2026-08-10T02:27:36.000000003Z third\n"),
	}, nil)
	want := "2026-08-10T02:27:36.000000001Z first\n" +
		"2026-08-10T02:27:36.000000002Z second\n" +
		"2026-08-10T02:27:36.000000003Z third\n"

	if got := readAllLogDemuxed(t, raw); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// A continuation frame whose payload merely looks textual must survive intact:
// only a well-formed RFC3339Nano stamp followed by a space is Docker's, and
// anything else is the container's own bytes.
func TestLogDemuxReader_keepsNonTimestampContinuation(t *testing.T) {
	raw := bytes.Join([][]byte{
		stdout("2026-08-10T02:27:36.000000001Z start-of-line"),
		stdout("not-a-timestamp rest-of-line\n"),
	}, nil)
	want := "2026-08-10T02:27:36.000000001Z start-of-linenot-a-timestamp rest-of-line\n"

	if got := readAllLogDemuxed(t, raw); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// The plain reader is used where Docker was not asked for timestamps (ECS), and
// must keep passing every byte through untouched.
func TestDemuxReader_doesNotStripTimestamps(t *testing.T) {
	raw := bytes.Join([][]byte{
		stdout("2026-08-10T02:27:36.000000001Z head"),
		stdout("2026-08-10T02:27:36.000000002Z tail\n"),
	}, nil)
	want := "2026-08-10T02:27:36.000000001Z head2026-08-10T02:27:36.000000002Z tail\n"

	if got := readAllDemuxed(t, raw); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// A stream that dies part-way through a continuation frame's timestamp still
// holds the newest bytes of the log, which is the part someone reading it wants
// most. They come out first; the error follows on the next read.
func TestLogDemuxReader_failedContinuationDrainsBeforeError(t *testing.T) {
	errBroken := errors.New("connection reset")
	raw := bytes.Join([][]byte{
		stdout("2026-08-10T02:27:36.000000001Z head"),
		stdout("tail-of-line\n"),
	}, nil)

	r := NewLogDemuxReader(&failAfterReader{r: bytes.NewReader(raw), after: len(raw) - 6, err: errBroken})

	got, err := io.ReadAll(r)
	if !errors.Is(err, errBroken) {
		t.Fatalf("error = %v, want %v", err, errBroken)
	}
	if want := "2026-08-10T02:27:36.000000001Z headtail-of"; string(got) != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// failAfterReader serves r until after bytes have been read, then fails.
type failAfterReader struct {
	r     io.Reader
	after int
	err   error
	n     int
}

func (f *failAfterReader) Read(p []byte) (int, error) {
	if f.n >= f.after {
		return 0, f.err
	}
	if len(p) > f.after-f.n {
		p = p[:f.after-f.n]
	}
	n, err := f.r.Read(p)
	f.n += n
	return n, err
}

func readAllLogDemuxed(t *testing.T, raw []byte) string {
	t.Helper()
	got, err := io.ReadAll(NewLogDemuxReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(got)
}

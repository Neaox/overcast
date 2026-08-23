package containerlogs

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/events"
)

// ─── a container's log endpoint, without a daemon ──────────────────────────

// dockerFrame wraps a payload the way a container without a TTY reaches the
// logs endpoint: an 8-byte header naming the stream and the payload length.
func dockerFrame(stream byte, payload string) []byte {
	hdr := make([]byte, 8)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	return append(hdr, payload...)
}

// stdoutLine is one timestamped line as the daemon frames it on stdout.
func stdoutLine(ts time.Time, msg string) []byte {
	return dockerFrame(1, ts.Format(time.RFC3339Nano)+" "+msg+"\n")
}

// fakeStream is one connection to the logs endpoint: a fixed payload, then
// either end-of-stream or an open connection that stays open until the
// follower's context is cancelled — which is what the daemon client does when
// it closes the response body underneath a reader.
type fakeStream struct {
	data   []byte
	off    int
	hold   bool
	ctx    context.Context
	closed chan struct{}
	once   sync.Once
}

func (s *fakeStream) Read(p []byte) (int, error) {
	if s.off < len(s.data) {
		n := copy(p, s.data[s.off:])
		s.off += n
		return n, nil
	}
	if !s.hold {
		return 0, io.EOF
	}
	select {
	case <-s.ctx.Done():
		return 0, errors.New("connection closed")
	case <-s.closed:
		return 0, errors.New("connection closed")
	}
}

func (s *fakeStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// streamScript is one scripted connection: what it serves, and whether it stays
// open afterwards instead of ending.
type streamScript struct {
	payload []byte
	hold    bool
}

// fakeStreamer serves scripted connections in order and records the `since`
// each one was opened with, which is how a test sees what the cursor asked for.
// The last script repeats once the list runs out, so a follower that keeps
// reconnecting does not run off the end.
type fakeStreamer struct {
	mu       sync.Mutex
	scripts  []streamScript
	opened   int
	since    []time.Time
	backfill []byte
	openErr  error
}

func (f *fakeStreamer) ContainerLogsStream(ctx context.Context, _ string, since time.Time) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.since = append(f.since, since)
	if f.openErr != nil {
		return nil, f.openErr
	}
	idx := f.opened
	f.opened++
	if idx >= len(f.scripts) {
		idx = len(f.scripts) - 1
	}
	script := f.scripts[idx]
	return &fakeStream{data: script.payload, hold: script.hold, ctx: ctx, closed: make(chan struct{})}, nil
}

func (f *fakeStreamer) ContainerLogsSince(_ context.Context, _ string, since time.Time) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.since = append(f.since, since)
	return io.NopCloser(newBytesReader(f.backfill)), nil
}

func (f *fakeStreamer) opens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opened
}

func (f *fakeStreamer) sinceValues() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.since...)
}

// newBytesReader avoids bytes.NewReader's ReadAt/Seek surface so a test cannot
// accidentally depend on more than an io.Reader.
func newBytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct {
	b   []byte
	off int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

// ─── sinks ─────────────────────────────────────────────────────────────────

// recordSink records what a follower hands it, and can hold the follower up
// inside Line so a test can arrange a queue behind it.
type recordSink struct {
	mu       sync.Mutex
	lines    []Line
	flushes  []int
	pending  int
	gate     chan struct{}
	gateOnce sync.Once
	reject   func(Line) bool
}

func (s *recordSink) Line(line Line) bool {
	if s.gate != nil {
		s.gateOnce.Do(func() { <-s.gate })
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	if s.reject != nil && s.reject(line) {
		return false
	}
	s.pending++
	return true
}

func (s *recordSink) Flush() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.pending
	s.pending = 0
	if n > 0 {
		s.flushes = append(s.flushes, n)
	}
	return n
}

func (s *recordSink) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.lines))
	for _, l := range s.lines {
		out = append(out, l.Message)
	}
	return out
}

func (s *recordSink) snapshot() ([]Line, []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Line(nil), s.lines...), append([]int(nil), s.flushes...)
}

// waitFor polls cond until it holds or the test's patience runs out. It waits
// on a goroutine making progress, never on time passing — every deadline in the
// follower is on the injected clock.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ─── CloudWatch Logs, without a store ──────────────────────────────────────

type fakeLogWriter struct {
	mu          sync.Mutex
	failWrites  int
	writes      [][]events.LogEntry
	ensureCalls int
	contexts    []context.Context
}

func (w *fakeLogWriter) EnsureLogGroup(context.Context, string) error { return nil }

func (w *fakeLogWriter) EnsureLogStream(context.Context, string, string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ensureCalls++
	return nil
}

func (w *fakeLogWriter) WriteLogEvents(ctx context.Context, _, _ string, entries []events.LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failWrites > 0 {
		w.failWrites--
		return errors.New("transient write failure")
	}
	w.contexts = append(w.contexts, ctx)
	w.writes = append(w.writes, append([]events.LogEntry(nil), entries...))
	return nil
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

func (w *fakeLogWriter) batchSizes() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]int, 0, len(w.writes))
	for _, batch := range w.writes {
		out = append(out, len(batch))
	}
	return out
}

package ecs

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

func TestAwslogsTargetFor(t *testing.T) {
	tests := []struct {
		name       string
		cd         ContainerDefinition
		wantOK     bool
		wantGroup  string
		wantStream string
	}{
		{
			name: "group and prefix",
			cd: ContainerDefinition{
				Name: "web",
				LogConfiguration: &LogConfiguration{
					LogDriver: "awslogs",
					Options: map[string]string{
						"awslogs-group":         "/ecs/app",
						"awslogs-stream-prefix": "ecs",
					},
				},
			},
			wantOK:     true,
			wantGroup:  "/ecs/app",
			wantStream: "ecs/web/task-1",
		},
		{
			name: "group without prefix",
			cd: ContainerDefinition{
				Name: "web",
				LogConfiguration: &LogConfiguration{
					LogDriver: "awslogs",
					Options:   map[string]string{"awslogs-group": "/ecs/app"},
				},
			},
			wantOK:     true,
			wantGroup:  "/ecs/app",
			wantStream: "web/task-1",
		},
		{
			name:   "no log configuration",
			cd:     ContainerDefinition{Name: "web"},
			wantOK: false,
		},
		{
			name: "another driver",
			cd: ContainerDefinition{
				Name:             "web",
				LogConfiguration: &LogConfiguration{LogDriver: "json-file"},
			},
			wantOK: false,
		},
		{
			name: "awslogs without a group",
			cd: ContainerDefinition{
				Name:             "web",
				LogConfiguration: &LogConfiguration{LogDriver: "awslogs", Options: map[string]string{}},
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := awslogsTargetFor(tt.cd, "task-1")
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.group != tt.wantGroup || got.stream != tt.wantStream {
				t.Fatalf("target = %+v, want group %q stream %q", got, tt.wantGroup, tt.wantStream)
			}
		})
	}
}

// A task that crash-loops explains itself only in CloudWatch, and the daemon
// connection carrying its output is the thing most likely to break while it is
// under the load that made it crash. Before internal/containerlogs, a break
// ended the pump: everything the task printed from that moment on was lost with
// no trace of the loss anywhere. Now the follower comes back — without
// re-delivering the lines the daemon replays when it resumes by timestamp.
//
// Forcing that break against a real daemon is not something a test can do
// reliably, so this drives the follower ECS builds through a scripted log
// endpoint instead; internal/containerlogs covers the mechanism, and what this
// pins is that ECS wires it up.
func TestAwslogsFollower_reconnectsWithoutLosingOrRepeatingLines(t *testing.T) {
	// Given: a container whose log stream ends after two lines, and a reconnect
	// that replays the last of them before serving a third.
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	var first, second bytes.Buffer
	first.Write(dockerLogLine(base, "starting"))
	first.Write(dockerLogLine(base.Add(time.Millisecond), "listening on 8080"))
	second.Write(dockerLogLine(base.Add(time.Millisecond), "listening on 8080"))
	second.Write(dockerLogLine(base.Add(2*time.Millisecond), "panic: out of memory"))

	streamer := &scriptedLogStreamer{payloads: [][]byte{first.Bytes(), second.Bytes()}}
	writer := &recordingLogWriter{}
	mock := clock.NewMock()
	h := &Handler{
		clk:       mock,
		log:       serviceutil.NewServiceLogger(zap.NewNop(), "ecs"),
		logWriter: writer,
	}
	target := awslogsTarget{group: "/ecs/app", stream: "ecs/web/task-1"}

	// When: the follower runs across the break.
	ctx, cancel := context.WithCancel(context.Background())
	follower := h.newAwslogsFollower(ctx, streamer, "container-1", "us-east-1", target)
	done := make(chan struct{})
	go func() {
		defer close(done)
		follower.Follow(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for len(writer.messages()) < 3 && time.Now().Before(deadline) {
		mock.Add(2 * time.Second)
	}
	cancel()
	<-done

	// Then: CloudWatch holds every line once, in order, under the task's
	// stream — and the timestamps are the container's, not the reader's.
	if got := strings.Join(writer.messages(), "|"); got != "starting|listening on 8080|panic: out of memory" {
		t.Fatalf("messages = %q, want the three lines exactly once each", got)
	}
	group, stream := writer.destination()
	if group != target.group || stream != target.stream {
		t.Fatalf("wrote to %q/%q, want %q/%q", group, stream, target.group, target.stream)
	}
	if got := writer.timestamps()[0]; got != base.UnixMilli() {
		t.Fatalf("first event timestamp = %d, want the container's %d", got, base.UnixMilli())
	}
}

// Batching is what makes the difference between one CloudWatch write per line
// and one per burst; ECS wrote a line at a time before this. A task logging a
// startup banner is a burst, and the write it costs should be one.
func TestAwslogsFollower_writesABurstAsOneBatch(t *testing.T) {
	// Given: a burst of thirty lines.
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	var payload bytes.Buffer
	for i := 0; i < 30; i++ {
		payload.Write(dockerLogLine(base.Add(time.Duration(i)*time.Millisecond), "banner"))
	}
	streamer := &scriptedLogStreamer{payloads: [][]byte{payload.Bytes()}, holdLast: true}
	writer := &recordingLogWriter{}
	mock := clock.NewMock()
	h := &Handler{
		clk:       mock,
		log:       serviceutil.NewServiceLogger(zap.NewNop(), "ecs"),
		logWriter: writer,
	}

	// When: the follower reads them, with no time passing at all — a full batch
	// does not wait on the clock.
	ctx, cancel := context.WithCancel(context.Background())
	follower := h.newAwslogsFollower(ctx, streamer, "container-1", "us-east-1",
		awslogsTarget{group: "/ecs/app", stream: "ecs/web/task-1"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		follower.Follow(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(writer.messages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// Then: the first twenty-five went out in one call, not twenty-five.
	if got := writer.batchSizes(); len(got) == 0 || got[0] != 25 {
		t.Fatalf("batch sizes = %v, want the first to hold 25 lines", got)
	}

	// And: cancelling delivers the rest rather than dropping them.
	cancel()
	<-done
	if got := len(writer.messages()); got != 30 {
		t.Fatalf("delivered %d lines, want all 30", got)
	}
}

// Stopping a container cancels its follower, which closes the daemon connection
// under a read that may have been mid-line. The daemon's persisted copy is
// complete by then, so one pass over it recovers what that cost — and nothing
// else, because the follower's cursor knows what was already written.
func TestAwslogsFollower_reconcileRecoversWhatTheCancelledFollowMissed(t *testing.T) {
	// Given: a follow that delivered one line, and a log file holding that one
	// and the line the container printed on its way out.
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	var live, file bytes.Buffer
	live.Write(dockerLogLine(base, "starting"))
	file.Write(dockerLogLine(base, "starting"))
	file.Write(dockerLogLine(base.Add(time.Millisecond), "panic: out of memory"))

	streamer := &scriptedLogStreamer{
		payloads: [][]byte{live.Bytes()},
		holdLast: true,
		backfill: file.Bytes(),
	}
	writer := &recordingLogWriter{}
	mock := clock.NewMock()
	h := &Handler{
		clk:       mock,
		log:       serviceutil.NewServiceLogger(zap.NewNop(), "ecs"),
		logWriter: writer,
	}

	ctx, cancel := context.WithCancel(context.Background())
	follower := h.newAwslogsFollower(ctx, streamer, "container-1", "us-east-1",
		awslogsTarget{group: "/ecs/app", stream: "ecs/web/task-1"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		follower.Follow(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(writer.messages()) == 0 && time.Now().Before(deadline) {
		mock.Add(10 * time.Millisecond)
	}

	// When: the container stops and the follower is cancelled, then reconciles.
	cancel()
	<-done
	follower.Reconcile(context.Background())

	// Then: the last line is recovered and the first is not written twice.
	if got := strings.Join(writer.messages(), "|"); got != "starting|panic: out of memory" {
		t.Fatalf("messages = %q, want starting|panic: out of memory", got)
	}
}

// A container that has stopped has nothing left to follow, and a follower that
// is not stopped re-opens its log stream against the daemon for as long as the
// emulator lives.
func TestStopLogStreaming_stopsTheFollower(t *testing.T) {
	h := &Handler{}
	stopped := make(chan struct{})
	h.registerLogPump("container-1", func() { close(stopped) })

	h.stopLogStreaming("container-1")

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stopLogStreaming did not cancel the follower")
	}
	h.logPumpsMu.Lock()
	defer h.logPumpsMu.Unlock()
	if len(h.logPumps) != 0 {
		t.Fatalf("logPumps = %v after stopping, want empty", h.logPumps)
	}
}

func TestStopLogStreaming_unknownContainerIsHarmless(t *testing.T) {
	h := &Handler{}
	h.stopLogStreaming("never-had-one")
}

// ─── test doubles ──────────────────────────────────────────────────────────

// dockerLogLine frames one timestamped stdout line the way the daemon does.
func dockerLogLine(ts time.Time, msg string) []byte {
	payload := ts.Format(time.RFC3339Nano) + " " + msg + "\n"
	hdr := make([]byte, 8)
	hdr[0] = 1
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	return append(hdr, payload...)
}

// scriptedLogStreamer answers the daemon's log endpoints from a script, serving
// each payload to one connection. The last payload repeats once the script runs
// out, so a follower that keeps reconnecting does not run off the end.
type scriptedLogStreamer struct {
	mu       sync.Mutex
	payloads [][]byte
	opened   int
	holdLast bool
	backfill []byte
}

func (s *scriptedLogStreamer) ContainerLogsStream(ctx context.Context, _ string, _ time.Time) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.opened
	s.opened++
	last := idx >= len(s.payloads)-1
	if idx >= len(s.payloads) {
		idx = len(s.payloads) - 1
	}
	return &scriptedStream{data: s.payloads[idx], hold: s.holdLast && last, ctx: ctx}, nil
}

func (s *scriptedLogStreamer) ContainerLogsSince(context.Context, string, time.Time) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return io.NopCloser(bytes.NewReader(s.backfill)), nil
}

type scriptedStream struct {
	data []byte
	off  int
	hold bool
	ctx  context.Context
}

func (s *scriptedStream) Read(p []byte) (int, error) {
	if s.off < len(s.data) {
		n := copy(p, s.data[s.off:])
		s.off += n
		return n, nil
	}
	if !s.hold {
		return 0, io.EOF
	}
	<-s.ctx.Done()
	return 0, io.EOF
}

func (s *scriptedStream) Close() error { return nil }

type recordingLogWriter struct {
	mu     sync.Mutex
	group  string
	stream string
	writes [][]events.LogEntry
}

func (w *recordingLogWriter) EnsureLogGroup(context.Context, string) error { return nil }

func (w *recordingLogWriter) EnsureLogStream(context.Context, string, string) error { return nil }

func (w *recordingLogWriter) WriteLogEvents(_ context.Context, group, stream string, entries []events.LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.group, w.stream = group, stream
	w.writes = append(w.writes, append([]events.LogEntry(nil), entries...))
	return nil
}

func (w *recordingLogWriter) messages() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, batch := range w.writes {
		for _, e := range batch {
			out = append(out, e.Message)
		}
	}
	return out
}

func (w *recordingLogWriter) timestamps() []int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []int64
	for _, batch := range w.writes {
		for _, e := range batch {
			out = append(out, e.Timestamp)
		}
	}
	return out
}

func (w *recordingLogWriter) destination() (string, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.group, w.stream
}

func (w *recordingLogWriter) batchSizes() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]int, 0, len(w.writes))
	for _, batch := range w.writes {
		out = append(out, len(batch))
	}
	return out
}

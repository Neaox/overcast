package lambda

// container_logs.go — a Lambda container's output, from the in-container init
// to CloudWatch Logs, the Telemetry API and the X-Amz-Log-Result tail.
//
// The container's stdout and stderr are owned by Overcast's init
// (internal/lambdainit), which runs as PID 1 inside the execution environment
// and is the parent of the runtime. It reads every line, tags it with the
// invocation that was in flight at the moment of the read, numbers it, and
// ships it to the host on one long-lived chunked POST to initproto.LogsPath.
// logSink is the host end of that stream.
//
// What that buys, and why this file no longer contains a single wait constant:
// where a line belongs is *stated* by the process that read it, not inferred
// from Docker's log stream going quiet. So the tail for a request is exactly
// that request's lines, CloudWatch ordering is exact for every invoke, and the
// only wait left on the invoke path is "has the frame the init numbered arrived
// yet" — normally already true, because the init published it before it
// forwarded the response. See docs/plans/lambda-in-container-init.md.

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/containerlogs"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

const (
	// tailMaxBytes is what X-Amz-Log-Result carries: "the last 4 KB of the
	// execution log", as AWS documents it.
	tailMaxBytes = 4096
	// tailBufferCap is the headroom a per-request buffer grows to before it is
	// trimmed back to tailMaxBytes. Trimming on every append would copy the
	// whole buffer per line; trimming at twice the size amortises that to one
	// copy per buffer's worth of output, and a snapshot only ever reads the
	// last tailMaxBytes either way.
	tailBufferCap = 2 * tailMaxBytes

	// retainedRequestBuffers bounds the per-request buffers a container holds.
	// Invoke releases a request's buffer as soon as it has snapshotted it, so
	// in the ordinary case at most one is live; this is the backstop for an
	// invocation that never reached its release — a caller that went away, a
	// container torn down mid-flight — so a long-lived warm environment cannot
	// accumulate them.
	retainedRequestBuffers = 16

	// ingestBatchMax and ingestFlushInterval are the CloudWatch batch
	// boundary, the same 25 lines / 5 ms the shared follower uses: a burst
	// fills the batch, and a lone line is written well inside the time it takes
	// anyone to refresh a console.
	ingestBatchMax      = 25
	ingestFlushInterval = 5 * time.Millisecond
)

// logSinkConfig is everything a container's sink needs, all of it known before
// the container exists. The sink is built at that point deliberately: the init
// starts publishing INIT-phase frames the moment the container starts, which is
// long before the acquire returns a containerInstance, and those frames are
// what the INIT-timeout diagnostic quotes.
type logSinkConfig struct {
	logger    *zap.Logger
	clk       clock.Clock
	logWriter events.LogWriter // nil when CloudWatch Logs is not wired
	group     string
	stream    string
	region    string
	// functionName and initType are what an init-phase platform record carries
	// that the init cannot know: which function this environment runs, and
	// whether AWS would call its initialization "on-demand" or
	// "provisioned-concurrency". Both are settled before the container exists,
	// which is why the host fills them in at ingest rather than passing them
	// into the container — see platformInitRecord.
	functionName string
	initType     string
	logFormat    string
	appLogLevel  logLevel
	sysLogLevel  logLevel
	runtimeAPI   *RuntimeAPIServer
}

// logSink is one container's log pipeline: the host end of the init's frame
// stream, the per-request tail buffers, the Telemetry/Logs API publish and the
// CloudWatch batcher.
//
// Everything is serialised on mu, including the batcher (which is not safe for
// concurrent use): frames arrive on the ingest handler's goroutine while the
// invoke path writes START / END / REPORT and takes snapshots on its own.
type logSink struct {
	cfg     logSinkConfig
	batcher *containerlogs.CloudWatchBatcher
	ctx     context.Context
	cancel  context.CancelFunc

	mu sync.Mutex
	// containerIP and containerID are Docker's, and are not known until the
	// container has been created — after this sink exists. Both are guarded,
	// because attach writes them while the ingest goroutine is reading them.
	containerIP string
	containerID string
	// lastSeq is the highest frame sequence this sink has accepted. The init
	// numbers frames from 1 and replays its backlog after a reconnect, so a
	// frame at or below this has already been seen and is dropped.
	lastSeq uint64
	// openStreams is how many ingest connections are live, endedStreams how
	// many have ended. Together they say whether the init still has a way to
	// tell us anything — see awaitLogSeq.
	openStreams  int
	endedStreams int
	// idleSeq is the highest sequence the init reported when the runtime last
	// went idle and asked for work — initproto.HeaderLogSeq on GET /next. It
	// bounds everything the container printed *before* the invocation about to
	// be handed out: the INIT phase before the first one, and whatever the
	// runtime printed after answering the previous one before later ones.
	// Waiting for it before writing START is what keeps that output in front of
	// the START in CloudWatch, as it is on AWS.
	idleSeq uint64
	// changed is closed and replaced whenever lastSeq or the stream state
	// moves, which is how the two waits below are woken without a polling loop.
	changed chan struct{}
	// pending counts lines handed to the batcher since the last flush, so the
	// ingest loop knows when it has a full batch.
	pending int

	buffers map[string]*tailBuffer
	order   []string // request IDs in arrival order, for eviction
	initBuf tailBuffer
	// unaddressedRecords holds platform records that arrived before Docker had
	// told us the container's address, which is what they are published
	// against. Drained by attach.
	unaddressedRecords []string
	closed             bool
}

// newLogSink builds a container's sink. It talks to nothing until a frame or a
// synthesised line arrives, apart from ensureStream.
func newLogSink(cfg logSinkConfig) *logSink {
	if cfg.logger == nil {
		cfg.logger = zap.NewNop()
	}
	if cfg.clk == nil {
		cfg.clk = clock.New()
	}
	// The context carries the region the lines belong to: CloudWatch Logs is
	// regional, and the request that started the container is long gone by the
	// time most of its output is written.
	ctx, cancel := context.WithCancel(middleware.ContextWithRegion(context.Background(), cfg.region))
	s := &logSink{
		cfg:     cfg,
		ctx:     ctx,
		cancel:  cancel,
		changed: make(chan struct{}),
		buffers: make(map[string]*tailBuffer),
	}
	s.batcher = containerlogs.NewCloudWatchBatcher(containerlogs.BatcherConfig{
		Writer:  cfg.logWriter,
		Group:   cfg.group,
		Stream:  cfg.stream,
		Context: ctx,
		Region:  cfg.region,
		Clock:   cfg.clk,
		Logger:  cfg.logger,
	})
	return s
}

// ensureStream creates the log stream so the first line does not pay for it.
// Writes create it too, so a failure here is worth a line in the log and no
// more.
func (s *logSink) ensureStream() {
	if s.cfg.logWriter == nil {
		return
	}
	if err := s.batcher.EnsureStream(s.ctx); err != nil && s.ctx.Err() == nil {
		s.cfg.logger.Debug("container logs: ensure log stream failed",
			zap.String("container", s.container()),
			zap.String("group", s.cfg.group),
			zap.Error(err),
		)
	}
}

// attach records the container's address and ID once Docker has assigned them.
// The address is what Telemetry/Logs API records are published against, so any
// platform record that arrived before this is published now, in order — the
// INIT phase starts reporting itself well before Docker will say where the
// container is.
func (s *logSink) attach(containerIP, containerID string) {
	s.mu.Lock()
	s.containerIP = containerIP
	s.containerID = containerID
	held := s.unaddressedRecords
	s.unaddressedRecords = nil
	s.mu.Unlock()

	if containerIP == "" || s.cfg.runtimeAPI == nil {
		return
	}
	for _, event := range held {
		s.cfg.runtimeAPI.PublishInitPhaseRecord(containerIP, event)
	}
}

// container is this sink's container, short, for a diagnostic. Empty until the
// container exists.
func (s *logSink) container() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return shortContainerID(s.containerID)
}

// notifyLocked wakes both waits. Caller must hold mu.
func (s *logSink) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// streamOpened and streamClosed bracket one ingest connection. The init opens a
// new one after a reconnect and replays its backlog, so a sink outlives any
// number of them.
func (s *logSink) streamOpened() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openStreams++
	s.notifyLocked()
}

func (s *logSink) streamClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openStreams > 0 {
		s.openStreams--
	}
	s.endedStreams++
	s.notifyLocked()
}

// noteIdleSeq records what the init had published when the runtime went idle.
// Monotonic: a /next that arrives out of order, or one from a client that sends
// no header at all, must never move the watermark backwards.
func (s *logSink) noteIdleSeq(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.idleSeq {
		s.idleSeq = seq
	}
}

// idleSeq reports the sequence the next START must wait for.
func (s *logSink) idleSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idleSeq
}

// frame handles one line off the init's stream and reports whether it is now
// waiting in the CloudWatch batch. Frames are handled the moment they arrive:
// the tail buffers and the batch are updated here, and only the CloudWatch
// write itself is deferred to a batch boundary.
//
// # The one ordering rule in this file
//
// The line reaches its buffer and the batch, and *then* lastSeq moves and the
// waiters are woken — both inside one critical section. Nothing may be
// interleaved between those two, because awaitLogSeq returning is what tells
// the invoke path that this request's output is in hand: a waiter woken before
// the append would snapshot a tail missing the very line it waited for, which
// is precisely the class of race this whole mechanism exists to remove.
//
// So the two things that do not need the lock are done around it, never in the
// middle: the log-level decision before (applicationLogLevel is pure), and the
// Telemetry publish after. The publish must be outside the lock in any case —
// it takes the Runtime API's own mutex, and holding two in that order is a
// deadlock waiting to be written — and it is not part of the guarantee:
// subscribers are fed asynchronously through a queue, and the ordering promise
// is about CloudWatch and the tail.
func (s *logSink) frame(f initproto.Frame) bool {
	if f.Rec.Type != "" {
		return s.platformFrame(f)
	}
	// An extension's output is an extension record, named by the directory
	// entry the init started it from — no prefix convention to parse back out.
	logType := "function"
	if _, ok := initproto.ExtensionName(f.Src); ok {
		logType = "extension"
	}
	// Log-level filtering needs the JSON log format; in Text mode AWS sends
	// every application record through untouched. It applies to CloudWatch and
	// the tail only — a subscriber sees the record either way.
	keep := s.cfg.logFormat != logFormatJSON || applicationLogLevel(f.Msg) >= s.cfg.appLogLevel

	s.mu.Lock()
	if f.Seq <= s.lastSeq {
		// A replay after a reconnect. The init cannot know which of the frames
		// it wrote to a connection that then failed actually arrived, so it
		// re-sends from the oldest it still holds; the sequence is what tells
		// the duplicates apart.
		s.mu.Unlock()
		return false
	}
	containerIP := s.containerIP
	containerID := s.containerID
	batched := false
	// A gap frame is a report of lines that will never arrive, not a line: it
	// advances the sequence so a waiter is released rather than held to its
	// bound, and puts nothing anywhere.
	if f.Gap == 0 && keep {
		s.bufferFor(f.Req).append(f.Msg)
		// Timestamped from the host's clock at ingest rather than from the
		// frame: the frame's clock is the container's, and a container whose
		// clock differs from the host's by a millisecond would have its lines
		// re-ordered against the START / END / REPORT lines the host writes
		// around them. Ordering is the point of this pipeline, and Frame.T is
		// documented as display only.
		s.batcher.Line(containerlogs.Line{Message: f.Msg})
		s.pending++
		batched = true
	}
	s.lastSeq = f.Seq
	s.notifyLocked()
	s.mu.Unlock()

	if f.Gap > 0 {
		// The init's replay backlog overflowed while the connection was down,
		// so those lines are gone from this transport for good. They are not
		// gone altogether: the init tees every line to the container's own
		// stdout, which is what this warning is pointing a human at.
		s.cfg.logger.Warn("lambda container logs: the init dropped frames while the log channel was down — `docker logs` still holds the complete output",
			zap.String("container", shortContainerID(containerID)),
			zap.String("log_group", s.cfg.group),
			zap.Uint64("frames_lost", f.Gap),
			zap.Uint64("through_seq", f.Seq),
		)
		return false
	}
	if containerIP != "" {
		s.cfg.runtimeAPI.PublishExtensionLog(containerIP, logType, f.Msg)
	}
	return batched
}

// platformFrame handles one of the init's platform-telemetry observations and
// reports whether it is now waiting in the CloudWatch batch. It is frame's
// other half, and it keeps that function's one ordering rule: the record
// reaches the batch, and *then* lastSeq moves and the waiters are woken, inside
// one critical section.
//
// Three things separate a record from a line:
//
//   - It is marshalled here, into the Telemetry API event AWS documents, from
//     the observation the init made plus what only the host knows (the
//     function, its version, how the environment was initialized).
//   - Subscribers see it whatever the log format is, because the Telemetry API
//     is not affected by the CloudWatch log format or level. CloudWatch sees it
//     only under LogFormat: JSON, filtered by SystemLogLevel — in Text mode
//     AWS's counterpart of platform.initStart is an INIT_START line whose
//     entire content is a managed runtime's version and its ARN, neither of
//     which Overcast has (and neither of which a container-image function has
//     at all), and there is no plain-text counterpart of the other two.
//   - It never enters a tail buffer. It belongs to no invocation, so
//     X-Amz-Log-Result is unaffected, and it is not container output, so the
//     INIT-timeout diagnostic does not quote it.
func (s *logSink) platformFrame(f initproto.Frame) bool {
	rec, ok := platformInitRecord(f.Rec, s.cfg.functionName, s.cfg.initType)
	if !ok {
		s.cfg.logger.Debug("lambda container logs: the init reported a platform record this build does not know",
			zap.String("container", s.container()),
			zap.String("record", f.Rec.Type),
			zap.String("status", f.Rec.Status),
		)
		// Still a frame: it has to advance the sequence, or a waiter for a seq
		// at or above it would be held to its bound.
		return s.advanceSeq(f.Seq)
	}
	// Marshalled unconditionally, unlike the invocation records
	// (emitPlatformRecord, which builds one for a Text-mode subscriber only if
	// there is one): there are exactly three of these per execution
	// environment, and none of them is on the invoke path.
	event := renderPlatformEvent(s.cfg.clk.Now(), rec)
	toCloudWatch := s.cfg.logFormat == logFormatJSON && rec.systemLogLevel() >= s.cfg.sysLogLevel

	s.mu.Lock()
	if f.Seq <= s.lastSeq {
		s.mu.Unlock()
		return false
	}
	containerIP := s.containerIP
	if containerIP == "" && len(s.unaddressedRecords) < initPhaseRecordsRetained {
		// The init publishes platform.initStart as the container starts, which
		// is before Docker has told us the container's address — and the
		// address is what a Telemetry record is published against. Held here
		// until attach knows it; see attach.
		s.unaddressedRecords = append(s.unaddressedRecords, event)
	}
	batched := false
	if toCloudWatch && event != "" {
		s.batcher.Line(containerlogs.Line{Message: event})
		s.pending++
		batched = true
	}
	s.lastSeq = f.Seq
	s.notifyLocked()
	s.mu.Unlock()

	if containerIP != "" {
		// Retained as well as published: an extension subscribes to the
		// Telemetry API during the very phase these records describe, and
		// initStart is published before the extensions are even started.
		s.cfg.runtimeAPI.PublishInitPhaseRecord(containerIP, event)
	}
	return batched
}

// advanceSeq moves the watermark for a frame that put nothing anywhere, and
// reports false because nothing is waiting in the batch.
func (s *logSink) advanceSeq(seq uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.lastSeq {
		s.lastSeq = seq
		s.notifyLocked()
	}
	return false
}

// flush writes the pending CloudWatch batch and reports how many lines it held.
func (s *logSink) flush() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *logSink) flushLocked() int {
	n := s.batcher.Flush()
	s.pending = 0
	return n
}

// batchFull reports whether the pending batch has reached the batch boundary.
func (s *logSink) batchFull() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending >= ingestBatchMax
}

// synth delivers one line the execution environment produced rather than the
// container — START, END, REPORT, or an Overcast warning — into reqID's tail
// buffer and to CloudWatch.
//
// The pending batch is flushed first, and that is what makes CloudWatch
// ordering exact: the invoke path has already waited for this request's frames
// to arrive (awaitLogSeq), so they are in the batch, and writing END before
// flushing it would put END in front of the output it closes.
func (s *logSink) synth(reqID, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bufferFor(reqID).append(line)
	if s.cfg.logWriter == nil {
		return
	}
	s.flushLocked()
	s.batcher.Write(s.ctx, []events.LogEntry{
		{Timestamp: s.cfg.clk.Now().UnixMilli(), Message: line},
	})
}

// awaitLogSeq blocks until the sink has ingested every frame the init numbered
// up to seq, the init's stream has ended, or ctx expires. It reports whether
// the frames arrived.
//
// seq is what the init put in initproto.HeaderLogSeq on the runtime's response:
// "every frame at or below this was published on the log stream before this
// request was forwarded". Because those frames went out on an already-open
// connection before the response was sent on another, the wait is normally
// already satisfied when it is entered — there is no round trip here.
//
// A stream that has ended counts as satisfied, because nothing more can arrive
// over it. That includes the moment between a dropped connection and the init's
// reconnect, which is a deliberate simplification: the caller bounds this wait
// anyway, and "the last 4 KB of what arrived" is exactly what LogResult
// promises.
func (s *logSink) awaitLogSeq(ctx context.Context, seq uint64) bool {
	if seq == 0 {
		// No header, so nothing to wait for: either a Runtime API client that
		// is not behind our init, or an invocation the init answered without
		// ever publishing a line.
		return true
	}
	for {
		s.mu.Lock()
		if s.lastSeq >= seq {
			s.mu.Unlock()
			return true
		}
		if s.closed || (s.endedStreams > 0 && s.openStreams == 0) {
			s.mu.Unlock()
			return false
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

// awaitStreamEnd blocks until the init's log stream has ended or ctx expires,
// and reports which. The init drains both pipes, flushes and closes the stream
// when its child exits, so a stream that has ended is the event meaning "the
// container has told us everything it is ever going to". It is what replaces
// the silence-plus-a-bound drain the daemon read-back path needed on the crash
// and timeout paths.
func (s *logSink) awaitStreamEnd(ctx context.Context) bool {
	for {
		s.mu.Lock()
		ended := s.closed || (s.endedStreams > 0 && s.openStreams == 0)
		// A sink no connection has ever reached has nothing to drain and
		// nothing to wait for; the caller's bound would be dead time.
		never := s.endedStreams == 0 && s.openStreams == 0
		changed := s.changed
		s.mu.Unlock()
		if ended || never {
			return true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

// snapshot returns the last tailMaxBytes of reqID's log, which is what
// X-Amz-Log-Result carries: START, the request's own lines in the order the
// init read them, END, REPORT.
func (s *logSink) snapshot(reqID string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf, ok := s.buffers[reqID]
	if !ok {
		return nil
	}
	return buf.snapshot()
}

// release drops a request's buffer once its invoke has answered.
func (s *logSink) release(reqID string) {
	if reqID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropLocked(reqID)
}

// initOutput is what the container printed before its first invocation — the
// INIT phase — plus anything it printed between invocations. It is what the
// INIT-timeout diagnostic quotes, and it is empty exactly when the init never
// got far enough to say anything, which is when that diagnostic falls back to
// the daemon's copy.
func (s *logSink) initOutput() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.initBuf.snapshot())
}

// close flushes what is in hand and ends the sink's context. A frame that
// arrives afterwards still reaches CloudWatch (the batcher re-issues on a fresh
// region-carrying context) but nothing waits for one.
func (s *logSink) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.flushLocked()
	s.notifyLocked()
	s.mu.Unlock()
	s.cancel()
}

// bufferFor returns the tail buffer a line belongs in. Caller must hold mu.
// An empty request ID is the INIT phase and the space between invocations,
// which is exactly what the init reports as an empty Frame.Req.
func (s *logSink) bufferFor(reqID string) *tailBuffer {
	if reqID == "" {
		return &s.initBuf
	}
	if buf, ok := s.buffers[reqID]; ok {
		return buf
	}
	buf := &tailBuffer{}
	s.buffers[reqID] = buf
	s.order = append(s.order, reqID)
	for len(s.order) > retainedRequestBuffers {
		s.dropLocked(s.order[0])
	}
	return buf
}

// dropLocked removes one request's buffer. Caller must hold mu.
func (s *logSink) dropLocked(reqID string) {
	if _, ok := s.buffers[reqID]; !ok {
		return
	}
	delete(s.buffers, reqID)
	for i, id := range s.order {
		if id == reqID {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// tailBuffer is one request's rolling log: lines in the order they were
// delivered, bounded so a chatty handler costs a fixed amount of memory and a
// snapshot is always the most recent tailMaxBytes.
type tailBuffer struct {
	buf []byte
}

func (b *tailBuffer) append(line string) {
	b.buf = append(append(b.buf, line...), '\n')
	if len(b.buf) > tailBufferCap {
		n := copy(b.buf, b.buf[len(b.buf)-tailMaxBytes:])
		b.buf = b.buf[:n]
	}
}

func (b *tailBuffer) snapshot() []byte {
	if len(b.buf) == 0 {
		return nil
	}
	out := b.buf
	if len(out) > tailMaxBytes {
		out = out[len(out)-tailMaxBytes:]
	}
	return append([]byte(nil), out...)
}

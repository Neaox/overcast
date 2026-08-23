package lambda

// container_logs.go — a Lambda container's output, from Docker to CloudWatch.
//
// The follower itself lives in internal/containerlogs, where ECS uses it too:
// opening and re-opening the daemon's log stream, demultiplexing it, assembling
// bounded lines, recognising the ones a reconnect replays, batching the rest
// into CloudWatch writes. What is Lambda's own is here — where a line goes
// besides CloudWatch (the Telemetry/Logs API and the rolling tail buffer that
// answers X-Amz-Log-Result), and the pipeline watermarks its tail and drain
// waits are built on.
//
// The second of those is transitional. See logObserver.

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/containerlogs"
	"github.com/Neaox/overcast/internal/events"
)

// logReconcileTimeout bounds the close-time backfill pass over the daemon's
// persisted log file. It is a plain read of a file the daemon already has, and
// it runs on the teardown path, so it is generous but finite: a daemon that
// cannot answer it in this long is not going to.
const logReconcileTimeout = 5 * time.Second

// initLogFollower wires this container's log pipeline: a CloudWatch batcher, a
// sink that puts each line in front of the Telemetry API and the tail buffer on
// its way there, and the follower that feeds them.
func (ci *containerInstance) initLogFollower() {
	batcher := ci.newCloudWatchBatcher()
	ci.logSink = &containerLogSink{ci: ci, batcher: batcher}
	ci.logFollower = containerlogs.New(containerlogs.Config{
		Client:      ci.docker,
		ContainerID: ci.id,
		Sink:        ci.logSink,
		Clock:       ci.clk,
		Logger:      ci.logger,
		Observer:    logObserver{ci: ci},
	})
}

// streamLogs runs as a background goroutine for the lifetime of the container,
// following its output into CloudWatch Logs and the rolling tail buffer. It
// exits when logCtx is cancelled, which is what Close does.
func (ci *containerInstance) streamLogs() {
	defer close(ci.logDone)
	ctx := ci.logCtx

	// Create the stream up front so the first line does not pay for it. Writes
	// create it too, so failing here is worth a line in the log and no more —
	// and the tail buffer is maintained either way.
	if err := ci.logSink.batcher.EnsureStream(ctx); err != nil {
		if ctx.Err() == nil {
			ci.logger.Debug("container logs: ensure log stream failed",
				zap.String("container", shortContainerID(ci.id)),
				zap.String("group", ci.logGroupName),
				zap.Error(err),
			)
		}
	}

	ci.logFollower.Follow(ctx)
}

// newCloudWatchBatcher builds a writer for this container's log stream. The
// region comes from the function's ARN rather than the caller's request, so a
// cross-region invoke's output lands where the function lives.
func (ci *containerInstance) newCloudWatchBatcher() *containerlogs.CloudWatchBatcher {
	return containerlogs.NewCloudWatchBatcher(containerlogs.BatcherConfig{
		Writer:  ci.logWriter,
		Group:   ci.logGroupName,
		Stream:  ci.logStream,
		Context: ci.logCtx,
		Region:  regionFromFunctionARN(ci.functionARN),
		Clock:   ci.clk,
		Logger:  ci.logger,
	})
}

// writeEventsWithRetry delivers synthesised lines — START, END, REPORT — that
// never come off the container's log stream, on the same bounded retry the
// follower's batches use.
func (ci *containerInstance) writeEventsWithRetry(ctx context.Context, entries []events.LogEntry) bool {
	if ci.logWriter == nil || len(entries) == 0 {
		return true
	}
	return ci.newCloudWatchBatcher().Write(ctx, entries)
}

// containerLogSink is what the follower hands each of this container's lines
// to. Every line goes to the Telemetry/Logs API and the rolling tail buffer
// first, and reaches CloudWatch only if the function's ApplicationLogLevel does
// not filter it out.
type containerLogSink struct {
	ci      *containerInstance
	batcher *containerlogs.CloudWatchBatcher
}

func (s *containerLogSink) Line(line containerlogs.Line) bool {
	// A backfilled line is not live output: it was recovered from the daemon's
	// persisted log file after the container was gone, so the invocation whose
	// tail buffer it would have joined has long since answered and the
	// Telemetry API subscribers went with the container. It goes to CloudWatch
	// and no further, which is what the reconciliation pass has always done.
	if !line.Backfill && !s.ci.ingestContainerLine(line.Message) {
		return false
	}
	return s.batcher.Line(line)
}

func (s *containerLogSink) Flush() int { return s.batcher.Flush() }

// The two values of containerInstance.logParkedAt that are a state rather
// than the moment the reader parked in its current Read.
const (
	// logReaderNotReading: no Docker log stream is open, so nothing is on its
	// way through one. This is the zero value, which is what an instance whose
	// follower has not started — or has ended — should read as. It covers two
	// different situations that dockerSilentSince tells apart via
	// logStreamEverAnswered: a container's very first connect, still racing
	// container start, and an already-proven stream between reconnects.
	logReaderNotReading = 0
	// logReaderBetweenReads: a stream is open and the reader is not blocked in
	// a Read on it — either it has not reached its first, or one has returned
	// and it has not got back yet. Either way it has no question outstanding
	// with Docker, so Docker having handed nothing over says nothing about the
	// container.
	logReaderBetweenReads = -1
)

// logObserver keeps a container's log-pipeline watermarks current from the
// follower's own progress. Four of them:
//
//   - logStreamEverAnswered — the daemon has answered a connect at least once,
//     with a stream or a refusal. Until it has, "no stream open" is a question
//     still in flight rather than an answer (#1160).
//   - logParkedAt — where the reader is: blocked in a live Read (the moment it
//     entered one), between two of them, or with no stream at all. Which of
//     those it is decides whether Docker's silence is evidence about the
//     container, and how tightly it is bounded (#873, #1325).
//   - logReadAt — when the daemon last handed over bytes, which is what
//     separates "this invocation printed nothing" from "it printed something
//     that has not become a line yet".
//   - logInFlight — lines assembled but not yet written, so a teardown drain
//     does not close the stream on output that is already in hand.
//
// # Why this exists at all
//
// Docker's log stream carries no request boundaries, so nothing in it can say
// where one invocation's output ends. waitForScannerIdle and waitForLogDrain
// infer it from these four, and getting the inference right took four rounds of
// fixes. The follower is the only thing that knows the facts they need, and
// they are of no interest to any other user of it — hence an optional Observer
// rather than anything in the follower's own contract.
//
// # Its future
//
// docs/plans/lambda-in-container-init.md replaces the whole inference with a
// protocol: an init process inside the container owns the runtime's stdout and
// tells Overcast where each invocation's output ends. Its Phase 2 deletes both
// waits, and Phase 3 deletes this type, these constants and the four atomics
// with them.
type logObserver struct{ ci *containerInstance }

func (o logObserver) StreamAnswered() { o.ci.logStreamEverAnswered.Store(true) }

func (o logObserver) StreamOpened() { o.ci.logParkedAt.Store(logReaderBetweenReads) }

func (o logObserver) StreamClosed() { o.ci.logParkedAt.Store(logReaderNotReading) }

func (o logObserver) ReadStarted() { o.ci.logParkedAt.Store(o.ci.clk.Now().UnixNano()) }

func (o logObserver) ReadReturned(n int) {
	o.ci.logParkedAt.Store(logReaderBetweenReads)
	if n > 0 {
		o.ci.logReadAt.Store(o.ci.clk.Now().UnixNano())
	}
}

func (o logObserver) LineScanned() { o.ci.logInFlight.Add(1) }

func (o logObserver) LinesRetired(n int) { o.ci.logInFlight.Add(-int64(n)) }

var (
	_ containerlogs.Observer = logObserver{}
	_ containerlogs.LineSink = (*containerLogSink)(nil)
)

// Package containerlogs follows a Docker container's output and delivers it to
// CloudWatch Logs.
//
// Reading a container's output back from the daemon is how anything whose logs
// reach CloudWatch through a log driver works on AWS, and two services here
// need it: ECS, for a task definition that asks for `awslogs`, and Lambda, for
// a function's stdout and stderr. Doing it well is more than opening
// `GET /containers/{id}/logs?follow=true` and scanning lines off it. The
// connection breaks mid-container and the follow has to resume where it left
// off — which the daemon can only do by timestamp, so it replays the whole
// last nanosecond and the follower has to recognise what it has already seen.
// A single line can be larger than CloudWatch will accept in one event, and
// larger than Docker's own frame, so it arrives in pieces with a timestamp on
// each. Writing a line at a time costs a store round trip per line where a
// batch costs one for twenty-five.
//
// Lambda learned all of that over four rounds of fixes. This package is where
// that follower lives so ECS does not have to learn it again.
//
// The pieces:
//
//   - Follower opens and re-opens the stream, demultiplexes it, assembles
//     bounded lines, drops the duplicates a reconnect replays, and hands what
//     is left to a LineSink. It drains what is already buffered when its
//     context is cancelled, and Reconcile backfills from the daemon's persisted
//     log file once the container has stopped.
//   - CloudWatchBatcher is the LineSink both services end at: batched writes to
//     one log stream, with a bounded retry.
//   - Observer is a transitional hook for Lambda's tail-wait machinery; see its
//     doc comment.
//
// Nothing here calls time.Now: every deadline, backoff and fallback timestamp
// comes from an injected clock.Clock, so a test drives the whole pipeline on a
// mock clock.
package containerlogs

import (
	"bufio"
	"context"
	"io"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/docker"
)

// LogStreamer is the part of the Docker client a Follower uses: the two log
// endpoints, one following and one not.
type LogStreamer interface {
	// ContainerLogsStream opens a follow=true, timestamps=true stream, resuming
	// from since (zero for "from the start of the container").
	ContainerLogsStream(ctx context.Context, id string, since time.Time) (io.ReadCloser, error)
	// ContainerLogsSince fetches the same shape of payload without following,
	// which on a stopped container is its complete log from since onwards.
	ContainerLogsSince(ctx context.Context, id string, since time.Time) (io.ReadCloser, error)
}

var _ LogStreamer = (*docker.Client)(nil)

// Line is one line of container output, as a Follower recovered it.
type Line struct {
	// Time is the timestamp the daemon stamped on the line. It is zero when the
	// line carried none, which happens for every line after the first inside a
	// single Docker frame — the daemon stamps the frame, not the line.
	Time time.Time
	// Message is the line itself: the timestamp prefix removed, the trailing
	// newline removed, and the remainder truncated to what CloudWatch accepts
	// in one event.
	Message string
	// Backfill is true for a line recovered by Reconcile after the follow
	// stream closed for good, rather than read live from it. A sink that does
	// something with a line beyond writing it — attributing it to a request in
	// flight, publishing it to a subscriber — generally wants to skip that part
	// for a backfilled line: by the time one arrives the container is gone.
	Backfill bool
}

// LineSink receives every line a Follower recovers, in order, already
// demultiplexed, de-duplicated and split from its timestamp.
type LineSink interface {
	// Line handles one line and reports whether it is now pending delivery —
	// held for a later Flush rather than finished with. A sink that drops a
	// line outright (a log-level filter, say) returns false, which is what
	// stops the follower counting it as work still in the pipeline.
	Line(line Line) bool
	// Flush delivers everything Line has accepted since the last Flush, and
	// reports how many lines that was. The follower calls it at every batch
	// boundary — batchMax lines, flushInterval after the most recent line, and
	// once more when a stream ends — so a sink needs no timer of its own.
	Flush() int
}

const (
	// batchMax and flushInterval are the batch boundary. Twenty-five lines is
	// what a burst of output fills in well under the interval, and 5 ms is
	// short enough that a single line printed on its own is in CloudWatch
	// before anyone can refresh a console.
	batchMax      = 25
	flushInterval = 5 * time.Millisecond

	// firstReconnectBackoff and maxReconnectBackoff bound how hard a broken
	// follow retries. The first reconnect is quick because the overwhelming
	// majority are a daemon hiccup that is already over; the ceiling is what
	// stops a container the daemon will never serve again from becoming a
	// steady load on it.
	firstReconnectBackoff = 50 * time.Millisecond
	maxReconnectBackoff   = 2 * time.Second

	// drainCap bounds the wait for lines already buffered when the context is
	// cancelled. Cancelling closes the daemon connection, so this is only ever
	// the reader goroutine finishing what it holds.
	drainCap = time.Second

	// readBufferSize is the line reader's buffer. Docker chunks a long line at
	// 16 KiB, so this holds four chunks without a refill.
	readBufferSize = 64 * 1024

	// scanQueueDepth lets the reader goroutine run ahead of the batching loop
	// by a batch or two, so a slow CloudWatch write does not stall the read
	// side of the pipeline.
	scanQueueDepth = 64
)

// Config describes one container's follower. Client, ContainerID and Sink are
// required; the rest have working defaults.
type Config struct {
	// Client reaches the daemon. In production this is *docker.Client.
	Client LogStreamer
	// ContainerID is the container to follow.
	ContainerID string
	// Sink receives the lines.
	Sink LineSink
	// Clock is the time source. Defaults to clock.New().
	Clock clock.Clock
	// Logger receives the follower's own diagnostics. Defaults to a no-op.
	Logger *zap.Logger
	// Observer is optional and transitional — see Observer.
	Observer Observer
}

// Follower follows one container's log stream for as long as its context lives.
type Follower struct {
	client LogStreamer
	id     string
	sink   LineSink
	clk    clock.Clock
	log    *zap.Logger
	obs    Observer
	cursor cursor
}

// New builds a Follower. It opens nothing: call Follow.
func New(cfg Config) *Follower {
	f := &Follower{
		client: cfg.Client,
		id:     cfg.ContainerID,
		sink:   cfg.Sink,
		clk:    cfg.Clock,
		log:    cfg.Logger,
		obs:    cfg.Observer,
	}
	if f.clk == nil {
		f.clk = clock.New()
	}
	if f.log == nil {
		f.log = zap.NewNop()
	}
	if f.obs == nil {
		f.obs = nopObserver{}
	}
	return f
}

// Follow streams the container's output to the sink until ctx is cancelled.
//
// It reconnects whenever the stream ends for any reason other than that
// cancellation, resuming from just before the newest line it has delivered.
// Asking the daemon to resume from a timestamp is the only resume it offers,
// and it is inclusive, so a reconnect replays every line sharing that
// nanosecond; the cursor counts them and admits only the ones past the count it
// had already seen. A container that keeps running while the daemon is
// unreachable therefore loses nothing but the time.
//
// Follow blocks. Run it on a goroutine of its own, and cancel ctx to stop it.
func (f *Follower) Follow(ctx context.Context) {
	var since time.Time
	backoff := firstReconnectBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		f.followOnce(ctx, since)
		if ctx.Err() != nil {
			return
		}

		// The stream ended on its own. Resume from the high watermark.
		since = f.cursor.Since()
		f.log.Debug("container logs: reconnecting",
			zap.String("container", shortID(f.id)),
			zap.Duration("backoff", backoff),
		)
		select {
		case <-f.clk.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > maxReconnectBackoff {
			backoff = maxReconnectBackoff
		}
	}
}

// scanResult is one line off the reader goroutine, or the error that ended it.
type scanResult struct {
	line string
	err  error
}

// followOnce opens one connection and batches lines off it until the stream
// ends or ctx is cancelled. It returns on any error so Follow can reconnect.
func (f *Follower) followOnce(ctx context.Context, since time.Time) {
	stream, err := f.client.ContainerLogsStream(ctx, f.id, since)
	if err != nil {
		// The daemon has answered, if only to refuse. That is what an Observer
		// needs to know: a follower with no stream and nothing in flight is
		// backing off from an answer, not waiting on a question still in
		// flight.
		f.obs.StreamAnswered()
		if ctx.Err() == nil {
			f.log.Warn("container logs: open stream failed",
				zap.String("container", shortID(f.id)),
				zap.Error(err),
			)
		}
		return
	}
	defer stream.Close()

	// Wrap the multiplexed stream so the line reader sees a plain byte stream,
	// with the timestamp the daemon repeats on each continuation chunk of a
	// long line dropped rather than spliced into the middle of it.
	tracked := &trackedReader{r: docker.NewLogDemuxReader(stream), obs: f.obs}
	// The reader is on this connection but has not reached its first Read,
	// which is the same position it is in between any two of them. Bounding the
	// state to the connection's life at both ends matters: once this one is
	// gone nothing can arrive over it, and an observer left believing a read is
	// outstanding would wait out a reconnect backoff for an answer that cannot
	// come.
	f.obs.StreamAnswered()
	f.obs.StreamOpened()
	defer f.obs.StreamClosed()

	reader := bufio.NewReaderSize(tracked, readBufferSize)
	adm := f.cursor.newAdmission(!since.IsZero())

	// Line assembly runs on its own goroutine so a CloudWatch write never
	// leaves the daemon's pipe unread.
	lines := make(chan scanResult, scanQueueDepth)
	go func() {
		defer close(lines)
		for {
			line, err := readBoundedLine(reader, maxLineBytes)
			if err != nil {
				if err != io.EOF {
					lines <- scanResult{err: err}
				}
				return
			}
			f.obs.LineScanned()
			lines <- scanResult{line: line}
		}
	}()

	pending := 0
	flush := func() {
		if pending == 0 {
			return
		}
		f.obs.LinesRetired(f.sink.Flush())
		pending = 0
	}

	flushTimer := f.clk.Timer(flushInterval)
	flushTimer.Stop()
	defer flushTimer.Stop()

	for {
		select {
		case res, ok := <-lines:
			if !ok {
				flush()
				return
			}
			if res.err != nil {
				if ctx.Err() == nil {
					f.log.Debug("container log stream ended",
						zap.String("container", shortID(f.id)),
						zap.Error(res.err),
					)
				}
				flush()
				return
			}
			if !f.accept(res.line, adm, false) {
				continue
			}
			pending++
			if pending >= batchMax {
				flush()
				flushTimer.Stop()
			} else {
				flushTimer.Reset(flushInterval)
			}

		case <-flushTimer.C:
			flush()

		case <-ctx.Done():
			// Drain until the reader goroutine finishes or the cap fires.
			// Cancelling ctx closed the daemon connection, so any bytes the
			// reader already holds are still delivered before lines closes.
			pending += f.drain(lines, adm)
			flush()
			return
		}
	}
}

// accept runs one raw line through the cursor and the sink, reporting whether
// it is now pending delivery. Every line that does not make it is retired here,
// so an Observer's in-flight count is decremented exactly once per line
// however the line ends.
func (f *Follower) accept(raw string, adm *cursorAdmission, backfill bool) bool {
	line, ok := admitLine(raw, adm, backfill)
	if !ok || !f.sink.Line(line) {
		f.obs.LinesRetired(1)
		return false
	}
	return true
}

// drain empties the reader goroutine's queue after cancellation, returning how
// many lines it added to the pending batch.
func (f *Follower) drain(lines <-chan scanResult, adm *cursorAdmission) int {
	added := 0
	drainTimer := f.clk.Timer(drainCap)
	defer drainTimer.Stop()
	for {
		select {
		case res, ok := <-lines:
			if !ok || res.err != nil {
				return added
			}
			if f.accept(res.line, adm, false) {
				added++
			}
		case <-drainTimer.C:
			return added
		}
	}
}

// Reconcile fetches everything the container logged after the follower's high
// watermark through a non-streaming request and delivers what the follow missed.
//
// It is the last safety net, and it only means anything once the container has
// stopped: the daemon's persisted log file is complete then, so a follow that
// died and never reconnected still costs nothing. Lines it recovers are marked
// Backfill.
//
// Call it after Follow has returned, on a context of its own — the one that
// stopped the follow is cancelled.
func (f *Follower) Reconcile(ctx context.Context) {
	since := f.cursor.Since()
	body, err := f.client.ContainerLogsSince(ctx, f.id, since)
	if err != nil {
		f.log.Debug("container logs: reconciliation fetch failed",
			zap.String("container", shortID(f.id)),
			zap.Error(err),
		)
		return
	}
	defer body.Close()

	reader := bufio.NewReaderSize(docker.NewLogDemuxReader(body), readBufferSize)
	adm := f.cursor.newAdmission(!since.IsZero())

	delivered := 0
	for {
		raw, readErr := readBoundedLine(reader, maxLineBytes)
		if readErr != nil {
			if readErr != io.EOF {
				f.log.Debug("container logs: reconciliation read ended",
					zap.String("container", shortID(f.id)),
					zap.Error(readErr),
				)
			}
			break
		}
		line, ok := admitLine(raw, adm, true)
		if !ok {
			continue
		}
		if f.sink.Line(line) {
			delivered++
		}
	}
	if delivered > 0 {
		f.sink.Flush()
	}
}

// trackedReader reports every Read on the log stream to an Observer. It is the
// only thing that can tell a follower parked on a live daemon round trip apart
// from one that has no connection at all — see Observer.
type trackedReader struct {
	r   io.Reader
	obs Observer
}

func (t *trackedReader) Read(p []byte) (int, error) {
	t.obs.ReadStarted()
	n, err := t.r.Read(p)
	t.obs.ReadReturned(n)
	return n, err
}

// shortID trims a container ID to the 12 characters the daemon's own tooling
// shows.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

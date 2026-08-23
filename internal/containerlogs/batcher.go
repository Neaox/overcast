package containerlogs

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

// retryDelays is how a failed CloudWatch write backs off. Three attempts over
// a quarter of a second: enough for a store that is momentarily busy, short
// enough that a genuinely broken write does not hold the follower's batching
// loop — and with it the read side of the pipeline — for long.
var retryDelays = []time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 250 * time.Millisecond}

// BatcherConfig describes where one container's lines go.
type BatcherConfig struct {
	// Writer is the CloudWatch Logs store.
	Writer events.LogWriter
	// Group and Stream name the destination.
	Group  string
	Stream string
	// Context is the long-lived context writes are made on. It must carry the
	// region the lines belong to: CloudWatch Logs is regional, the request that
	// started the container is long gone, and a write on a context with no
	// region lands in the wrong log group or none at all. It may be nil.
	Context context.Context
	// Region is that same region, kept so a write can be re-issued on a fresh
	// context after Context is cancelled — which is exactly when the last batch
	// of a container being torn down is written, and the one time losing it
	// would be noticed.
	Region string
	// Clock is the time source for retry delays and for the fallback timestamp
	// of a line that carried none. Defaults to clock.New().
	Clock clock.Clock
	// Logger receives the diagnostic for a write that failed every attempt.
	// Defaults to a no-op.
	Logger *zap.Logger
}

// CloudWatchBatcher is the LineSink that writes to CloudWatch Logs: it collects
// lines and writes them in one call per batch, retrying a failed write a
// bounded number of times.
//
// It is not safe for concurrent use. A Follower calls Line and Flush from one
// goroutine, which is the intended shape; Write is independent of the batch and
// may be called from anywhere.
type CloudWatchBatcher struct {
	cfg   BatcherConfig
	batch []events.LogEntry
}

// NewCloudWatchBatcher builds a batcher. It talks to nothing until a line is
// flushed.
func NewCloudWatchBatcher(cfg BatcherConfig) *CloudWatchBatcher {
	if cfg.Clock == nil {
		cfg.Clock = clock.New()
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &CloudWatchBatcher{cfg: cfg}
}

// EnsureStream creates the log stream up front, so the first line does not pay
// for it. Writes create it too, so a failure here is worth reporting and not
// worth stopping for.
func (b *CloudWatchBatcher) EnsureStream(ctx context.Context) error {
	if b.cfg.Writer == nil {
		return nil
	}
	return b.cfg.Writer.EnsureLogStream(ctx, b.cfg.Group, b.cfg.Stream)
}

// Line adds a line to the current batch. It always accepts.
func (b *CloudWatchBatcher) Line(line Line) bool {
	b.batch = append(b.batch, events.LogEntry{
		Timestamp: eventTimestampMillis(line.Time, b.cfg.Clock.Now()),
		Message:   line.Message,
	})
	return true
}

// Flush writes the current batch and reports how many lines it held. The batch
// is cleared whether the write succeeded or not: a batch that survived the
// retries is not going to be written by keeping it.
func (b *CloudWatchBatcher) Flush() int {
	n := len(b.batch)
	if n == 0 {
		return 0
	}
	b.write(b.liveContext(), b.batch)
	b.batch = b.batch[:0]
	return n
}

// Write delivers entries now, outside the batch, retrying a failed write.
// It reports whether the entries were written.
//
// ctx is the context to write on. A context that is already cancelled — or that
// is cancelled while the retries are running — is replaced by a fresh one
// carrying the configured region, because the write that matters most is the
// one racing a teardown.
func (b *CloudWatchBatcher) Write(ctx context.Context, entries []events.LogEntry) bool {
	if ctx == nil || ctx.Err() != nil {
		ctx = b.liveContext()
	}
	return b.write(ctx, entries)
}

// write is Write with the context already settled.
func (b *CloudWatchBatcher) write(writeCtx context.Context, entries []events.LogEntry) bool {
	if b.cfg.Writer == nil || len(entries) == 0 {
		return true
	}
	_ = b.cfg.Writer.EnsureLogStream(writeCtx, b.cfg.Group, b.cfg.Stream)

	var err error
	for attempt := 0; attempt < len(retryDelays); attempt++ {
		if attempt > 0 {
			_ = b.cfg.Writer.EnsureLogStream(writeCtx, b.cfg.Group, b.cfg.Stream)
		}
		err = b.cfg.Writer.WriteLogEvents(writeCtx, b.cfg.Group, b.cfg.Stream, entries)
		if err == nil {
			return true
		}
		if attempt == len(retryDelays)-1 {
			break
		}
		select {
		case <-b.cfg.Clock.After(retryDelays[attempt]):
		case <-writeCtx.Done():
			writeCtx = b.regionContext()
		}
	}
	b.cfg.Logger.Error("container logs: write events failed after retries",
		zap.String("group", b.cfg.Group),
		zap.String("stream", b.cfg.Stream),
		zap.Int("entries", len(entries)),
		zap.Error(err),
	)
	return false
}

// liveContext is the batcher's own context while it lives, and a fresh
// region-carrying one once it does not.
func (b *CloudWatchBatcher) liveContext() context.Context {
	if b.cfg.Context != nil && b.cfg.Context.Err() == nil {
		return b.cfg.Context
	}
	return b.regionContext()
}

// regionContext is a context that outlives whatever was cancelled, carrying the
// region the lines belong to.
func (b *CloudWatchBatcher) regionContext() context.Context {
	return middleware.ContextWithRegion(context.Background(), b.cfg.Region)
}

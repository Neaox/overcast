package containerlogs

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/middleware"
)

func newBatcher(writer events.LogWriter, ctx context.Context, clk clock.Clock) *CloudWatchBatcher {
	return NewCloudWatchBatcher(BatcherConfig{
		Writer:  writer,
		Group:   "/aws/lambda/fn",
		Stream:  "stream",
		Context: ctx,
		Region:  "us-east-1",
		Clock:   clk,
		Logger:  zap.NewNop(),
	})
}

func TestCloudWatchBatcher_retriesATransientFailure(t *testing.T) {
	// Given: a log writer that fails once, then succeeds.
	writer := &fakeLogWriter{failWrites: 1}
	b := newBatcher(writer, context.Background(), clock.New())

	// When: events are written durably.
	ok := b.Write(context.Background(), []events.LogEntry{{Timestamp: 1, Message: "hello"}})

	// Then: the transient error is retried after ensuring the stream.
	if !ok {
		t.Fatal("Write returned false")
	}
	if len(writer.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writer.writes))
	}
	if writer.ensureCalls == 0 {
		t.Fatal("EnsureLogStream was not called on retry")
	}
}

func TestCloudWatchBatcher_givesUpAfterTheBoundedRetry(t *testing.T) {
	// Given: a writer that fails more times than the retry allows.
	writer := &fakeLogWriter{failWrites: 10}
	b := newBatcher(writer, context.Background(), clock.New())

	// When: events are written.
	ok := b.Write(context.Background(), []events.LogEntry{{Timestamp: 1, Message: "hello"}})

	// Then: the write is reported as failed rather than retried forever.
	if ok {
		t.Fatal("Write returned true after every attempt failed")
	}
	if got := 10 - writer.failWrites; got != len(retryDelays) {
		t.Fatalf("attempted %d writes, want %d", got, len(retryDelays))
	}
}

// The batch a container is holding when it is torn down is the one most worth
// delivering — it is the output that explains why it died — and the context it
// was collected on is cancelled by that very teardown.
func TestCloudWatchBatcher_writesOnAFreshContextAfterCancellation(t *testing.T) {
	// Given: a batcher whose long-lived context has been cancelled.
	writer := &fakeLogWriter{}
	ctx, cancel := context.WithCancel(middleware.ContextWithRegion(context.Background(), "us-east-1"))
	cancel()
	b := newBatcher(writer, ctx, clock.New())

	// When: a batch is flushed.
	b.Line(Line{Time: time.UnixMilli(1700000000000), Message: "last words"})
	if n := b.Flush(); n != 1 {
		t.Fatalf("Flush = %d, want 1", n)
	}

	// Then: the write went out on a live context that still carries the region.
	if got := writer.allMessages(); len(got) != 1 || got[0] != "last words" {
		t.Fatalf("messages = %v, want [last words]", got)
	}
	used := writer.contexts[0]
	if used.Err() != nil {
		t.Fatal("the write used a cancelled context")
	}
	if region := middleware.RegionFromContext(used, ""); region != "us-east-1" {
		t.Fatalf("write context carried region %q, want us-east-1", region)
	}
}

func TestCloudWatchBatcher_usesTheDaemonsTimestamp(t *testing.T) {
	// Given: a line the daemon stamped, and a clock reading something else.
	writer := &fakeLogWriter{}
	mock := clock.NewMock()
	mock.Set(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	b := newBatcher(writer, context.Background(), mock)
	emitted := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)

	// When: it is batched and flushed.
	b.Line(Line{Time: emitted, Message: "stamped"})
	b.Flush()

	// Then: CloudWatch records when the container printed it, not when Overcast
	// got round to reading it.
	if got := writer.writes[0][0].Timestamp; got != emitted.UnixMilli() {
		t.Fatalf("timestamp = %d, want %d", got, emitted.UnixMilli())
	}
}

func TestCloudWatchBatcher_fallsBackToTheClockForAnUnstampedLine(t *testing.T) {
	// Given: a continuation line, which carries no timestamp of its own.
	writer := &fakeLogWriter{}
	mock := clock.NewMock()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.Set(now)
	b := newBatcher(writer, context.Background(), mock)

	// When: it is batched and flushed.
	b.Line(Line{Message: "continuation"})
	b.Flush()

	// Then: the injected clock supplies the time — never time.Now.
	if got := writer.writes[0][0].Timestamp; got != now.UnixMilli() {
		t.Fatalf("timestamp = %d, want %d", got, now.UnixMilli())
	}
}

func TestCloudWatchBatcher_flushClearsTheBatch(t *testing.T) {
	// Given: a batch that has been flushed.
	writer := &fakeLogWriter{}
	b := newBatcher(writer, context.Background(), clock.New())
	b.Line(Line{Message: "one"})
	b.Flush()

	// When: it is flushed again with nothing new.
	// Then: nothing is written twice, and the flush reports no lines.
	if n := b.Flush(); n != 0 {
		t.Fatalf("second Flush = %d, want 0", n)
	}
	if got := writer.batchSizes(); len(got) != 1 {
		t.Fatalf("batches written = %v, want one", got)
	}
}

// A batcher with nowhere to write is not an error — Lambda keeps a container's
// rolling tail buffer whether CloudWatch is wired up or not.
func TestCloudWatchBatcher_withoutAWriterAcceptsAndDiscards(t *testing.T) {
	b := newBatcher(nil, context.Background(), clock.New())
	if !b.Line(Line{Message: "nowhere to go"}) {
		t.Fatal("Line was refused")
	}
	if n := b.Flush(); n != 1 {
		t.Fatalf("Flush = %d, want 1", n)
	}
	if err := b.EnsureStream(context.Background()); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
}

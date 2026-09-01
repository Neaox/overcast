package pipes

// Source polling for the sources that are not pushed onto the event bus.
//
// DynamoDB Streams reach a pipe through the internal event bus, so they need no
// poller. Kinesis streams and SQS queues do: AWS's pipe "continuously polls for
// events from the source", at a base rate of once per second for streams.
//
// Shape of the implementation, and why:
//
//   - One goroutine for the whole service, not one per pipe. A goroutine per
//     pipe would outlive Stop for any pipe created after it, and a burst of
//     pipes would be a burst of goroutines for work that is trivially serial
//     at emulator scale. This mirrors the EventBridge schedule engine.
//   - Driven by the injected clock.Clock, never time.Now/time.Sleep, so a test
//     advances a mock clock instead of sleeping.
//   - Bounded and stoppable: the loop selects on stopCh, and Stop waits for it
//     to drain so no delivery is in flight when the process exits.
//   - Shard cursors live in memory. They are a consumer position, not emulated
//     AWS state, and rebuilding them from the stream on restart is exactly what
//     a real consumer with no checkpoint does.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/eventtarget"
)

// pollInterval is how often each polled source is read. Real EventBridge polls
// stream shards "at a base rate of once per second".
const pollInterval = time.Second

// Batch sizes AWS defaults to when the pipe does not set one.
const (
	defaultSQSBatchSize     = 10
	defaultKinesisBatchSize = 100
	// sqsVisibilitySeconds hides a received message while the pipe execution
	// runs. A failed execution leaves it to reappear, which is how AWS's
	// "messages become visible in the queue again" behaviour is reproduced.
	sqsVisibilitySeconds = 30
)

// cursors tracks the per-shard read position of every Kinesis source.
type cursors struct {
	mu  sync.Mutex
	pos map[string]string
}

func newCursors() *cursors { return &cursors{pos: make(map[string]string)} }

func (c *cursors) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.pos[key]
	return v, ok
}

func (c *cursors) set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pos[key] = value
}

// forget drops the cursors of pipes that no longer exist, so a long-lived
// emulator does not accumulate positions for deleted pipes.
func (c *cursors) forget(live map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.pos {
		if !live[cursorPipe(key)] {
			delete(c.pos, key)
		}
	}
}

// attemptCounts tracks how many consecutive delivery failures a Kinesis
// shard's current batch has seen.
//
// A Kinesis source has no inline retry loop the way executeStream gives a
// stream-bus source: each attempt is one poll tick apart, because the cursor
// is left where it was so the same batch is re-read next time. That means the
// attempt count has to survive between ticks, keyed the same way a shard's
// cursor is — reusing cursorKey/cursorPipe rather than inventing a second
// keying scheme.
type attemptCounts struct {
	mu sync.Mutex
	n  map[string]int
}

func newAttemptCounts() *attemptCounts { return &attemptCounts{n: make(map[string]int)} }

// increment records one more failed attempt at key and returns the new total.
func (a *attemptCounts) increment(key string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n[key]++
	return a.n[key]
}

// reset clears key's count, called once a batch is no longer pending — it was
// delivered, dead-lettered, or dropped.
func (a *attemptCounts) reset(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.n, key)
}

// forget drops the counts of pipes that no longer exist, mirroring
// cursors.forget.
func (a *attemptCounts) forget(live map[string]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key := range a.n {
		if !live[cursorPipe(key)] {
			delete(a.n, key)
		}
	}
}

// cursorKey identifies one shard of one pipe's source.
func cursorKey(region, pipe, shard string) string { return region + "/" + pipe + "\x00" + shard }

// cursorPipe returns the region/pipe part of a cursor key.
func cursorPipe(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i]
		}
	}
	return key
}

// startPolling launches the single source-polling loop. It is guarded by a
// sync.Once and does no work on the caller's goroutine, so it is safe to call
// from router construction (docs/dev/performance.md § Startup budget).
func (h *Handler) startPolling() {
	h.pollOnce.Do(func() {
		// Register the ticker before the goroutine starts: a mock clock only
		// fires timers that already exist when test time is advanced.
		ticker := h.clk.Ticker(pollInterval)
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer ticker.Stop()
			for {
				select {
				case <-h.stopCh:
					return
				case <-ticker.C:
					h.pollSources()
				}
			}
		}()
	})
}

// stopPolling signals the loop and waits for it to drain.
func (h *Handler) stopPolling(ctx context.Context) {
	h.stopOnce.Do(func() { close(h.stopCh) })
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// pollSources reads one batch from every RUNNING pipe whose source is polled.
func (h *Handler) pollSources() {
	ctx := context.Background()
	pipes, aerr := h.store.listAllPipes(ctx)
	if aerr != nil {
		h.log.Error("pipes: poll: list pipes failed", zap.String("error", aerr.Message))
		return
	}
	live := make(map[string]bool, len(pipes))
	for _, p := range pipes {
		region := regionOrDefault(regionFromARN(p.SourceArn), h.cfg.Region)
		live[region+"/"+p.Name] = true
		if p.CurrentState != PipeStateRunning {
			continue
		}
		kind, err := classifySourceARN(p.SourceArn)
		if err != nil {
			continue // stored by an older build; the console reports it as inert
		}
		pipeCtx := regionContext(ctx, region)
		switch kind {
		case sourceKinesisStream:
			h.pollKinesis(pipeCtx, p, region)
		case sourceSQSQueue:
			h.pollSQS(pipeCtx, p)
		case sourceDynamoDBStream:
			// Delivered from the event bus; nothing to poll.
		}
	}
	h.cursors.forget(live)
	h.kinesisAttempts.forget(live)
}

// pollKinesis reads one batch from every shard of a Kinesis source.
func (h *Handler) pollKinesis(ctx context.Context, p *Pipe, region string) {
	if h.streams == nil {
		return
	}
	stream := resourceNameFromARN(p.SourceArn)
	shards, err := h.streams.ListStreamShards(ctx, stream)
	if err != nil {
		h.log.Warn("pipes: poll: list shards failed",
			zap.String("pipe", p.Name), zap.String("stream", stream), zap.Error(err))
		return
	}
	batchSize := defaultKinesisBatchSize
	if sp := p.SourceParameters; sp != nil && sp.KinesisStreamParameters != nil && sp.KinesisStreamParameters.BatchSize > 0 {
		batchSize = sp.KinesisStreamParameters.BatchSize
	}

	for _, shard := range shards {
		key := cursorKey(region, p.Name, shard)
		after, seen := h.cursors.get(key)
		if !seen && startsAtLatest(p) {
			// LATEST skips whatever is already on the shard. Fast-forward to
			// the tip once, then read forward from there like any other shard.
			if tip, err := h.shardTip(ctx, stream, shard); err == nil {
				h.cursors.set(key, tip)
				continue
			}
		}
		records, next, err := h.streams.ReceiveStreamRecords(ctx, stream, shard, after, batchSize)
		if err != nil {
			h.log.Warn("pipes: poll: read shard failed",
				zap.String("pipe", p.Name), zap.String("shard", shard), zap.Error(err))
			continue
		}
		if len(records) == 0 {
			h.cursors.set(key, next)
			continue
		}
		batch := make([]map[string]any, 0, len(records))
		for _, rec := range records {
			batch = append(batch, kinesisRecord(p.SourceArn, p.RoleArn, rec))
		}
		failures, err := h.execute(ctx, p, batch)
		if err != nil || failures.retryAll {
			// Leave the cursor where it was so the batch is retried, matching
			// a stream consumer that does not checkpoint a failed batch — until
			// the source's MaximumRetryAttempts is exhausted, at which point
			// the batch is dead-lettered and the cursor advances past it
			// rather than retrying forever.
			h.kinesisRetryOrDeadLetter(ctx, p, key, batch, next, err, failures.retryAll)
			continue
		}
		h.kinesisAttempts.reset(key)
		if failures.reported {
			// A partial-batch report checkpoints the shard just before the
			// earliest record the target named, so the retry resumes there.
			// Naming the first record of the batch leaves the cursor alone,
			// which is the same as not checkpointing at all.
			if failures.firstIndex > 0 {
				h.cursors.set(key, records[failures.firstIndex-1].SequenceNumber)
			}
			continue
		}
		h.cursors.set(key, next)
	}
}

// kinesisRetryOrDeadLetter counts one more failed attempt at a shard's current
// batch and, once the source's MaximumRetryAttempts is exhausted, dead-letters
// the batch and advances the cursor past it rather than retrying forever.
//
// Until issue #513, a Kinesis source's MaximumRetryAttempts and
// DeadLetterConfig were accepted and echoed but never read: a batch that never
// succeeds was retried on every poll tick, indefinitely, and had nowhere else
// to go. This gives Kinesis the same retry-then-dead-letter contract
// executeStream already gives a DynamoDB Streams source (issue #582) — the
// same StreamSourceParameters shape, the same maxStreamDeliveryAttempts
// ceiling — adapted to a cursor-based source where one attempt is one poll
// tick apart rather than an inline loop, so the count has to be kept between
// calls instead of in a local variable.
func (h *Handler) kinesisRetryOrDeadLetter(ctx context.Context, p *Pipe, key string, batch []map[string]any, next string, deliverErr error, retryAllReported bool) {
	log := h.log.WithRecorder(ctx)
	sp := kinesisSourceParameters(p)
	attempts := h.kinesisAttempts.increment(key)
	if attempts < streamDeliveryAttempts(sp) {
		return // leave the cursor; the next poll tick retries
	}

	rec := deliveryRecord{
		Region:     regionOrDefault(regionFromARN(p.SourceArn), h.store.region(ctx)),
		Pipe:       p.Name,
		SourceARN:  p.SourceArn,
		TargetARN:  p.TargetArn,
		TargetType: eventtarget.DisplayName(targetKindOf(p)),
		Records:    len(batch),
		Attempts:   attempts,
		Outcome:    outcomeFailed,
	}
	switch {
	case deliverErr != nil:
		rec.Error = deliverErr.Error()
	case retryAllReported:
		rec.Error = "the target's partial-batch failure report could not be honoured"
	}

	dlq := streamDeadLetterARN(sp)
	switch {
	case dlq == "":
		log.Warn("pipes: kinesis batch dropped after retries — configure DeadLetterConfig to keep it",
			zap.String("pipe", p.Name), zap.String("target", p.TargetArn),
			zap.Int("attempts", attempts), zap.Error(deliverErr))
	default:
		if dlqErr := h.deadLetter(ctx, dlq, batch); dlqErr != nil {
			rec.Error = fmt.Sprintf("%s; dead-letter delivery also failed: %v", rec.Error, dlqErr)
			log.Warn("pipes: dead-letter delivery failed",
				zap.String("pipe", p.Name), zap.String("dlq", dlq), zap.Error(dlqErr))
		} else {
			rec.Outcome = outcomeDLQ
			log.Warn("pipes: kinesis batch dead-lettered",
				zap.String("pipe", p.Name), zap.String("target", p.TargetArn),
				zap.String("dlq", dlq), zap.Int("attempts", attempts))
		}
	}
	h.recordDelivery(rec)
	// The poison batch is done with either way: dead-lettered, or reported as
	// failed with nowhere to go. Advancing past it is what keeps the shard
	// from being blocked on it forever, the same trade AWS documents for its
	// own stream sources once retries are exhausted.
	h.cursors.set(key, next)
	h.kinesisAttempts.reset(key)
}

// shardTip returns the sequence number of the last record currently on a shard,
// or "" for an empty shard.
func (h *Handler) shardTip(ctx context.Context, stream, shard string) (string, error) {
	after := ""
	for {
		records, next, err := h.streams.ReceiveStreamRecords(ctx, stream, shard, after, defaultKinesisBatchSize)
		if err != nil {
			return "", err
		}
		if len(records) == 0 {
			return next, nil
		}
		after = next
	}
}

// startsAtLatest reports whether the pipe asked to start at the tip of the
// stream. AWS's default for a Kinesis source is TRIM_HORIZON.
func startsAtLatest(p *Pipe) bool {
	sp := p.SourceParameters
	return sp != nil && sp.KinesisStreamParameters != nil && sp.KinesisStreamParameters.StartingPosition == "LATEST"
}

// pollSQS reads one batch from an SQS source and deletes it once the batch has
// been delivered. A failed execution leaves the messages to become visible
// again, which is how AWS retries an SQS-sourced pipe.
func (h *Handler) pollSQS(ctx context.Context, p *Pipe) {
	if h.messages == nil {
		return
	}
	queue := resourceNameFromARN(p.SourceArn)
	batchSize := defaultSQSBatchSize
	if sp := p.SourceParameters; sp != nil && sp.SqsQueueParameters != nil && sp.SqsQueueParameters.BatchSize > 0 {
		batchSize = sp.SqsQueueParameters.BatchSize
	}
	msgs, err := h.messages.ReceiveMessages(ctx, queue, batchSize, sqsVisibilitySeconds)
	if err != nil {
		h.log.Warn("pipes: poll: receive failed",
			zap.String("pipe", p.Name), zap.String("queue", queue), zap.Error(err))
		return
	}
	if len(msgs) == 0 {
		return
	}
	batch := make([]map[string]any, 0, len(msgs))
	for _, msg := range msgs {
		batch = append(batch, sqsRecord(p.SourceArn, msg))
	}
	failures, err := h.execute(ctx, p, batch)
	if err != nil || failures.retryAll {
		return
	}
	// A message the target reported as failed is simply not deleted: it stays
	// in flight and SQS makes it visible again when the visibility timeout
	// expires, which is how AWS redelivers it.
	handles := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		if failures.failed[msg.MessageID] {
			continue
		}
		handles = append(handles, msg.ReceiptHandle)
	}
	if len(handles) == 0 {
		return
	}
	if err := h.messages.DeleteMessages(ctx, queue, handles); err != nil {
		h.log.Warn("pipes: poll: delete failed",
			zap.String("pipe", p.Name), zap.String("queue", queue), zap.Error(err))
	}
}

package lambda

// esm_delivery.go — SQS→Lambda and DynamoDB Streams→Lambda event delivery.
//
// When an EventSourceMapping is created or enabled, the esmDeliveryManager:
//   - SQS:              starts a polling goroutine (once/second, batch of BatchSize)
//   - DynamoDB Streams: registers a HandlerFunc on the shared event bus
//
// On success (no FunctionError), SQS messages are deleted from the queue.
// On failure:
//   - SQS: messages remain in the queue and become visible after the visibility
//     timeout expires — matching real SQS/Lambda behaviour. The queue's own
//     RedrivePolicy handles retries and DLQ movement.
//   - DynamoDB Streams: the event is retried up to MaximumRetryAttempts times.
//     After exhausting retries, if a DestinationConfig.OnFailure.Destination
//     is set, a failure record is sent to the destination SQS queue.
//
// Every goroutine respects context cancellation (ctx.Done). DynamoDB stream
// subscriptions are cancelled via the unsubscribe function returned by bus.Subscribe.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// esmDeliveryManager manages the lifecycle of ESM event delivery.
// Each enabled ESM has either a polling goroutine (SQS) or a bus subscription
// (DynamoDB Streams) tracked by UUID.
type esmDeliveryManager struct {
	store    *esmStore
	invoker  *ServiceInvoker
	receiver events.MessageReceiver // nil when SQS service is not available
	enqueuer events.MessageEnqueuer // nil when SQS service is not available
	bus      *events.Bus            // nil when event bus is not wired
	log      *serviceutil.ServiceLogger
	clk      clock.Clock
	cfg      *config.Config
	baseCtx  context.Context

	mu   sync.Mutex
	stop map[string]func() // keyed by ESM UUID; value cancels delivery
	wg   sync.WaitGroup
}

func newESMDeliveryManager(
	store *esmStore,
	invoker *ServiceInvoker,
	receiver events.MessageReceiver,
	enqueuer events.MessageEnqueuer,
	bus *events.Bus,
	log *serviceutil.ServiceLogger,
	clk clock.Clock,
	cfg *config.Config,
	baseCtx context.Context,
) *esmDeliveryManager {
	return &esmDeliveryManager{
		store:    store,
		invoker:  invoker,
		receiver: receiver,
		enqueuer: enqueuer,
		bus:      bus,
		log:      log,
		clk:      clk,
		cfg:      cfg,
		baseCtx:  baseCtx,
		stop:     make(map[string]func()),
	}
}

// Start begins event delivery for the given ESM.
// Idempotent: if delivery is already running for this UUID nothing happens.
// No-ops when the ESM state is not Enabled.
func (m *esmDeliveryManager) Start(esm *EventSourceMapping) {
	if esm.State != esmStateEnabled {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, running := m.stop[esm.UUID]; running {
		return
	}

	esmCopy := *esm // avoid closing over a live pointer

	switch {
	case isSQSArn(esm.EventSourceArn):
		ctx, cancel := context.WithCancel(m.baseCtx)
		m.stop[esm.UUID] = cancel
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer func() {
				m.mu.Lock()
				delete(m.stop, esmCopy.UUID)
				m.mu.Unlock()
			}()
			m.pollSQS(ctx, &esmCopy)
		}()

	case isDynamoDBStreamArn(esm.EventSourceArn):
		if m.bus == nil {
			m.log.Logger().Warn("lambda: esm dynamodb: no event bus — delivery skipped",
				zap.String("uuid", esm.UUID))
			return
		}
		// Use a cancellable context so Stop() terminates in-flight retries.
		ctx, cancel := context.WithCancel(m.baseCtx)
		// Subscribe to all three stream event types.
		unsub1 := m.bus.Subscribe(events.DynamoDBStreamInsert, m.makeStreamHandler(ctx, &esmCopy))
		unsub2 := m.bus.Subscribe(events.DynamoDBStreamModify, m.makeStreamHandler(ctx, &esmCopy))
		unsub3 := m.bus.Subscribe(events.DynamoDBStreamRemove, m.makeStreamHandler(ctx, &esmCopy))
		m.stop[esm.UUID] = func() {
			cancel()
			unsub1()
			unsub2()
			unsub3()
		}

	default:
		m.log.Logger().Warn("lambda: esm: unsupported event source",
			zap.String("uuid", esm.UUID),
			zap.String("arn", esm.EventSourceArn))
	}
}

// Stop cancels delivery for the given UUID. No-op if not running.
func (m *esmDeliveryManager) Stop(uuid string) {
	m.mu.Lock()
	fn, ok := m.stop[uuid]
	if ok {
		delete(m.stop, uuid)
	}
	m.mu.Unlock()
	if ok {
		fn()
	}
}

// StopAll cancels all running deliveries and waits for SQS pollers to finish.
// Called from Service.Stop.
func (m *esmDeliveryManager) StopAll() {
	m.mu.Lock()
	for uuid, fn := range m.stop {
		fn()
		delete(m.stop, uuid)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// ReloadAll starts delivery for every Enabled ESM found in the store.
// Called once after service startup to resume delivery across restarts.
// Scans all regions so ESMs created in non-default regions are not missed.
func (m *esmDeliveryManager) ReloadAll(ctx context.Context) {
	// If the backing store has an asynchronous initialisation phase (e.g.
	// HybridStore opening SQLite in the background), wait for it before
	// scanning. Without this, Scan falls back to the empty in-memory store
	// and persisted ESMs are silently skipped — their pollers never start.
	if w, ok := m.store.s.store.(state.ReadyAwaiter); ok {
		if err := w.WaitReady(ctx); err != nil {
			m.log.Logger().Warn("lambda: esm reload: store not ready", zap.Error(err))
			return
		}
	}
	mappings, aerr := m.store.listAllESMs(ctx)
	if aerr != nil {
		m.log.Logger().Warn("lambda: esm reload failed", zap.String("error", aerr.Message))
		return
	}
	for _, esm := range mappings {
		if esm.State == esmStateEnabled {
			m.Start(esm)
		}
	}
}

// ---- SQS polling -----------------------------------------------------------

// pollSQS blocks, polling once per second, until ctx is cancelled.
func (m *esmDeliveryManager) pollSQS(ctx context.Context, esm *EventSourceMapping) {
	if m.receiver == nil {
		m.log.Logger().Warn("lambda: esm sqs: receiver not wired — delivery skipped",
			zap.String("uuid", esm.UUID))
		return
	}

	// Derive a region-scoped context from the function ARN. Real AWS requires
	// the SQS queue and Lambda function to be in the same region, so a single
	// region context covers all operations: ESM store reads, SQS receive/delete,
	// and Lambda invoke. This mirrors the pattern in makeStreamHandler.
	ctx = middleware.ContextWithRegion(ctx, m.regionFromARN(esm.FunctionArn))

	queueName := queueNameFromARN(esm.EventSourceArn)
	funcName := functionNameFromARN(esm.FunctionArn)
	ticker := m.clk.Ticker(time.Second)
	defer ticker.Stop()

	// sem is a counting semaphore that caps concurrent Lambda invocations.
	// A nil sem means unlimited concurrency (no ScalingConfig set).
	var sem chan struct{}
	if esm.ScalingConfig != nil && esm.ScalingConfig.MaximumConcurrency > 0 {
		sem = make(chan struct{}, esm.ScalingConfig.MaximumConcurrency)
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Re-read ESM from store each tick to detect disable/delete.
		// A transient store error (e.g. SQLite lock contention on a mounted
		// volume) must not terminate the loop permanently — log and retry on
		// the next tick. Only exit when the ESM is genuinely gone or disabled.
		current, aerr := m.store.getESM(ctx, esm.UUID)
		if aerr != nil {
			m.log.Logger().Warn("lambda: esm sqs: store read error — skipping tick",
				zap.String("uuid", esm.UUID),
				zap.String("error", aerr.Message))
			continue
		}
		if current == nil || current.State != esmStateEnabled {
			return
		}

		// Always receive messages first — this mirrors real AWS behaviour where
		// the poller always consumes messages from SQS regardless of concurrency.
		msgs, err := m.receiver.ReceiveMessages(ctx, queueName, esm.BatchSize, 30)
		if err != nil {
			m.log.Logger().Warn("lambda: esm sqs: receive error",
				zap.String("queue", queueName),
				zap.Error(err))
			continue
		}
		if len(msgs) == 0 {
			continue
		}

		// Apply filter criteria: non-matching SQS messages are immediately
		// deleted from the queue (per AWS spec: Lambda removes them rather
		// than leaving them to expire and retry).
		msgs = m.filterAndDeleteSQS(ctx, current, queueName, msgs)
		if len(msgs) == 0 {
			continue
		}

		// Emit before spawning the goroutine so the stream shows the trigger
		// before any Lambda instance events that follow.
		m.publishESMInvoked(ctx, current, "", len(msgs))

		// If a MaximumConcurrency limit is set, try to acquire a slot.
		// When all slots are occupied the invoke is throttled: messages have
		// already been received (they are now in-flight / invisible in SQS)
		// and will return to the queue after the visibility timeout expires.
		// This matches real AWS throttling behaviour.
		if sem != nil {
			select {
			case sem <- struct{}{}: // acquired a slot
			default:
				// All concurrency slots are taken — throttled.
				// Messages were received but cannot be processed; they stay
				// in-flight and will become visible again after the visibility
				// timeout, just like real AWS.
				m.updateLastResult(ctx, esm.UUID, "Throttled")
				m.log.Logger().Debug("lambda: esm sqs: throttled — max concurrency reached",
					zap.String("uuid", esm.UUID),
					zap.Int("maxConcurrency", current.ScalingConfig.MaximumConcurrency))
				continue
			}
		}

		payload, err := m.buildSQSEvent(esm, msgs)
		if err != nil {
			if sem != nil {
				<-sem
			}
			m.log.Logger().Error("lambda: esm sqs: build event failed", zap.Error(err))
			continue
		}

		// Capture loop variables for the goroutine closure.
		batchMsgs := msgs
		batchPayload := payload
		esmCopy := *current

		wg.Add(1)
		go func() {
			defer wg.Done()
			if sem != nil {
				defer func() { <-sem }()
			}

			outcome, err := m.invoker.Invoke(ctx, funcName, batchPayload)
			if err != nil {
				m.log.Logger().Error("lambda: esm sqs: invoke error",
					zap.String("function", funcName),
					zap.Error(err))
				m.updateLastResult(ctx, esmCopy.UUID, "PROBLEM - Lambda returned an error")
				return
			}
			if outcome == nil {
				// No runtime or function not found — skip silently.
				return
			}
			if outcome.FunctionError != "" {
				m.log.Logger().Warn("lambda: esm sqs: function returned error",
					zap.String("function", funcName),
					zap.String("function_error", outcome.FunctionError))
				m.updateLastResult(ctx, esmCopy.UUID, "PROBLEM - Function returned an error")
				return // leave messages in queue
			}

			handles := make([]string, len(batchMsgs))
			for i, msg := range batchMsgs {
				handles[i] = msg.ReceiptHandle
			}
			if err := m.receiver.DeleteMessages(ctx, queueName, handles); err != nil {
				m.log.Logger().Warn("lambda: esm sqs: delete error",
					zap.String("queue", queueName),
					zap.Error(err))
			}
			m.updateLastResult(ctx, esmCopy.UUID, "OK")
		}()
	}
}

// filterAndDeleteSQS evaluates filter criteria against each message in msgs.
// Non-matching messages are immediately deleted from the queue per the AWS
// specification (Lambda removes them rather than returning them to visibility).
// Returns only the messages that matched the criteria; returns msgs unchanged
// when FilterCriteria is nil or empty (pass-through).
func (m *esmDeliveryManager) filterAndDeleteSQS(ctx context.Context, esm *EventSourceMapping, queueName string, msgs []events.ReceivedMessage) []events.ReceivedMessage {
	if esm.FilterCriteria == nil || len(esm.FilterCriteria.Filters) == 0 {
		return msgs
	}
	sourceRegion := m.regionFromARN(esm.EventSourceArn)
	var matching []events.ReceivedMessage
	var toDelete []string
	for _, msg := range msgs {
		// Convert attributes to map[string]any so that nested filter patterns
		// (e.g. {"attributes": {"ApproximateReceiveCount": ["5"]}}) can drill
		// into the map via toEventMap. map[string]string is not coercible to
		// map[string]any by Go's type system, so we must convert explicitly.
		attrsAny := make(map[string]any, len(msg.Attributes))
		for k, v := range msg.Attributes {
			attrsAny[k] = v
		}
		record := map[string]any{
			"messageId":         msg.MessageID,
			"receiptHandle":     msg.ReceiptHandle,
			"body":              msg.Body,
			"attributes":        attrsAny,
			"messageAttributes": map[string]any{},
			"md5OfBody":         msg.MD5OfBody,
			"eventSource":       "aws:sqs",
			"eventSourceARN":    esm.EventSourceArn,
			"awsRegion":         sourceRegion,
		}
		if matchesFilterCriteria(esm.FilterCriteria, record) {
			matching = append(matching, msg)
		} else {
			toDelete = append(toDelete, msg.ReceiptHandle)
		}
	}
	if len(toDelete) > 0 {
		m.publishESMFiltered(ctx, esm, "", len(toDelete), esm.FilterCriteria)
		if err := m.receiver.DeleteMessages(ctx, queueName, toDelete); err != nil {
			m.log.Logger().Warn("lambda: esm sqs: delete filtered messages error",
				zap.String("queue", queueName),
				zap.Error(err))
		}
	}
	return matching
}

// buildSQSEvent creates the Lambda SQS trigger payload for a batch of messages.
func (m *esmDeliveryManager) buildSQSEvent(esm *EventSourceMapping, msgs []events.ReceivedMessage) ([]byte, error) {
	type sqsRecord struct {
		MessageID         string            `json:"messageId"`
		ReceiptHandle     string            `json:"receiptHandle"`
		Body              string            `json:"body"`
		Attributes        map[string]string `json:"attributes"`
		MessageAttributes map[string]any    `json:"messageAttributes"`
		MD5OfBody         string            `json:"md5OfBody"`
		EventSource       string            `json:"eventSource"`
		EventSourceARN    string            `json:"eventSourceARN"`
		AWSRegion         string            `json:"awsRegion"`
	}

	records := make([]sqsRecord, len(msgs))
	// AWS event-record AWSRegion reflects the source queue's region (encoded
	// in the EventSourceArn), not the function's region or the emulator default.
	sourceRegion := m.regionFromARN(esm.EventSourceArn)
	for i, msg := range msgs {
		attrs := msg.Attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		records[i] = sqsRecord{
			MessageID:         msg.MessageID,
			ReceiptHandle:     msg.ReceiptHandle,
			Body:              msg.Body,
			Attributes:        attrs,
			MessageAttributes: map[string]any{},
			MD5OfBody:         msg.MD5OfBody,
			EventSource:       "aws:sqs",
			EventSourceARN:    esm.EventSourceArn,
			AWSRegion:         sourceRegion,
		}
	}
	return json.Marshal(map[string]any{"Records": records})
}

// ---- DynamoDB Streams delivery ---------------------------------------------

// makeStreamHandler returns a HandlerFunc that delivers stream records for
// the given ESM. Each subscription call gets its own closure over esmCopy.
// The handler dispatches invocation (which may involve cold starts and retries)
// onto a separate goroutine so bus workers are never blocked.
func (m *esmDeliveryManager) makeStreamHandler(ctx context.Context, esm *EventSourceMapping) events.HandlerFunc {
	// Pre-compute region context once (immutable per ESM).
	region := m.regionFromARN(esm.FunctionArn)
	regionCtx := middleware.ContextWithRegion(ctx, region)
	tableName := tableNameFromStreamARN(esm.EventSourceArn)

	return func(_ context.Context, evt events.Event) {
		payload, ok := evt.Payload.(events.DynamoDBStreamPayload)
		if !ok {
			return
		}
		// Match by table name: the ESM EventSourceArn ends with /table/NAME/stream/...
		if !strings.EqualFold(tableName, payload.Table) {
			return
		}

		// Dispatch the (potentially slow) invoke+retry loop off the bus
		// worker pool so we don't starve other event deliveries.
		m.mu.Lock()
		if _, running := m.stop[esm.UUID]; !running {
			m.mu.Unlock()
			return
		}
		m.wg.Add(1)
		m.mu.Unlock()
		go func() {
			defer m.wg.Done()
			m.deliverStreamRecord(regionCtx, esm, payload, evt)
		}()
	}
}

// deliverStreamRecord performs the actual Lambda invocation with retries for
// a single DynamoDB stream record. Runs on its own goroutine.
func (m *esmDeliveryManager) deliverStreamRecord(ctx context.Context, esm *EventSourceMapping, payload events.DynamoDBStreamPayload, evt events.Event) {
	// Re-check ESM state before invoking.
	current, aerr := m.store.getESM(ctx, esm.UUID)
	if aerr != nil || current == nil || current.State != esmStateEnabled {
		return
	}

	funcName := functionNameFromARN(esm.FunctionArn)
	record := m.buildDynamoDBRecord(esm.EventSourceArn, evt, payload)

	// Apply filter criteria before invoking. Non-matching records are silently
	// dropped — the DynamoDB Streams iterator advances past them (per AWS spec).
	if !matchesFilterCriteria(current.FilterCriteria, record) {
		if current.FilterCriteria != nil && len(current.FilterCriteria.Filters) > 0 {
			m.publishESMFiltered(ctx, current, payload.EventName, 1, current.FilterCriteria)
		}
		return
	}

	m.publishESMInvoked(ctx, current, payload.EventName, 1)

	lambdaPayload, err := json.Marshal(map[string]any{"Records": []any{record}})
	if err != nil {
		m.log.Logger().Error("lambda: esm dynamodb: marshal failed", zap.Error(err))
		return
	}

	// Determine max retry attempts. Default: -1 (unlimited) for streams,
	// but we cap unlimited at a reasonable 10000 to prevent infinite loops
	// in the emulator. A value of 0 means no retries (invoke once).
	maxRetries := -1
	if current.MaximumRetryAttempts != nil {
		maxRetries = *current.MaximumRetryAttempts
	}
	if maxRetries < 0 {
		maxRetries = 10000
	}

	var lastErr string
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-m.clk.After(time.Second):
			case <-ctx.Done():
				return
			}
		}
		outcome, err := m.invoker.Invoke(ctx, funcName, lambdaPayload)
		if err != nil {
			lastErr = err.Error()
			m.log.Logger().Error("lambda: esm dynamodb: invoke error",
				zap.String("function", funcName),
				zap.Int("attempt", attempt),
				zap.Error(err))
			m.updateLastResult(ctx, esm.UUID, "PROBLEM - Lambda returned an error")
			continue
		}
		if outcome == nil {
			return
		}
		if outcome.FunctionError == "" {
			m.updateLastResult(ctx, esm.UUID, "OK")
			return // success
		}

		lastErr = outcome.FunctionError
		m.log.Logger().Warn("lambda: esm dynamodb: function error",
			zap.String("function", funcName),
			zap.String("error", outcome.FunctionError),
			zap.Int("attempt", attempt))
		m.updateLastResult(ctx, esm.UUID, "PROBLEM - Function returned an error")
	}

	// Exhausted all retries — send to on-failure destination if configured.
	m.sendOnFailure(ctx, current, payload, funcName, lastErr)
}

// buildDynamoDBRecord formats a DynamoDB stream payload into the standard
// Lambda event record format (same format used by Pipes and EventBridge).
func (m *esmDeliveryManager) buildDynamoDBRecord(sourceARN string, _ events.Event, payload events.DynamoDBStreamPayload) map[string]any {
	seqNum := fmt.Sprintf("%021d", payload.SequenceNumber)
	region := m.regionFromARN(sourceARN)

	return map[string]any{
		"eventID":        seqNum,
		"eventVersion":   "1.1",
		"eventSource":    "aws:dynamodb",
		"eventSourceARN": sourceARN,
		"awsRegion":      region,
		"eventName":      payload.EventName,
		"dynamodb": map[string]any{
			"ApproximateCreationDateTime": float64(payload.CreatedAt) / 1000.0,
			"Keys":                        payload.Keys,
			"NewImage":                    payload.NewImage,
			"OldImage":                    payload.OldImage,
			"SequenceNumber":              seqNum,
			"StreamViewType":              "NEW_AND_OLD_IMAGES",
		},
	}
}

// ---- Helpers ---------------------------------------------------------------

// sendOnFailure delivers a failure record to the ESM's on-failure destination
// (if configured). Currently only SQS destinations are supported, matching
// the most common real-world pattern.
func (m *esmDeliveryManager) sendOnFailure(ctx context.Context, esm *EventSourceMapping, payload events.DynamoDBStreamPayload, funcName, lastErr string) {
	if esm.DestinationConfig == nil || esm.DestinationConfig.OnFailure == nil {
		return
	}
	destARN := esm.DestinationConfig.OnFailure.Destination
	if destARN == "" {
		return
	}

	// Only SQS destinations are supported in the emulator.
	if !isSQSArn(destARN) {
		m.log.Logger().Warn("lambda: esm on-failure: unsupported destination type (only SQS is supported)",
			zap.String("destination", destARN))
		return
	}
	if m.enqueuer == nil {
		m.log.Logger().Warn("lambda: esm on-failure: SQS enqueuer not available")
		return
	}

	dlqName := queueNameFromARN(destARN)

	// Build an AWS-compatible invocation failure record.
	// See: https://docs.aws.amazon.com/lambda/latest/dg/invocation-async.html#invocation-async-destinations
	failureRecord := map[string]any{
		"requestContext": map[string]any{
			"requestId":              fmt.Sprintf("%d", payload.SequenceNumber),
			"functionArn":            esm.FunctionArn,
			"condition":              "RetriesExhausted",
			"approximateInvokeCount": m.effectiveMaxRetries(esm) + 1,
		},
		"responseContext": map[string]any{
			"statusCode":      200,
			"executedVersion": "$LATEST",
			"functionError":   lastErr,
		},
		"version":   "1.0",
		"timestamp": m.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"requestPayload": map[string]any{
			"eventSourceArn": esm.EventSourceArn,
			"table":          payload.Table,
			"eventName":      payload.EventName,
			"sequenceNumber": fmt.Sprintf("%021d", payload.SequenceNumber),
		},
	}

	body, err := json.Marshal(failureRecord)
	if err != nil {
		m.log.Logger().Error("lambda: esm on-failure: marshal failed", zap.Error(err))
		return
	}

	if err := m.enqueuer.EnqueueRaw(ctx, dlqName, string(body)); err != nil {
		m.log.Logger().Error("lambda: esm on-failure: enqueue failed",
			zap.String("queue", dlqName),
			zap.Error(err))
	} else {
		m.log.Logger().Info("lambda: esm on-failure: failure record sent to DLQ",
			zap.String("queue", dlqName),
			zap.String("function", funcName))
	}
}

// effectiveMaxRetries returns the max retry count for an ESM, treating nil/-1 as 10000.
func (m *esmDeliveryManager) effectiveMaxRetries(esm *EventSourceMapping) int {
	if esm.MaximumRetryAttempts == nil || *esm.MaximumRetryAttempts < 0 {
		return 10000
	}
	return *esm.MaximumRetryAttempts
}

func (m *esmDeliveryManager) updateLastResult(ctx context.Context, uuid, result string) {
	esm, aerr := m.store.getESM(ctx, uuid)
	if aerr != nil || esm == nil {
		return
	}
	esm.LastProcessingResult = result
	_ = m.store.putESM(ctx, esm)
}

func (m *esmDeliveryManager) regionFromARN(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) >= 5 {
		return parts[3]
	}
	return m.cfg.Region
}

// isSQSArn reports whether the ARN belongs to an SQS queue
// (arn:aws:sqs:region:account:name).
func isSQSArn(arn string) bool {
	return strings.Contains(strings.ToLower(arn), ":sqs:")
}

// isDynamoDBStreamArn reports whether the ARN belongs to a DynamoDB stream.
func isDynamoDBStreamArn(arn string) bool {
	lower := strings.ToLower(arn)
	return strings.Contains(lower, ":dynamodb:") && strings.Contains(lower, "/stream/")
}

// queueNameFromARN extracts the queue name from an SQS ARN
// (arn:aws:sqs:us-east-1:000000000000:my-queue → "my-queue").
func queueNameFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 1 {
		return arn
	}
	return parts[len(parts)-1]
}

// tableNameFromStreamARN extracts the table name from a DynamoDB stream ARN:
// arn:aws:dynamodb:us-east-1:000000000000:table/MyTable/stream/2024-01-01 → "MyTable".
func tableNameFromStreamARN(arn string) string {
	// The resource portion (after the 5th colon) is "table/NAME/stream/LABEL".
	// Find "table/" in the ARN and extract the table name that follows.
	idx := strings.Index(arn, "table/")
	if idx < 0 {
		return arn
	}
	rest := arn[idx+len("table/"):]
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// esmSourceName returns the bare resource name from an ESM event source ARN
// (queue name for SQS, table name for DynamoDB) to match topology node IDs.
func esmSourceName(arn string) string {
	if isSQSArn(arn) {
		return queueNameFromARN(arn)
	}
	if isDynamoDBStreamArn(arn) {
		return tableNameFromStreamARN(arn)
	}
	return arn
}

// esmSourceType returns "sqs" or "dynamodb" for known ESM source ARN types.
func esmSourceType(arn string) string {
	if isSQSArn(arn) {
		return "sqs"
	}
	if isDynamoDBStreamArn(arn) {
		return "dynamodb"
	}
	return "unknown"
}

// publishESMFiltered emits a LambdaESMRecordFiltered event onto the bus.
// count is the number of records/messages that were dropped.
// fc is the criteria that rejected them; its patterns are included in the
// payload so subscribers can display the reason without further API calls.
func (m *esmDeliveryManager) publishESMFiltered(ctx context.Context, esm *EventSourceMapping, eventName string, count int, fc *FilterCriteria) {
	if m.bus == nil {
		return
	}
	patterns := make([]string, len(fc.Filters))
	for i, f := range fc.Filters {
		patterns[i] = f.Pattern
	}
	m.bus.Publish(ctx, events.Event{
		Type:   events.LambdaESMRecordFiltered,
		Time:   m.clk.Now(),
		Source: "lambda",
		Payload: events.LambdaESMEventPayload{
			ESMID:          esm.UUID,
			FunctionName:   functionNameFromARN(esm.FunctionArn),
			EventSource:    esmSourceName(esm.EventSourceArn),
			SourceType:     esmSourceType(esm.EventSourceArn),
			EventName:      eventName,
			RecordCount:    count,
			FilterPatterns: patterns,
		},
	})
}

// publishESMInvoked emits a LambdaESMInvoked event onto the bus.
// count is the number of records/messages being delivered.
func (m *esmDeliveryManager) publishESMInvoked(ctx context.Context, esm *EventSourceMapping, eventName string, count int) {
	if m.bus == nil {
		return
	}
	m.bus.Publish(ctx, events.Event{
		Type:   events.LambdaESMInvoked,
		Time:   m.clk.Now(),
		Source: "lambda",
		Payload: events.LambdaESMEventPayload{
			ESMID:        esm.UUID,
			FunctionName: functionNameFromARN(esm.FunctionArn),
			EventSource:  esmSourceName(esm.EventSourceArn),
			SourceType:   esmSourceType(esm.EventSourceArn),
			EventName:    eventName,
			RecordCount:  count,
		},
	})
}

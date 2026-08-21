package pipes

// One pipe execution: source records → optional enrichment → target.
//
// The stages and their order are AWS's (see
// https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-pipes.html):
//
//  1. the source produces a batch of records, always a JSON array — real Pipes
//     "will use the batch API for the supported enrichment or target even if
//     the batch size is 1";
//  2. the enrichment input template, if any, is applied to each record;
//  3. the enrichment is invoked synchronously and its return value *replaces*
//     the batch — an empty response ("", {}, []) drops it without invoking the
//     target;
//  4. the target input template, if any, is applied to each record of what came
//     back;
//  5. the batch is delivered through internal/eventtarget, the dispatch seam
//     issue #467 built for EventBridge rule targets.
//
// Delivery outcomes go into a fixed-size in-process ring, not state.Store: this
// is diagnostic data with no durability requirement on a per-batch path, the
// same reasoning as internal/services/eventbridge/deliveries.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/partialbatch"
)

// execute runs one pipe execution for a batch of source records and records the
// outcome.
//
// It returns an error when the batch was not delivered, so a polling source can
// leave the records for redelivery rather than acknowledging them. A stream
// source has nothing to leave behind and goes through executeStream instead.
func (h *Handler) execute(ctx context.Context, p *Pipe, records []map[string]any) (batchFailures, error) {
	rec, failures, err := h.attempt(ctx, p, records)
	if rec.Outcome == "" {
		return failures, err // empty batch: nothing ran, nothing to report
	}
	rec.Attempts = 1
	h.recordDelivery(rec)
	return failures, err
}

// attempt runs one pipe execution and returns the outcome it produced without
// recording it, so a caller that retries reports the sequence once rather than
// once per attempt. The returned record has a zero Outcome for an empty batch.
func (h *Handler) attempt(ctx context.Context, p *Pipe, records []map[string]any) (deliveryRecord, batchFailures, error) {
	log := h.log.WithRecorder(ctx)

	if len(records) == 0 {
		return deliveryRecord{}, batchFailures{}, nil
	}
	rec := deliveryRecord{
		Region:     regionOrDefault(regionFromARN(p.SourceArn), h.store.region(ctx)),
		Pipe:       p.Name,
		SourceARN:  p.SourceArn,
		TargetARN:  p.TargetArn,
		TargetType: eventtarget.DisplayName(targetKindOf(p)),
		Records:    len(records),
	}

	batch, drop, err := h.renderAndEnrich(ctx, p, records)
	switch {
	case err != nil:
		rec.Outcome = outcomeFailed
		rec.Error = err.Error()
		log.Warn("pipes: execution failed before delivery",
			zap.String("pipe", p.Name), zap.Error(err))
		return rec, batchFailures{}, err
	case drop:
		rec.Outcome = outcomeFiltered
		return rec, batchFailures{}, nil
	}

	response, err := h.dispatch(ctx, p, batch)
	if err != nil {
		rec.Outcome = outcomeFailed
		rec.Error = err.Error()
		log.Warn("pipes: target delivery failed",
			zap.String("pipe", p.Name), zap.String("target", p.TargetArn), zap.Error(err))
		return rec, batchFailures{}, err
	}
	rec.Outcome = outcomeDelivered
	h.publishDelivered(ctx, p, len(records))
	return rec, h.readBatchFailures(ctx, p, records, response), nil
}

// maxStreamDeliveryAttempts caps how many times one stream batch is attempted,
// however many retries the source asks for. AWS's default MaximumRetryAttempts
// for a stream source is -1 — "retry until the record expires", up to 24 hours
// — which an emulator running the delivery inline has nowhere to put. The same
// ceiling and the same reasoning as EventBridge's maxDeliveryAttempts.
const maxStreamDeliveryAttempts = 6

// executeStream delivers a batch that has nowhere to be redelivered from.
//
// An SQS source retries by leaving the message to become visible again and a
// Kinesis source by leaving its shard cursor where it was, so both can let one
// failed execution stand. A DynamoDB Streams record reaches the pipe through
// the internal event bus and is gone the moment the subscriber returns, so
// dropping a failed delivery loses it silently — the write that produced it has
// already answered its client. AWS covers exactly this with the stream source's
// MaximumRetryAttempts and DeadLetterConfig, which Overcast accepted, stored
// and echoed back without ever reading. This is where they are read.
func (h *Handler) executeStream(ctx context.Context, p *Pipe, records []map[string]any) {
	log := h.log.WithRecorder(ctx)

	sp := dynamoDBSourceParameters(p)
	attempts := streamDeliveryAttempts(sp)
	var rec deliveryRecord
	var err error
	// pending only ever shrinks, and only on a partial-batch report: a stream
	// resumes at the earliest record the target named, never past it.
	pending := records
	for attempt := 1; attempt <= attempts; attempt++ {
		var failures batchFailures
		rec, failures, err = h.attempt(ctx, p, pending)
		rec.Attempts = attempt
		if err != nil {
			continue
		}
		if !failures.reported && !failures.retryAll {
			if rec.Outcome != "" {
				h.recordDelivery(rec)
			}
			return
		}
		if failures.retryAll {
			err = errors.New("the target's partial-batch failure report could not be honoured")
		} else {
			err = fmt.Errorf("the target reported %d of %d records as failed", len(failures.failed), len(pending))
			pending = pending[failures.firstIndex:]
		}
		rec.Outcome = outcomeFailed
		rec.Error = err.Error()
	}

	dlq := streamDeadLetterARN(sp)
	if dlq == "" {
		log.Warn("pipes: stream batch dropped after retries — configure DeadLetterConfig to keep it",
			zap.String("pipe", p.Name), zap.String("target", p.TargetArn),
			zap.Int("attempts", rec.Attempts), zap.Error(err))
		h.recordDelivery(rec)
		return
	}
	if dlqErr := h.deadLetter(ctx, dlq, pending); dlqErr != nil {
		rec.Error = fmt.Sprintf("%v; dead-letter delivery also failed: %v", err, dlqErr)
		log.Warn("pipes: dead-letter delivery failed",
			zap.String("pipe", p.Name), zap.String("dlq", dlq), zap.Error(dlqErr))
		h.recordDelivery(rec)
		return
	}
	rec.Outcome = outcomeDLQ
	log.Warn("pipes: stream batch dead-lettered",
		zap.String("pipe", p.Name), zap.String("target", p.TargetArn),
		zap.String("dlq", dlq), zap.Int("attempts", rec.Attempts), zap.Error(err))
	h.recordDelivery(rec)
}

// dynamoDBSourceParameters returns the DynamoDB Streams parameter block, or
// nil when the pipe configured none.
func dynamoDBSourceParameters(p *Pipe) *StreamSourceParameters {
	if p.SourceParameters == nil {
		return nil
	}
	return p.SourceParameters.DynamoDBStreamParameters
}

// kinesisSourceParameters returns the Kinesis parameter block, or nil when the
// pipe configured none. Shares StreamSourceParameters with the DynamoDB source
// — same MaximumRetryAttempts/DeadLetterConfig shape, same AWS semantics —
// which is what lets pollKinesis reuse streamDeliveryAttempts and
// streamDeadLetterARN below rather than duplicating them.
func kinesisSourceParameters(p *Pipe) *StreamSourceParameters {
	if p.SourceParameters == nil {
		return nil
	}
	return p.SourceParameters.KinesisStreamParameters
}

// streamDeliveryAttempts returns how many times a stream batch is delivered in
// total: one, plus the source's MaximumRetryAttempts, capped at
// maxStreamDeliveryAttempts. A nil parameter block or an unset value takes
// AWS's default of retrying; an explicit 0 means one attempt and no more.
func streamDeliveryAttempts(sp *StreamSourceParameters) int {
	if sp == nil || sp.MaximumRetryAttempts == nil {
		return maxStreamDeliveryAttempts
	}
	retries := *sp.MaximumRetryAttempts
	if retries < 0 {
		return maxStreamDeliveryAttempts
	}
	attempts := retries + 1
	if attempts > maxStreamDeliveryAttempts {
		return maxStreamDeliveryAttempts
	}
	return attempts
}

// streamDeadLetterARN returns the source's dead-letter target, if any.
func streamDeadLetterARN(sp *StreamSourceParameters) string {
	if sp == nil || sp.DeadLetterConfig == nil {
		return ""
	}
	return strings.TrimSpace(sp.DeadLetterConfig.Arn)
}

// deadLetter parks an undeliverable batch on the source's dead-letter target.
//
// AWS's own dead-letter message for a stream source is a failure envelope
// naming the shard and sequence range rather than the records themselves —
// shard bookkeeping this emulator does not keep, because a DynamoDB-sourced
// pipe here is driven by the event bus rather than by a shard iterator. The
// records are sent instead, which is what makes the batch replayable and
// matches how EventBridge rule targets dead-letter here.
func (h *Handler) deadLetter(ctx context.Context, arn string, records []map[string]any) error {
	dispatcher := h.dispatcher()
	if dispatcher == nil {
		return fmt.Errorf("target router is not configured")
	}
	kind, err := eventtarget.Classify(arn)
	if err != nil {
		return err
	}
	if kind != eventtarget.KindSQS && kind != eventtarget.KindSNS {
		return fmt.Errorf("dead-letter target %s must be an SQS queue or an SNS topic", arn)
	}
	payload, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return dispatcher.Deliver(ctx, eventtarget.Request{ARN: arn, Kind: kind, Payload: payload})
}

// renderAndEnrich runs stages 2 to 4: the enrichment template, the enrichment
// itself, and the target template. drop is true when the enrichment asked for
// the target not to be invoked.
func (h *Handler) renderAndEnrich(ctx context.Context, p *Pipe, records []map[string]any) (batch []byte, drop bool, err error) {
	source := records
	if p.EnrichmentParameters != nil && p.EnrichmentParameters.InputTemplate != "" {
		if source, err = h.renderTemplate(p, records, p.EnrichmentParameters.InputTemplate); err != nil {
			return nil, false, fmt.Errorf("EnrichmentParameters.InputTemplate: %w", err)
		}
	}
	if batch, err = json.Marshal(source); err != nil {
		return nil, false, err
	}

	if batch, drop, err = h.enrich(ctx, p, batch); err != nil || drop {
		return nil, drop, err
	}

	if p.TargetParameters != nil && p.TargetParameters.InputTemplate != "" {
		var decoded []map[string]any
		if err := json.Unmarshal(batch, &decoded); err != nil {
			// An enrichment may legitimately return a non-array payload; there
			// is nothing per-record to apply a template to in that case.
			return nil, false, fmt.Errorf("TargetParameters.InputTemplate: the batch is not a JSON array of records: %w", err)
		}
		rendered, err := h.renderTemplate(p, decoded, p.TargetParameters.InputTemplate)
		if err != nil {
			return nil, false, fmt.Errorf("TargetParameters.InputTemplate: %w", err)
		}
		if batch, err = json.Marshal(rendered); err != nil {
			return nil, false, err
		}
	}
	return batch, false, nil
}

// renderTemplate applies an InputTemplate to each record of a batch.
//
// AWS applies input transformers "separately on each individual JSON record in
// the array, not the array as a whole". A template that renders something other
// than a JSON object is kept as a raw JSON value so the batch stays an array.
func (h *Handler) renderTemplate(p *Pipe, records []map[string]any, template string) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		raw, err := eventtarget.PathTemplate(template).Render(record, h.reservedVars(p, record))
		if err != nil {
			return nil, err
		}
		var rendered map[string]any
		if err := json.Unmarshal(raw, &rendered); err != nil {
			return nil, fmt.Errorf("the rendered template is not a JSON object: %s", strings.TrimSpace(string(raw)))
		}
		out = append(out, rendered)
	}
	return out, nil
}

// reservedVars supplies the aws.pipes.* variables an InputTemplate may use.
func (h *Handler) reservedVars(p *Pipe, record map[string]any) map[string]any {
	return map[string]any{
		"aws.pipes.pipe-arn":             p.Arn,
		"aws.pipes.pipe-name":            p.Name,
		"aws.pipes.source-arn":           p.SourceArn,
		"aws.pipes.enrichment-arn":       p.Enrichment,
		"aws.pipes.target-arn":           p.TargetArn,
		"aws.pipes.event.ingestion-time": h.clk.Now().UTC().Format(time.RFC3339),
		"aws.pipes.event":                record,
		"aws.pipes.event.json":           record,
	}
}

// enrich invokes the pipe's enrichment, returning the batch the target should
// receive. drop is true when the enrichment returned an empty response, AWS's
// way of saying "do not invoke the target".
//
// A pipe with no enrichment is a pass-through.
func (h *Handler) enrich(ctx context.Context, p *Pipe, batch []byte) (out []byte, drop bool, err error) {
	if p.Enrichment == "" {
		return batch, false, nil
	}
	if h.enricher == nil {
		return nil, false, fmt.Errorf("enrichment is configured but the Lambda service is not available")
	}
	name := eventtarget.FunctionName(p.Enrichment)
	outcome, err := h.enricher.Invoke(ctx, name, batch)
	if err != nil {
		return nil, false, fmt.Errorf("enrichment %s: %w", name, err)
	}
	if outcome == nil {
		return nil, false, fmt.Errorf("enrichment %s returned no response", name)
	}
	if outcome.FunctionError != "" {
		return nil, false, fmt.Errorf("enrichment %s failed (%s): %s",
			name, outcome.FunctionError, strings.TrimSpace(string(outcome.Payload)))
	}
	if isEmptyEnrichmentResponse(outcome.Payload) {
		return nil, true, nil
	}
	return outcome.Payload, false, nil
}

// isEmptyEnrichmentResponse reports AWS's "do not invoke the target" signal:
// an empty response, `""`, `{}`, `[]` or JSON null. `[{}]` is deliberately not
// one — AWS documents it as "invoke the target with an empty payload".
func isEmptyEnrichmentResponse(payload []byte) bool {
	switch strings.TrimSpace(string(payload)) {
	case "", `""`, "{}", "[]", "null":
		return true
	}
	return false
}

// dispatch delivers the batch to the pipe's target through the shared
// eventtarget dispatcher, returning what the target answered.
//
// How a batch maps onto a target is AWS's rule, not a choice: a target with a
// batch API — SQS, SNS, Kinesis, Firehose, an EventBridge bus — receives one
// entry per record, and "if the enrichment or target doesn't have a batch API
// but receives full JSON payloads, such as Lambda and Step Functions, the
// entire JSON array is sent in one request".
//
// A per-record target is delivered to record by record, and the first failure
// fails the whole execution: AWS does not offer partial-batch reporting for
// those targets either, so a polling source retries the batch rather than
// pretending it succeeded. The returned response is non-nil only for a Lambda
// target, the one kind that answers with a payload; it is what carries a
// partial-batch failure report.
func (h *Handler) dispatch(ctx context.Context, p *Pipe, batch []byte) ([]byte, error) {
	dispatcher := h.dispatcher()
	if dispatcher == nil {
		return nil, fmt.Errorf("target router is not configured")
	}
	kind := targetKindOf(p)
	if kind == "" {
		return nil, fmt.Errorf("target %s is not a type this emulator delivers to", p.TargetArn)
	}
	if kind == eventtarget.KindECS {
		// Refused at CreatePipe time; reachable only for a pipe stored by a
		// build that predates the wiring check.
		return nil, fmt.Errorf("ECS targets are not supported by EventBridge Pipes in Overcast")
	}

	if targetTakesWholeBatch(kind) {
		return dispatcher.DeliverResponse(ctx, h.targetRequest(p, kind, batch))
	}
	records := splitBatch(batch)
	for i, record := range records {
		if err := dispatcher.Deliver(ctx, h.targetRequest(p, kind, record)); err != nil {
			return nil, fmt.Errorf("record %d of %d: %w", i+1, len(records), err)
		}
	}
	return nil, nil
}

// batchFailures is a target's partial-batch failure report, resolved against
// the source records the execution delivered.
//
// The zero value means the target reported nothing, so the whole batch is
// acknowledged — which is every execution whose target is not a Lambda function
// and every Lambda target that does not use the feature.
type batchFailures struct {
	// failed holds the source-record identifiers the target named.
	failed map[string]bool
	// firstIndex is the position, in the delivered batch, of the earliest
	// record the target named. A stream source resumes there.
	firstIndex int
	// reported is true when the target named at least one record.
	reported bool
	// retryAll is true when the report could not be honoured, so nothing may
	// be acknowledged and the whole batch is redelivered.
	retryAll bool
}

// readBatchFailures reads a partial-batch failure report out of a target's response
// and resolves it against the source records of the execution.
//
// Unlike a Lambda event source mapping, a pipe has no FunctionResponseTypes to
// opt in with — AWS makes partial-batch reporting always available for a Lambda
// target — so a response that never mentions the member is left alone rather
// than being held to AWS's rules for a malformed one. See partialbatch.Reports.
//
// Not covered, and documented as a gap in docs/services/pipes.md: a Step
// Functions target, whose asynchronous StartExecution has no response to read,
// and an enrichment's response, which AWS defines as *replacing* the batch
// rather than reporting on it.
func (h *Handler) readBatchFailures(ctx context.Context, p *Pipe, records []map[string]any, response []byte) batchFailures {
	if len(response) == 0 || !partialbatch.Reports(response) {
		return batchFailures{}
	}
	log := h.log.WithRecorder(ctx)
	parsed := partialbatch.Parse(response)
	if parsed.WholeBatchFailed {
		log.Warn("pipes: partial-batch failure report not honoured — the whole batch is retried",
			zap.String("pipe", p.Name), zap.String("target", p.TargetArn),
			zap.String("reason", parsed.Reason))
		return batchFailures{retryAll: true}
	}

	identifiers := sourceRecordIdentifiers(p, records)
	firstIndex, reported, unknown := parsed.LowestReportedIndex(identifiers)
	if unknown != "" {
		log.Warn("pipes: partial-batch failure report named a record that was not in the batch — the whole batch is retried",
			zap.String("pipe", p.Name), zap.String("target", p.TargetArn),
			zap.String("itemIdentifier", unknown))
		return batchFailures{retryAll: true}
	}
	if !reported {
		return batchFailures{}
	}
	failed := make(map[string]bool, len(parsed.Failed))
	for _, id := range parsed.Failed {
		failed[id] = true
	}
	return batchFailures{failed: failed, firstIndex: firstIndex, reported: true}
}

// sourceRecordIdentifiers returns the identifier each source record is reported
// under: the message ID for an SQS queue and the sequence number for a stream,
// which is what AWS names in an itemIdentifier.
func sourceRecordIdentifiers(p *Pipe, records []map[string]any) []string {
	kind, err := classifySourceARN(p.SourceArn)
	if err != nil {
		return make([]string, len(records))
	}
	out := make([]string, len(records))
	for i, record := range records {
		switch kind {
		case sourceSQSQueue:
			out[i], _ = record["messageId"].(string)
		case sourceKinesisStream:
			out[i], _ = record["sequenceNumber"].(string)
		case sourceDynamoDBStream:
			// eventID and dynamodb.SequenceNumber are the same value; the
			// top-level one needs no second type assertion.
			out[i], _ = record["eventID"].(string)
		}
	}
	return out
}

// targetTakesWholeBatch reports whether the target receives the whole JSON
// array in one request rather than one entry per record.
func targetTakesWholeBatch(kind eventtarget.Kind) bool {
	return kind == eventtarget.KindLambda || kind == eventtarget.KindStepFunctions
}

// splitBatch returns the individual records of a batch. A payload that is not
// a JSON array — an enrichment may return anything — is delivered as one item.
func splitBatch(batch []byte) [][]byte {
	var items []json.RawMessage
	if err := json.Unmarshal(batch, &items); err != nil {
		return [][]byte{batch}
	}
	out := make([][]byte, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

// targetRequest builds the delivery request for one payload — the whole batch
// or a single record, depending on the target.
func (h *Handler) targetRequest(p *Pipe, kind eventtarget.Kind, payload []byte) eventtarget.Request {
	req := eventtarget.Request{ARN: p.TargetArn, Kind: kind, Payload: payload}
	tp := p.TargetParameters

	switch kind { //nolint:exhaustive // ECS is refused before dispatch reaches here
	case eventtarget.KindLambda:
		// AWS invokes a Lambda target with REQUEST_RESPONSE unless the pipe
		// asks for FIRE_AND_FORGET, so a function error is a delivery failure.
		req.InvocationType = eventtarget.InvocationRequestResponse
		if tp != nil && tp.LambdaFunctionParameters != nil &&
			tp.LambdaFunctionParameters.InvocationType == invocationFireAndForget {
			req.InvocationType = eventtarget.InvocationEvent
		}
	case eventtarget.KindSQS:
		if tp != nil && tp.SqsQueueParameters != nil {
			req.MessageGroupID = tp.SqsQueueParameters.MessageGroupId
			req.MessageDeduplicationID = tp.SqsQueueParameters.MessageDeduplicationId
		}
	case eventtarget.KindKinesis:
		req.PartitionKey = kinesisPartitionKey(p, payload)
	case eventtarget.KindEventBus:
		if tp != nil && tp.EventBridgeEventBusParameters != nil {
			req.Source = tp.EventBridgeEventBusParameters.Source
			req.DetailType = tp.EventBridgeEventBusParameters.DetailType
			req.Resources = tp.EventBridgeEventBusParameters.Resources
		}
	}
	return req
}

// kinesisPartitionKey resolves a Kinesis target's partition key for one record.
// AWS allows a dynamic path — `$.messageId` — resolved against the record.
func kinesisPartitionKey(p *Pipe, record []byte) string {
	key := ""
	if p.TargetParameters != nil && p.TargetParameters.KinesisStreamParameters != nil {
		key = p.TargetParameters.KinesisStreamParameters.PartitionKey
	}
	if !strings.HasPrefix(key, "$") {
		return key
	}
	var decoded any
	if json.Unmarshal(record, &decoded) != nil {
		return ""
	}
	if v, ok := eventtarget.SelectPath(decoded, key); ok {
		if s, isString := v.(string); isString {
			return s
		}
		return fmt.Sprint(v)
	}
	return ""
}

// targetKindOf resolves the pipe's target kind, returning "" when the stored
// ARN no longer classifies (a pipe written by a build before the wiring check).
func targetKindOf(p *Pipe) eventtarget.Kind {
	kind, err := classifyTarget(p.TargetArn)
	if err != nil {
		return ""
	}
	return kind
}

// publishDelivered emits the bus event the web console's activity feed reads.
func (h *Handler) publishDelivered(ctx context.Context, p *Pipe, records int) {
	if h.bus == nil {
		return
	}
	h.bus.Publish(ctx, events.Event{
		Type:   events.PipesDelivered,
		Time:   h.clk.Now(),
		Source: serviceName,
		Payload: events.PipesDeliveryPayload{
			PipeName:    p.Name,
			SourceTable: tableNameFromStreamARN(p.SourceArn),
			TargetQueue: resourceNameFromARN(p.TargetArn),
			EventName:   fmt.Sprintf("%d record(s)", records),
		},
	})
}

// regionOrDefault returns primary when it is set, otherwise fallback.
func regionOrDefault(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

// regionContext pins a region on a background context, so a pipe outside the
// default region reads and writes its own regional state.
func regionContext(ctx context.Context, region string) context.Context {
	return middleware.ContextWithRegion(ctx, region)
}

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
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/middleware"
)

// execute runs one pipe execution for a batch of source records.
//
// It returns an error when the batch was not delivered, so a polling source can
// leave the records for redelivery rather than acknowledging them.
func (h *Handler) execute(ctx context.Context, p *Pipe, records []map[string]any) error {
	if len(records) == 0 {
		return nil
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
		h.recordDelivery(rec)
		h.log.Warn("pipes: execution failed before delivery",
			zap.String("pipe", p.Name), zap.Error(err))
		return err
	case drop:
		rec.Outcome = outcomeFiltered
		h.recordDelivery(rec)
		return nil
	}

	if err := h.dispatch(ctx, p, batch); err != nil {
		rec.Outcome = outcomeFailed
		rec.Error = err.Error()
		h.recordDelivery(rec)
		h.log.Warn("pipes: target delivery failed",
			zap.String("pipe", p.Name), zap.String("target", p.TargetArn), zap.Error(err))
		return err
	}
	rec.Outcome = outcomeDelivered
	h.recordDelivery(rec)
	h.publishDelivered(ctx, p, len(records))
	return nil
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
// eventtarget dispatcher.
//
// How a batch maps onto a target is AWS's rule, not a choice: a target with a
// batch API — SQS, SNS, Kinesis, Firehose, an EventBridge bus — receives one
// entry per record, and "if the enrichment or target doesn't have a batch API
// but receives full JSON payloads, such as Lambda and Step Functions, the
// entire JSON array is sent in one request".
//
// A per-record target is delivered to record by record, and the first failure
// fails the whole execution: the emulator has no partial-batch bookkeeping, so
// a polling source retries the batch rather than pretending it succeeded.
func (h *Handler) dispatch(ctx context.Context, p *Pipe, batch []byte) error {
	dispatcher := h.dispatcher()
	if dispatcher == nil {
		return fmt.Errorf("target router is not configured")
	}
	kind := targetKindOf(p)
	if kind == "" {
		return fmt.Errorf("target %s is not a type this emulator delivers to", p.TargetArn)
	}
	if kind == eventtarget.KindECS {
		// Refused at CreatePipe time; reachable only for a pipe stored by a
		// build that predates the wiring check.
		return fmt.Errorf("ECS targets are not supported by EventBridge Pipes in Overcast")
	}

	if targetTakesWholeBatch(kind) {
		return dispatcher.Deliver(ctx, h.targetRequest(p, kind, batch))
	}
	records := splitBatch(batch)
	for i, record := range records {
		if err := dispatcher.Deliver(ctx, h.targetRequest(p, kind, record)); err != nil {
			return fmt.Errorf("record %d of %d: %w", i+1, len(records), err)
		}
	}
	return nil
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

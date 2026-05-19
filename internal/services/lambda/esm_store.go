package lambda

// esm_store.go — EventSourceMapping domain model and state access.
//
// EventSourceMappings are keyed by UUID in the "lambda:esm" namespace.
// Secondary lookups (by function name, by event source ARN) perform a full
// scan and filter, which is fine at emulator scale.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const nsESM = "lambda:esm"

// ScalingConfig controls the maximum concurrency for an SQS event source mapping.
// It mirrors the AWS Lambda ScalingConfig wire format.
type ScalingConfig struct {
	// MaximumConcurrency caps the number of concurrent Lambda invocations driven
	// by this ESM. 0 means unlimited. SQS sources only (2–1000 in AWS).
	MaximumConcurrency int `json:"MaximumConcurrency"`
}

// EventSourceMapping is the domain model for a Lambda event source mapping.
// Field names and JSON tags mirror the AWS Lambda wire format so they can be
// serialised directly in HTTP responses.
type EventSourceMapping struct {
	// UUID is the primary key assigned at creation time.
	UUID string `json:"UUID"`
	// FunctionArn is the full ARN of the target Lambda function.
	FunctionArn string `json:"FunctionArn"`
	// EventSourceArn is the ARN of the SQS queue or DynamoDB stream.
	EventSourceArn string `json:"EventSourceArn"`
	// State is the lifecycle state (see esmState* constants).
	State string `json:"State"`
	// StateTransitionReason is a human-readable explanation of the last state change.
	StateTransitionReason string `json:"StateTransitionReason"`
	// BatchSize is the maximum number of records per invocation batch.
	BatchSize int `json:"BatchSize"`
	// StartingPosition is required for stream-based sources ("TRIM_HORIZON", "LATEST").
	StartingPosition string `json:"StartingPosition,omitempty"`
	// MaximumBatchingWindowInSeconds controls how long to accumulate records
	// before invoking (0 means invoke as soon as records arrive).
	MaximumBatchingWindowInSeconds int `json:"MaximumBatchingWindowInSeconds"`
	// FilterCriteria defines event-filtering patterns evaluated before invoking
	// the function. Only records matching at least one filter are processed.
	FilterCriteria *FilterCriteria `json:"FilterCriteria,omitempty"`
	// MaximumRecordAgeInSeconds is the maximum age (in seconds) of a record that
	// Lambda sends to the function. -1 disables the limit. Stream sources only.
	MaximumRecordAgeInSeconds *int `json:"MaximumRecordAgeInSeconds,omitempty"`
	// MaximumRetryAttempts is the max number of retries when the function returns
	// an error. -1 means unlimited. Stream sources only.
	MaximumRetryAttempts *int `json:"MaximumRetryAttempts,omitempty"`
	// TumblingWindowInSeconds groups stream records into fixed-duration processing
	// windows. 0 disables tumbling windows. Stream sources only.
	TumblingWindowInSeconds int `json:"TumblingWindowInSeconds,omitempty"`
	// BisectBatchOnFunctionError splits a failed batch into two and retries each
	// half separately. Stream sources only.
	BisectBatchOnFunctionError bool `json:"BisectBatchOnFunctionError,omitempty"`
	// DestinationConfig specifies where to send records of failed asynchronous
	// invocations. Only OnFailure.Destination is supported (SQS ARN).
	DestinationConfig *DestinationConfig `json:"DestinationConfig,omitempty"`
	// LastModified is the Unix timestamp (seconds, fractional) of the last update.
	LastModified float64 `json:"LastModified"`
	// LastProcessingResult describes the outcome of the most recent invocation
	// ("No records processed", "OK", "FunctionError", "Throttled", etc.).
	LastProcessingResult string `json:"LastProcessingResult,omitempty"`
	// ScalingConfig controls the maximum concurrent invocations for this ESM.
	// Only applicable to SQS sources. nil means unlimited.
	ScalingConfig *ScalingConfig `json:"ScalingConfig,omitempty"`
}

// FilterCriteria defines event-filtering criteria for an EventSourceMapping.
type FilterCriteria struct {
	Filters []Filter `json:"Filters"`
}

// Filter is a single event-filter pattern in an EventSourceMapping.
type Filter struct {
	Pattern string `json:"Pattern"`
}

// DestinationConfig specifies where to send records of invocations that fail
// after exhausting retries. Mirrors the AWS Lambda DestinationConfig structure.
type DestinationConfig struct {
	OnFailure *OnFailure `json:"OnFailure,omitempty"`
}

// OnFailure specifies the destination for records of failed invocations.
type OnFailure struct {
	Destination string `json:"Destination"`
}

// esmState constants mirror AWS Lambda ESM state names.
const (
	esmStateEnabled  = "Enabled"
	esmStateDisabled = "Disabled"
	esmStateDeleting = "Deleting"
)

// esmStore wraps the shared state.Store for event source mapping operations.
type esmStore struct {
	s *lambdaStore
}

func newESMStore(ls *lambdaStore) *esmStore { return &esmStore{s: ls} }

// getESM returns the ESM with the given UUID.
// Returns (nil, nil) when not found.
func (e *esmStore) getESM(ctx context.Context, uuid string) (*EventSourceMapping, *protocol.AWSError) {
	raw, found, err := e.s.store.Get(ctx, nsESM, serviceutil.RegionKey(e.s.region(ctx), uuid))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("esm get %s: %w", uuid, err))
	}
	if !found {
		return nil, nil
	}
	var m EventSourceMapping
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("esm decode %s: %w", uuid, err))
	}
	return &m, nil
}

// putESM writes the ESM to the store.
func (e *esmStore) putESM(ctx context.Context, m *EventSourceMapping) *protocol.AWSError {
	raw, err := json.Marshal(m)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("esm marshal %s: %w", m.UUID, err))
	}
	if err := e.s.store.Set(ctx, nsESM, serviceutil.RegionKey(e.s.region(ctx), m.UUID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("esm put %s: %w", m.UUID, err))
	}
	return nil
}

// deleteESM removes the ESM from the store.
func (e *esmStore) deleteESM(ctx context.Context, uuid string) *protocol.AWSError {
	if err := e.s.store.Delete(ctx, nsESM, serviceutil.RegionKey(e.s.region(ctx), uuid)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("esm delete %s: %w", uuid, err))
	}
	return nil
}

// listESMs returns all event source mappings, optionally filtered by
// FunctionName (if functionName != "") and/or EventSourceArn prefix
// (if eventSourceArn != "").
func (e *esmStore) listESMs(ctx context.Context, functionName, eventSourceArn string) ([]*EventSourceMapping, *protocol.AWSError) {
	kvs, err := e.s.store.Scan(ctx, nsESM, serviceutil.RegionKey(e.s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("esm scan: %w", err))
	}

	out := make([]*EventSourceMapping, 0, len(kvs))
	for _, v := range kvs {
		var m EventSourceMapping
		if err := json.Unmarshal([]byte(v.Value), &m); err != nil {
			continue
		}
		if functionName != "" && functionNameFromARN(m.FunctionArn) != functionName {
			continue
		}
		if eventSourceArn != "" && !strings.EqualFold(m.EventSourceArn, eventSourceArn) {
			continue
		}
		out = append(out, &m)
	}
	return out, nil
}

// listAllESMs returns all event source mappings across all regions.
// Used by startup recovery (ReloadAll) which must not be scoped to a single region.
func (e *esmStore) listAllESMs(ctx context.Context) ([]*EventSourceMapping, *protocol.AWSError) {
	kvs, err := e.s.store.Scan(ctx, nsESM, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("esm scan all: %w", err))
	}
	out := make([]*EventSourceMapping, 0, len(kvs))
	for _, v := range kvs {
		var m EventSourceMapping
		if err := json.Unmarshal([]byte(v.Value), &m); err != nil {
			continue
		}
		out = append(out, &m)
	}
	return out, nil
}

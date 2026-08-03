package pipes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const nsPipes = "pipes:pipes"

// PipeState is the lifecycle state of a Pipe, matching AWS EventBridge Pipes states.
type PipeState string

// The pipe states this emulator reaches. AWS's PipeState enum also carries the
// *_FAILED and *_ROLLBACK_FAILED members, which only a real control plane can
// produce.
const (
	PipeStateCreating PipeState = "CREATING"
	PipeStateRunning  PipeState = "RUNNING"
	PipeStateStopped  PipeState = "STOPPED"
	PipeStateStopping PipeState = "STOPPING"
	PipeStateStarting PipeState = "STARTING"
	PipeStateUpdating PipeState = "UPDATING"
	PipeStateDeleting PipeState = "DELETING"
)

// ---- Wire types --------------------------------------------------------------
//
// Field names and JSON tags match the AWS EventBridge Pipes REST API. Parameter
// blocks the emulator has no implementation for are still modelled, as
// json.RawMessage, so CreatePipe can name them when it refuses them rather than
// accepting a pipe that would quietly ignore half its configuration.

// FilterCriteria is AWS's PipeSourceParameters.FilterCriteria.
type FilterCriteria struct {
	Filters []struct {
		Pattern string `json:"Pattern"`
	} `json:"Filters,omitempty"`
}

// DeadLetterConfig is AWS's PipeSourceParameters DeadLetterConfig: where a
// stream batch goes once its retries are exhausted.
type DeadLetterConfig struct {
	Arn string `json:"Arn,omitempty"`
}

// StreamSourceParameters covers the shared shape of AWS's
// DynamoDBStreamParameters and KinesisStreamParameters.
//
// MaximumRetryAttempts is a pointer because AWS distinguishes "not set" (retry
// until the record expires) from an explicit 0 (never retry), and the delivery
// path honours both. The JSON shape is unchanged — a pointer marshals and
// unmarshals identically to the int it replaced.
type StreamSourceParameters struct {
	BatchSize                      int               `json:"BatchSize,omitempty"`
	MaximumBatchingWindowInSeconds int               `json:"MaximumBatchingWindowInSeconds,omitempty"`
	StartingPosition               string            `json:"StartingPosition,omitempty"`
	MaximumRetryAttempts           *int              `json:"MaximumRetryAttempts,omitempty"`
	ParallelizationFactor          int               `json:"ParallelizationFactor,omitempty"`
	OnPartialBatchItemFailure      string            `json:"OnPartialBatchItemFailure,omitempty"`
	DeadLetterConfig               *DeadLetterConfig `json:"DeadLetterConfig,omitempty"`
}

// SQSSourceParameters is AWS's PipeSourceSqsQueueParameters.
type SQSSourceParameters struct {
	BatchSize                      int `json:"BatchSize,omitempty"`
	MaximumBatchingWindowInSeconds int `json:"MaximumBatchingWindowInSeconds,omitempty"`
}

// SourceParameters is AWS's PipeSourceParameters.
type SourceParameters struct {
	FilterCriteria           *FilterCriteria         `json:"FilterCriteria,omitempty"`
	DynamoDBStreamParameters *StreamSourceParameters `json:"DynamoDBStreamParameters,omitempty"`
	KinesisStreamParameters  *StreamSourceParameters `json:"KinesisStreamParameters,omitempty"`
	SqsQueueParameters       *SQSSourceParameters    `json:"SqsQueueParameters,omitempty"`

	// Source types with no poller here. Kept so they can be refused by name.
	ActiveMQBrokerParameters        json.RawMessage `json:"ActiveMQBrokerParameters,omitempty"`
	RabbitMQBrokerParameters        json.RawMessage `json:"RabbitMQBrokerParameters,omitempty"`
	ManagedStreamingKafkaParameters json.RawMessage `json:"ManagedStreamingKafkaParameters,omitempty"`
	SelfManagedKafkaParameters      json.RawMessage `json:"SelfManagedKafkaParameters,omitempty"`
}

// EnrichmentParameters is AWS's PipeEnrichmentParameters.
type EnrichmentParameters struct {
	InputTemplate string `json:"InputTemplate,omitempty"`
	// HttpParameters only applies to API destination / API Gateway
	// enrichments, which this emulator refuses.
	HttpParameters json.RawMessage `json:"HttpParameters,omitempty"`
}

// InvocationTypeParameters is the shape shared by AWS's
// PipeTargetLambdaFunctionParameters and PipeTargetStateMachineParameters.
type InvocationTypeParameters struct {
	InvocationType string `json:"InvocationType,omitempty"`
}

// SQSTargetParameters is AWS's PipeTargetSqsQueueParameters.
type SQSTargetParameters struct {
	MessageGroupId         string `json:"MessageGroupId,omitempty"`
	MessageDeduplicationId string `json:"MessageDeduplicationId,omitempty"`
}

// KinesisTargetParameters is AWS's PipeTargetKinesisStreamParameters.
type KinesisTargetParameters struct {
	PartitionKey string `json:"PartitionKey,omitempty"`
}

// EventBusTargetParameters is AWS's PipeTargetEventBridgeEventBusParameters.
type EventBusTargetParameters struct {
	DetailType string   `json:"DetailType,omitempty"`
	EndpointId string   `json:"EndpointId,omitempty"`
	Resources  []string `json:"Resources,omitempty"`
	Source     string   `json:"Source,omitempty"`
	Time       string   `json:"Time,omitempty"`
}

// TargetParameters is AWS's PipeTargetParameters.
type TargetParameters struct {
	InputTemplate                      string                    `json:"InputTemplate,omitempty"`
	LambdaFunctionParameters           *InvocationTypeParameters `json:"LambdaFunctionParameters,omitempty"`
	StepFunctionStateMachineParameters *InvocationTypeParameters `json:"StepFunctionStateMachineParameters,omitempty"`
	SqsQueueParameters                 *SQSTargetParameters      `json:"SqsQueueParameters,omitempty"`
	KinesisStreamParameters            *KinesisTargetParameters  `json:"KinesisStreamParameters,omitempty"`
	EventBridgeEventBusParameters      *EventBusTargetParameters `json:"EventBridgeEventBusParameters,omitempty"`

	// Target types with no sink here. Kept so they can be refused by name.
	HttpParameters              json.RawMessage `json:"HttpParameters,omitempty"`
	EcsTaskParameters           json.RawMessage `json:"EcsTaskParameters,omitempty"`
	BatchJobParameters          json.RawMessage `json:"BatchJobParameters,omitempty"`
	CloudWatchLogsParameters    json.RawMessage `json:"CloudWatchLogsParameters,omitempty"`
	RedshiftDataParameters      json.RawMessage `json:"RedshiftDataParameters,omitempty"`
	SageMakerPipelineParameters json.RawMessage `json:"SageMakerPipelineParameters,omitempty"`
	TimestreamParameters        json.RawMessage `json:"TimestreamParameters,omitempty"`
}

// Pipe represents a single EventBridge Pipe resource.
type Pipe struct {
	// Name is the unique name of the pipe (path parameter in the AWS API).
	Name string `json:"Name"`
	// Arn is the full resource ARN, e.g. arn:aws:pipes:us-east-1:000000000000:pipe/MyPipe
	Arn string `json:"Arn"`
	// Description is an optional free-text description.
	Description string `json:"Description,omitempty"`
	// RoleArn is the role the pipe assumes to reach its source and target.
	// Required by AWS; not evaluated here (Overcast is not a security boundary).
	RoleArn string `json:"RoleArn,omitempty"`

	// SourceArn is the ARN of the source resource. The JSON tag matches AWS's
	// wire field, which is "Source".
	SourceArn string `json:"Source"`
	// SourceParameters configures how the source is read.
	SourceParameters *SourceParameters `json:"SourceParameters,omitempty"`

	// Enrichment is the ARN of an optional resource invoked between source and
	// target; its return value replaces the batch.
	Enrichment string `json:"Enrichment,omitempty"`
	// EnrichmentParameters configures the enrichment invocation.
	EnrichmentParameters *EnrichmentParameters `json:"EnrichmentParameters,omitempty"`

	// TargetArn is the ARN of the target resource. AWS's wire field is "Target".
	TargetArn string `json:"Target"`
	// TargetParameters configures how the target is invoked.
	TargetParameters *TargetParameters `json:"TargetParameters,omitempty"`

	// CurrentState is the lifecycle state of the pipe.
	CurrentState PipeState `json:"CurrentState"`
	// DesiredState is what the pipe should converge to.
	DesiredState PipeState `json:"DesiredState"`
	// StateReason explains a state AWS would not reach on its own.
	StateReason string `json:"StateReason,omitempty"`
	// Tags are the pipe's resource tags.
	Tags map[string]string `json:"Tags,omitempty"`

	// CreationTime is the Unix epoch seconds when the pipe was created.
	// Float64 matches the AWS EventBridge Pipes REST API wire format.
	CreationTime float64 `json:"CreationTime"`
	// LastModifiedTime is the Unix epoch seconds of the last state change.
	LastModifiedTime float64 `json:"LastModifiedTime"`
}

// summary renders the PipeSummary shape ListPipes returns. AWS's list response
// carries a strict subset of DescribePipe — notably no parameter blocks.
func (p *Pipe) summary() map[string]any {
	out := map[string]any{
		"Name":             p.Name,
		"Arn":              p.Arn,
		"Source":           p.SourceArn,
		"Target":           p.TargetArn,
		"CurrentState":     p.CurrentState,
		"DesiredState":     p.DesiredState,
		"CreationTime":     p.CreationTime,
		"LastModifiedTime": p.LastModifiedTime,
	}
	if p.Enrichment != "" {
		out["Enrichment"] = p.Enrichment
	}
	if p.StateReason != "" {
		out["StateReason"] = p.StateReason
	}
	return out
}

// mutationResponse renders the 6-member body AWS returns from CreatePipe,
// UpdatePipe, DeletePipe and StartPipe/StopPipe.
func (p *Pipe) mutationResponse() map[string]any {
	return map[string]any{
		"Arn":              p.Arn,
		"Name":             p.Name,
		"CurrentState":     p.CurrentState,
		"DesiredState":     p.DesiredState,
		"CreationTime":     p.CreationTime,
		"LastModifiedTime": p.LastModifiedTime,
	}
}

// tableNameFromStreamARN extracts the DynamoDB table name from a stream ARN.
// e.g. "arn:aws:dynamodb:us-east-1:000000000000:table/MyTable/stream/2024-01-01T00:00:00.000" → "MyTable".
func tableNameFromStreamARN(streamARN string) string {
	// Format: arn:aws:dynamodb:REGION:ACCOUNT:table/TABLE/stream/LABEL
	for _, part := range strings.Split(streamARN, ":") {
		if strings.HasPrefix(part, "table/") {
			segments := strings.Split(part, "/")
			if len(segments) >= 2 {
				return segments[1]
			}
		}
	}
	return ""
}

// resourceNameFromARN extracts the trailing resource name of an ARN — the SQS
// queue name, the Kinesis stream name, the EventBridge bus name.
func resourceNameFromARN(arn string) string {
	name := arn
	if i := strings.LastIndexAny(name, ":/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// regionFromARN returns an ARN's region segment, or "" when it has none.
func regionFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

// ---- Store ------------------------------------------------------------------

type pipesStore struct {
	st            state.Store
	defaultRegion string
}

func newPipesStore(st state.Store, defaultRegion string) *pipesStore {
	return &pipesStore{st: st, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *pipesStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

func (s *pipesStore) getPipe(ctx context.Context, name string) (*Pipe, *protocol.AWSError) {
	raw, found, err := s.st.Get(ctx, nsPipes, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errPipeNotFound(name)
	}
	var p Pipe
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &p, nil
}

func (s *pipesStore) putPipe(ctx context.Context, p *Pipe) *protocol.AWSError {
	raw, err := json.Marshal(p)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.st.Set(ctx, nsPipes, serviceutil.RegionKey(s.region(ctx), p.Name), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *pipesStore) deletePipe(ctx context.Context, name string) *protocol.AWSError {
	if err := s.st.Delete(ctx, nsPipes, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *pipesStore) listPipes(ctx context.Context) ([]*Pipe, *protocol.AWSError) {
	return s.scan(ctx, serviceutil.RegionKey(s.region(ctx), ""))
}

// listAllPipes returns all pipes across all regions.
func (s *pipesStore) listAllPipes(ctx context.Context) ([]*Pipe, *protocol.AWSError) {
	return s.scan(ctx, "")
}

// scan decodes every pipe under prefix. A single malformed record is skipped
// rather than failing the whole listing.
func (s *pipesStore) scan(ctx context.Context, prefix string) ([]*Pipe, *protocol.AWSError) {
	pairs, err := s.st.Scan(ctx, nsPipes, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	pipes := make([]*Pipe, 0, len(pairs))
	for _, kv := range pairs {
		var pipe Pipe
		if err := json.Unmarshal([]byte(kv.Value), &pipe); err != nil {
			continue
		}
		pipes = append(pipes, &pipe)
	}
	return pipes, nil
}

// ---- Errors -----------------------------------------------------------------

func errPipeNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NotFoundException",
		Message:    fmt.Sprintf("Pipe %q does not exist.", name),
		HTTPStatus: http.StatusNotFound,
	}
}

func errPipeAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ConflictException",
		Message:    fmt.Sprintf("Pipe %q already exists.", name),
		HTTPStatus: http.StatusConflict,
	}
}

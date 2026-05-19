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

const (
	PipeStateCreating PipeState = "CREATING"
	PipeStateRunning  PipeState = "RUNNING"
	PipeStateStopped  PipeState = "STOPPED"
	PipeStateStopping PipeState = "STOPPING"
	PipeStateStarting PipeState = "STARTING"
	PipeStateDeleting PipeState = "DELETING"
)

// Pipe represents a single EventBridge Pipe resource.
// Field names and JSON tags match the AWS EventBridge Pipes REST API wire format.
type Pipe struct {
	// Name is the unique name of the pipe (path parameter in the AWS API).
	Name string `json:"Name"`
	// Arn is the full resource ARN, e.g. arn:aws:pipes:us-east-1:000000000000:pipe/MyPipe
	Arn string `json:"Arn"`
	// SourceArn is the ARN of the source resource (DynamoDB stream ARN).
	// JSON tag matches the real AWS Pipes API wire format.
	SourceArn string `json:"Source"`
	// TargetArn is the ARN of the target resource (SQS queue ARN).
	// JSON tag matches the real AWS Pipes API wire format.
	TargetArn string `json:"Target"`
	// SourceName is the parsed DynamoDB table name from SourceArn.
	// Included for convenience so consumers don't have to parse ARNs.
	SourceName string `json:"SourceName"`
	// TargetName is the parsed SQS queue name from TargetArn.
	// Included for convenience so consumers don't have to parse ARNs.
	TargetName string `json:"TargetName"`
	// CurrentState is the lifecycle state of the pipe.
	CurrentState PipeState `json:"CurrentState"`
	// DesiredState is what the pipe should converge to.
	DesiredState PipeState `json:"DesiredState"`
	// Description is an optional free-text description.
	Description string `json:"Description,omitempty"`
	// CreationTime is the Unix epoch seconds when the pipe was created.
	// Float64 matches the AWS EventBridge Pipes REST API wire format (epoch seconds).
	CreationTime float64 `json:"CreationTime"`
	// LastModifiedTime is the Unix epoch seconds of the last state change.
	LastModifiedTime float64 `json:"LastModifiedTime"`
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

// queueNameFromARN extracts the SQS queue name from an ARN.
// e.g. "arn:aws:sqs:us-east-1:000000000000:my-queue" → "my-queue".
func queueNameFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
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
	pairs, err := s.st.Scan(ctx, nsPipes, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	pipes := make([]*Pipe, 0, len(pairs))
	for _, p := range pairs {
		var pipe Pipe
		if err := json.Unmarshal([]byte(p.Value), &pipe); err != nil {
			continue
		}
		pipes = append(pipes, &pipe)
	}
	return pipes, nil
}

// listAllPipes returns all pipes across all regions.
func (s *pipesStore) listAllPipes(ctx context.Context) ([]*Pipe, *protocol.AWSError) {
	pairs, err := s.st.Scan(ctx, nsPipes, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	pipes := make([]*Pipe, 0, len(pairs))
	for _, p := range pairs {
		var pipe Pipe
		if err := json.Unmarshal([]byte(p.Value), &pipe); err != nil {
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

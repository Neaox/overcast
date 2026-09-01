package stepfunctions

import (
	"context"
	"fmt"
	"net/http"

	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

type createStateMachineRequest struct {
	Name                 string         `json:"name" cbor:"name"`
	Definition           string         `json:"definition" cbor:"definition"`
	RoleArn              string         `json:"roleArn" cbor:"roleArn"`
	Type                 string         `json:"type" cbor:"type"`
	LoggingConfiguration map[string]any `json:"loggingConfiguration" cbor:"loggingConfiguration"`
	TracingConfiguration map[string]any `json:"tracingConfiguration" cbor:"tracingConfiguration"`
	Tags                 []sfnTag       `json:"tags" cbor:"tags"`
}

// sfnTag is the wire shape of Step Functions' tag list, shared by
// CreateStateMachine and TagResource.
type sfnTag struct {
	Key   string `json:"key" cbor:"key"`
	Value string `json:"value" cbor:"value"`
}

type createStateMachineResponse struct {
	StateMachineArn string  `json:"stateMachineArn" cbor:"stateMachineArn"`
	CreationDate    float64 `json:"creationDate" cbor:"creationDate"`
}

type describeStateMachineRequest struct {
	StateMachineArn string `json:"stateMachineArn" cbor:"stateMachineArn"`
}

type describeStateMachineResponse struct {
	StateMachineArn      string         `json:"stateMachineArn" cbor:"stateMachineArn"`
	Name                 string         `json:"name" cbor:"name"`
	Definition           string         `json:"definition" cbor:"definition"`
	RoleArn              string         `json:"roleArn" cbor:"roleArn"`
	Type                 string         `json:"type" cbor:"type"`
	Status               string         `json:"status" cbor:"status"`
	CreationDate         float64        `json:"creationDate" cbor:"creationDate"`
	LoggingConfiguration map[string]any `json:"loggingConfiguration" cbor:"loggingConfiguration"`
	TracingConfiguration map[string]any `json:"tracingConfiguration" cbor:"tracingConfiguration"`
}

type updateStateMachineRequest struct {
	StateMachineArn      string         `json:"stateMachineArn" cbor:"stateMachineArn"`
	Definition           string         `json:"definition" cbor:"definition"`
	RoleArn              string         `json:"roleArn" cbor:"roleArn"`
	LoggingConfiguration map[string]any `json:"loggingConfiguration" cbor:"loggingConfiguration"`
	TracingConfiguration map[string]any `json:"tracingConfiguration" cbor:"tracingConfiguration"`
}

type updateStateMachineResponse struct {
	UpdateDate float64 `json:"updateDate" cbor:"updateDate"`
}

type listStateMachinesRequest struct{}

type stateMachineListItem struct {
	StateMachineArn string  `json:"stateMachineArn" cbor:"stateMachineArn"`
	Name            string  `json:"name" cbor:"name"`
	Type            string  `json:"type" cbor:"type"`
	CreationDate    float64 `json:"creationDate" cbor:"creationDate"`
}

type listStateMachinesResponse struct {
	StateMachines []stateMachineListItem `json:"stateMachines" cbor:"stateMachines"`
}

type startExecutionRequest struct {
	StateMachineArn string `json:"stateMachineArn" cbor:"stateMachineArn"`
	Input           string `json:"input" cbor:"input"`
	Name            string `json:"name" cbor:"name"`
}

type startExecutionResponse struct {
	ExecutionArn string  `json:"executionArn" cbor:"executionArn"`
	StartDate    float64 `json:"startDate" cbor:"startDate"`
}

type deleteStateMachineRequest struct {
	StateMachineArn string `json:"stateMachineArn" cbor:"stateMachineArn"`
}

func (h *Handler) createStateMachineTyped(ctx context.Context, req *createStateMachineRequest) (*createStateMachineResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, &protocol.AWSError{
			Code:       "InvalidName",
			Message:    "Value null at 'name' failed to satisfy constraint",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if aerr := validateDefinitionForCreate(req.Definition); aerr != nil {
		return nil, aerr
	}
	existing, err := h.store.GetStateMachine(ctx, req.Name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if existing != nil {
		smType := req.Type
		if smType == "" {
			smType = "STANDARD"
		}
		if existing.Definition == req.Definition && existing.RoleArn == req.RoleArn && existing.Type == smType {
			return &createStateMachineResponse{
				StateMachineArn: existing.ARN,
				CreationDate:    float64(existing.CreatedAt.UnixMilli()) / 1000.0,
			}, nil
		}
		return nil, &protocol.AWSError{
			Code:       "StateMachineAlreadyExists",
			Message:    fmt.Sprintf("State Machine Already Exists: '%s'", req.Name),
			HTTPStatus: http.StatusConflict,
		}
	}
	smType := req.Type
	if smType == "" {
		smType = "STANDARD"
	}
	// Validated before the state machine is written (#1196) — a rejected
	// create leaves no state machine behind.
	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}
	if aerr := serviceutil.ValidateTags(sfnTagCfg, tags); aerr != nil {
		return nil, aerr
	}
	now := h.clk.Now()
	// The typed path used to hardcode h.cfg.Region here instead of resolving
	// the request's actual region (as execution_ops.go's StartExecution
	// already does) — a pre-existing divergence between this path and the
	// legacy JSON handler unified into it by #1196. A CBOR create used to
	// mint an ARN in the account's default region regardless of the request
	// region; fixed to match.
	region := middleware.RegionFromContext(ctx, h.cfg.Region)
	arn := protocol.ARN(region, h.cfg.AccountID, "states", "stateMachine:"+req.Name)
	sm := &StateMachine{
		Name:                 req.Name,
		ARN:                  arn,
		Definition:           req.Definition,
		RoleArn:              req.RoleArn,
		Type:                 smType,
		Status:               "ACTIVE",
		CreatedAt:            now,
		LoggingConfiguration: req.LoggingConfiguration,
		TracingConfiguration: req.TracingConfiguration,
		Tags:                 tags,
	}
	if err := h.store.PutStateMachine(ctx, sm); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	h.publishCtx(ctx, events.SFNStateMachineCreated, events.ResourcePayload{Name: req.Name})
	return &createStateMachineResponse{
		StateMachineArn: arn,
		CreationDate:    float64(now.UnixMilli()) / 1000.0,
	}, nil
}

func (h *Handler) describeStateMachineTyped(ctx context.Context, req *describeStateMachineRequest) (*describeStateMachineResponse, *protocol.AWSError) {
	name := extractSMName(req.StateMachineArn)
	sm, err := h.store.GetStateMachine(ctx, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if sm == nil {
		return nil, errSMNotFound(req.StateMachineArn)
	}
	return &describeStateMachineResponse{
		StateMachineArn:      sm.ARN,
		Name:                 sm.Name,
		Definition:           sm.Definition,
		RoleArn:              sm.RoleArn,
		Type:                 sm.Type,
		Status:               sm.Status,
		CreationDate:         float64(sm.CreatedAt.UnixMilli()) / 1000.0,
		LoggingConfiguration: sfnLoggingConfigOrDefault(sm.LoggingConfiguration),
		TracingConfiguration: sfnTracingConfigOrDefault(sm.TracingConfiguration),
	}, nil
}

// updateStateMachineTyped implements UpdateStateMachine. Every field besides
// StateMachineArn is optional and left unchanged when omitted, matching real
// AWS: an update supplies only the properties it wants to change.
//
// Versioning (publish/versionDescription, stateMachineVersionArn in the
// response) is out of scope — Overcast does not model state machine
// versions or aliases anywhere else either.
func (h *Handler) updateStateMachineTyped(ctx context.Context, req *updateStateMachineRequest) (*updateStateMachineResponse, *protocol.AWSError) {
	name := extractSMName(req.StateMachineArn)
	sm, err := h.store.GetStateMachine(ctx, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if sm == nil {
		return nil, errSMNotFound(req.StateMachineArn)
	}
	if req.Definition != "" {
		if aerr := validateDefinitionForCreate(req.Definition); aerr != nil {
			return nil, aerr
		}
		sm.Definition = req.Definition
	}
	if req.RoleArn != "" {
		sm.RoleArn = req.RoleArn
	}
	if req.LoggingConfiguration != nil {
		sm.LoggingConfiguration = req.LoggingConfiguration
	}
	if req.TracingConfiguration != nil {
		sm.TracingConfiguration = req.TracingConfiguration
	}
	if err := h.store.PutStateMachine(ctx, sm); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	h.publishCtx(ctx, events.SFNStateMachineUpdated, events.ResourcePayload{Name: name})
	return &updateStateMachineResponse{UpdateDate: float64(h.clk.Now().UnixMilli()) / 1000.0}, nil
}

func (h *Handler) listStateMachinesTyped(ctx context.Context, _ *listStateMachinesRequest) (*listStateMachinesResponse, *protocol.AWSError) {
	sms, err := h.store.ListStateMachines(ctx)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	items := make([]stateMachineListItem, 0, len(sms))
	for _, sm := range sms {
		items = append(items, stateMachineListItem{
			StateMachineArn: sm.ARN,
			Name:            sm.Name,
			Type:            sm.Type,
			CreationDate:    float64(sm.CreatedAt.UnixMilli()) / 1000.0,
		})
	}
	return &listStateMachinesResponse{StateMachines: items}, nil
}

func (h *Handler) deleteStateMachineTyped(ctx context.Context, req *deleteStateMachineRequest) (*struct{}, *protocol.AWSError) {
	name := extractSMName(req.StateMachineArn)
	if err := h.store.DeleteStateMachine(ctx, name); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	h.publishCtx(ctx, events.SFNStateMachineDeleted, events.ResourcePayload{Name: name})
	return &struct{}{}, nil
}

func (h *Handler) publishCtx(ctx context.Context, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: t, Payload: payload})
	}
}

type listTagsForResourceTypedResponse struct {
	Tags []map[string]string `json:"tags"`
}

func (h *Handler) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*struct{}, *protocol.AWSError) {
	name := extractSMName(req.ResourceArn)

	incoming := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		incoming[t.Key] = t.Value
	}

	if aerr := serviceutil.ApplyInlineTags(ctx, name, incoming, sfnTagCfg,
		func(ctx context.Context, key string) (*StateMachine, *protocol.AWSError) {
			sm, err := h.store.GetStateMachine(ctx, key)
			if err != nil {
				return nil, protocol.Wrap(protocol.ErrInternalError, err)
			}
			if sm == nil {
				return nil, errSMNotFound(req.ResourceArn)
			}
			return sm, nil
		},
		func(ctx context.Context, sm *StateMachine) *protocol.AWSError {
			if err := h.store.PutStateMachine(ctx, sm); err != nil {
				return protocol.Wrap(protocol.ErrInternalError, err)
			}
			return nil
		},
	); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*struct{}, *protocol.AWSError) {
	name := extractSMName(req.ResourceArn)
	sm, err := h.store.GetStateMachine(ctx, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if sm == nil {
		return nil, errSMNotFound(req.ResourceArn)
	}

	for _, k := range req.TagKeys {
		delete(sm.Tags, k)
	}

	if err := h.store.PutStateMachine(ctx, sm); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &struct{}{}, nil
}

func (h *Handler) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceTypedResponse, *protocol.AWSError) {
	name := extractSMName(req.ResourceArn)
	sm, err := h.store.GetStateMachine(ctx, name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if sm == nil {
		return nil, errSMNotFound(req.ResourceArn)
	}

	tags := make([]map[string]string, 0, len(sm.Tags))
	for k, v := range sm.Tags {
		tags = append(tags, map[string]string{"key": k, "value": v})
	}
	return &listTagsForResourceTypedResponse{Tags: tags}, nil
}

package stepfunctions

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

type createStateMachineRequest struct {
	Name       string `json:"name" cbor:"name"`
	Definition string `json:"definition" cbor:"definition"`
	RoleArn    string `json:"roleArn" cbor:"roleArn"`
	Type       string `json:"type" cbor:"type"`
}

type createStateMachineResponse struct {
	StateMachineArn string  `json:"stateMachineArn" cbor:"stateMachineArn"`
	CreationDate    float64 `json:"creationDate" cbor:"creationDate"`
}

type describeStateMachineRequest struct {
	StateMachineArn string `json:"stateMachineArn" cbor:"stateMachineArn"`
}

type describeStateMachineResponse struct {
	StateMachineArn string  `json:"stateMachineArn" cbor:"stateMachineArn"`
	Name            string  `json:"name" cbor:"name"`
	Definition      string  `json:"definition" cbor:"definition"`
	RoleArn         string  `json:"roleArn" cbor:"roleArn"`
	Type            string  `json:"type" cbor:"type"`
	Status          string  `json:"status" cbor:"status"`
	CreationDate    float64 `json:"creationDate" cbor:"creationDate"`
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
	now := h.clk.Now()
	arn := protocol.ARN(h.cfg.Region, h.cfg.AccountID, "states", "stateMachine:"+req.Name)
	sm := &StateMachine{
		Name:       req.Name,
		ARN:        arn,
		Definition: req.Definition,
		RoleArn:    req.RoleArn,
		Type:       smType,
		Status:     "ACTIVE",
		CreatedAt:  now,
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
		StateMachineArn: sm.ARN,
		Name:            sm.Name,
		Definition:      sm.Definition,
		RoleArn:         sm.RoleArn,
		Type:            sm.Type,
		Status:          sm.Status,
		CreationDate:    float64(sm.CreatedAt.UnixMilli()) / 1000.0,
	}, nil
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

func (h *Handler) startExecutionTyped(ctx context.Context, req *startExecutionRequest) (*startExecutionResponse, *protocol.AWSError) {
	smName := extractSMName(req.StateMachineArn)
	sm, err := h.store.GetStateMachine(ctx, smName)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if sm == nil {
		return nil, errSMNotFound(req.StateMachineArn)
	}
	execName := req.Name
	if execName == "" {
		execName = uuid.NewString()
	}
	now := h.clk.Now()
	execArn := protocol.ARN(h.cfg.Region, h.cfg.AccountID, "states", "execution:"+smName+":"+execName)
	exec := &Execution{
		ExecutionArn:    execArn,
		StateMachineArn: req.StateMachineArn,
		Name:            execName,
		Input:           req.Input,
		Status:          "SUCCEEDED",
		StartDate:       now,
	}
	if err := h.store.PutExecution(ctx, exec); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	h.publishCtx(ctx, events.SFNExecutionStarted, events.ResourcePayload{Name: execName})
	return &startExecutionResponse{
		ExecutionArn: execArn,
		StartDate:    float64(now.UnixMilli()) / 1000.0,
	}, nil
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

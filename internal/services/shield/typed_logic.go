package shield

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
)

type createProtectionRequest struct {
	Name        string `json:"Name"`
	ResourceArn string `json:"ResourceArn"`
}

type createProtectionResponse struct {
	ProtectionId string `json:"ProtectionId"`
}

type deleteProtectionRequest struct {
	ProtectionId string `json:"ProtectionId"`
}

type describeProtectionRequest struct {
	ProtectionId string `json:"ProtectionId"`
	ResourceArn  string `json:"ResourceArn"`
}

type describeProtectionResponse struct {
	Protection *Protection `json:"Protection"`
}

type listProtectionsRequest struct{}

type listProtectionsResponse struct {
	Protections []*Protection `json:"Protections"`
}

type describeSubscriptionResponse struct {
	Subscription subscriptionWire `json:"Subscription"`
}

type subscriptionWire struct {
	StartTime               float64 `json:"StartTime"`
	TimeCommitmentInSeconds int     `json:"TimeCommitmentInSeconds"`
	AutoRenew               string  `json:"AutoRenew"`
	SubscriptionState       string  `json:"SubscriptionState"`
}

func (h *Handler) describeSubscriptionTyped(ctx context.Context, req *struct{}) (*describeSubscriptionResponse, *protocol.AWSError) {
	return &describeSubscriptionResponse{
		Subscription: subscriptionWire{
			StartTime:               1000000000.0,
			TimeCommitmentInSeconds: 31536000,
			AutoRenew:               "ENABLED",
			SubscriptionState:       "ACTIVE",
		},
	}, nil
}

func (h *Handler) createProtectionTyped(ctx context.Context, req *createProtectionRequest) (*createProtectionResponse, *protocol.AWSError) {
	if req.Name == "" || req.ResourceArn == "" {
		return nil, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Name and ResourceArn are required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	p := &Protection{
		ID:          uuid.NewString(),
		Name:        req.Name,
		ResourceArn: req.ResourceArn,
	}
	if err := h.store.putProtection(ctx, p); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createProtectionResponse{ProtectionId: p.ID}, nil
}

func (h *Handler) listProtectionsTyped(ctx context.Context, req *listProtectionsRequest) (*listProtectionsResponse, *protocol.AWSError) {
	protections, err := h.store.listProtections(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listProtectionsResponse{Protections: protections}, nil
}

func (h *Handler) deleteProtectionTyped(ctx context.Context, req *deleteProtectionRequest) (*struct{}, *protocol.AWSError) {
	if req.ProtectionId == "" {
		return nil, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "ProtectionId is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if _, found := h.store.getProtection(ctx, req.ProtectionId); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Protection %s not found", req.ProtectionId),
			HTTPStatus: http.StatusNotFound,
		}
	}
	if err := h.store.deleteProtection(ctx, req.ProtectionId); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) describeProtectionTyped(ctx context.Context, req *describeProtectionRequest) (*describeProtectionResponse, *protocol.AWSError) {
	if req.ProtectionId != "" {
		p, found := h.store.getProtection(ctx, req.ProtectionId)
		if !found {
			return nil, &protocol.AWSError{
				Code:       "ResourceNotFoundException",
				Message:    fmt.Sprintf("Protection %s not found", req.ProtectionId),
				HTTPStatus: http.StatusNotFound,
			}
		}
		return &describeProtectionResponse{Protection: p}, nil
	}
	if req.ResourceArn != "" {
		all, err := h.store.listProtections(ctx)
		if err != nil {
			return nil, protocol.ErrInternalError
		}
		for _, p := range all {
			if p.ResourceArn == req.ResourceArn {
				return &describeProtectionResponse{Protection: p}, nil
			}
		}
	}
	return nil, &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "Protection not found",
		HTTPStatus: http.StatusNotFound,
	}
}

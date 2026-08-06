package shield

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds Shield handler dependencies.
type Handler struct {
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
	store   *shieldStore
}

func newHandler(st state.Store) *Handler {
	h := &Handler{store: newShieldStore(st)}
	h.initOps()
	return h
}

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"DescribeSubscription": h.describeSubscription,
		"CreateProtection":     h.createProtection,
		"ListProtections":      h.listProtections,
		"DeleteProtection":     h.deleteProtection,
		"DescribeProtection":   h.describeProtection,
		"TagResource":          h.tagResource,
		"UntagResource":        h.untagResource,
		"ListTagsForResource":  h.listTagsForResource,
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) describeSubscription(w http.ResponseWriter, r *http.Request) {
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"Subscription": map[string]any{
			"StartTime":               1000000000.0,
			"TimeCommitmentInSeconds": 31536000,
			"AutoRenew":               "ENABLED",
			"SubscriptionState":       "ACTIVE",
		},
	})
}

func (h *Handler) createProtection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"Name"`
		ResourceArn string `json:"ResourceArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.ResourceArn == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Name and ResourceArn are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	p := &Protection{
		ID:          uuid.NewString(),
		Name:        req.Name,
		ResourceArn: req.ResourceArn,
	}
	if err := h.store.putProtection(r.Context(), p); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ProtectionId": p.ID,
	})
}

func (h *Handler) listProtections(w http.ResponseWriter, r *http.Request) {
	protections, err := h.store.listProtections(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"Protections": protections,
	})
}

func (h *Handler) deleteProtection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProtectionId string `json:"ProtectionId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ProtectionId == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "ProtectionId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if _, found := h.store.getProtection(r.Context(), req.ProtectionId); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Protection %s not found", req.ProtectionId),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	if err := h.store.deleteProtection(r.Context(), req.ProtectionId); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) describeProtection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProtectionId string `json:"ProtectionId"`
		ResourceArn  string `json:"ResourceArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ProtectionId != "" {
		p, found := h.store.getProtection(r.Context(), req.ProtectionId)
		if !found {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "ResourceNotFoundException",
				Message:    fmt.Sprintf("Protection %s not found", req.ProtectionId),
				HTTPStatus: http.StatusNotFound,
			})
			return
		}
		protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Protection": p})
		return
	}
	if req.ResourceArn != "" {
		all, err := h.store.listProtections(r.Context())
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInternalError)
			return
		}
		for _, p := range all {
			if p.ResourceArn == req.ResourceArn {
				protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Protection": p})
				return
			}
		}
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "Protection not found",
		HTTPStatus: http.StatusNotFound,
	})
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string      `json:"ResourceARN"`
		Tags        []shieldTag `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ResourceARN == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "ResourceARN is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	pid, aerr := protectionIDFromARN(req.ResourceARN)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ctx := r.Context()
	p, found := h.store.getProtection(ctx, pid)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: fmt.Sprintf("Protection %s not found", pid),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	tags := p.GetTags()
	if tags == nil {
		tags = map[string]string{}
	}
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}
	p.SetTags(tags)
	if err := h.store.putProtection(ctx, p); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string   `json:"ResourceARN"`
		TagKeys     []string `json:"TagKeys"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ResourceARN == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "ResourceARN is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	pid, aerr := protectionIDFromARN(req.ResourceARN)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ctx := r.Context()
	p, found := h.store.getProtection(ctx, pid)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: fmt.Sprintf("Protection %s not found", pid),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	tags := p.GetTags()
	if tags != nil {
		for _, k := range req.TagKeys {
			delete(tags, k)
		}
		p.SetTags(tags)
	}
	if err := h.store.putProtection(ctx, p); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string `json:"ResourceARN"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ResourceARN == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "ResourceARN is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	pid, aerr := protectionIDFromARN(req.ResourceARN)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ctx := r.Context()
	p, found := h.store.getProtection(ctx, pid)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: fmt.Sprintf("Protection %s not found", pid),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	tagList := shieldTagsToList(p.GetTags())
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Tags": tagList})
}

type shieldTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func protectionIDFromARN(arn string) (string, *protocol.AWSError) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", &protocol.AWSError{
			Code: "InvalidParameterException", Message: "Invalid ResourceARN format",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	resource := parts[5]
	if !strings.HasPrefix(resource, "protection/") {
		return "", &protocol.AWSError{
			Code: "InvalidParameterException", Message: "ResourceARN must be a protection ARN",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return strings.TrimPrefix(resource, "protection/"), nil
}

func shieldTagsToList(tags map[string]string) []shieldTag {
	list := make([]shieldTag, 0, len(tags))
	for k, v := range tags {
		list = append(list, shieldTag{Key: k, Value: v})
	}
	return list
}

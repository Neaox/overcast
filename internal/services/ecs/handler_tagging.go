package ecs

// handler_tagging.go — TagResource, UntagResource, ListTagsForResource handlers.

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

var ecsTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterException",
	InvalidCode:     "InvalidParameterException",
	ExceededMessage: "Tag key list exceeds maximum tag limit",
}

// TagResource handles AmazonEC2ContainerServiceV20141113.TagResource.
func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
		Tags        []Tag  `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ResourceArn == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "resourceArn must not be null",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()
	region := h.store.region(ctx)
	pairs := make([]serviceutil.TagPair, len(req.Tags))
	for i, t := range req.Tags {
		pairs[i] = serviceutil.TagPair{Key: t.Key, Value: t.Value}
	}
	if _, aerr := serviceutil.ApplyTagsToStore(ctx, ecsTagCfg, nsTags, serviceutil.RegionKey(region, req.ResourceArn), pairs, h.store.store); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{}, "application/x-amz-json-1.1")
}

// UntagResource handles AmazonEC2ContainerServiceV20141113.UntagResource.
func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"resourceArn"`
		TagKeys     []string `json:"tagKeys"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ResourceArn == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "resourceArn must not be null",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()
	region := h.store.region(ctx)
	if _, aerr := serviceutil.RemoveTagsFromStore(ctx, nsTags, serviceutil.RegionKey(region, req.ResourceArn), req.TagKeys, h.store.store); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{}, "application/x-amz-json-1.1")
}

// ListTagsForResource handles AmazonEC2ContainerServiceV20141113.ListTagsForResource.
func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ResourceArn == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "resourceArn must not be null",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ctx := r.Context()
	region := h.store.region(ctx)
	existing, aerr := serviceutil.TagsFromStore(ctx, h.store.store, nsTags, serviceutil.RegionKey(region, req.ResourceArn))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	tags := make([]Tag, 0, len(existing))
	for k, v := range existing {
		tags = append(tags, Tag{Key: k, Value: v})
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"tags": tags}, "application/x-amz-json-1.1")
}

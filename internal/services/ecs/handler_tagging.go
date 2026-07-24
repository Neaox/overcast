package ecs

// handler_tagging.go — TagResource, UntagResource, ListTagsForResource handlers.

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

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

	existing, aerr := h.store.getTags(r.Context(), req.ResourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing == nil {
		existing = make(map[string]string)
	}
	for _, t := range req.Tags {
		existing[t.Key] = t.Value
	}
	if aerr := h.store.putTags(r.Context(), req.ResourceArn, existing); aerr != nil {
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

	existing, aerr := h.store.getTags(r.Context(), req.ResourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing != nil {
		for _, k := range req.TagKeys {
			delete(existing, k)
		}
		if aerr := h.store.putTags(r.Context(), req.ResourceArn, existing); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
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

	existing, aerr := h.store.getTags(r.Context(), req.ResourceArn)
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

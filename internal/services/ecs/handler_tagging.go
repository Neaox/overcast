package ecs

// handler_tagging.go — TagResource, UntagResource, ListTagsForResource handlers.

import (
	"context"
	"net/http"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

var ecsTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterException",
	InvalidCode:     "InvalidParameterException",
	ExceededMessage: "Tag key list exceeds maximum tag limit",
}

// storeTaskDefinitionTags records the tags a RegisterTaskDefinition call
// carried, against the revision's own ARN.
//
// Tags belong to the revision, not the family: AWS tags each revision as it is
// registered, and a later revision that names no tags has none rather than
// inheriting its predecessor's. Registering is also the only way a task
// definition can be tagged at creation, which is what CloudFormation and CDK
// use — TagResource is a separate call they do not make for it.
func (h *Handler) storeTaskDefinitionTags(ctx context.Context, arn string, tags []Tag) *protocol.AWSError {
	if len(tags) == 0 {
		return nil
	}
	pairs := make([]serviceutil.TagPair, len(tags))
	for i, t := range tags {
		pairs[i] = serviceutil.TagPair{Key: t.Key, Value: t.Value}
	}
	key := serviceutil.RegionKey(h.store.region(ctx), arn)
	_, aerr := serviceutil.ApplyTagsToStore(ctx, ecsTagCfg, nsTags, key, pairs, h.store.store)
	return aerr
}

// taskDefinitionTags reads the tags stored against a task definition ARN.
//
// A read failure yields no tags rather than failing the placement — a task
// whose tags could not be read should still run — but it is logged, because
// the visible symptom is a hot-reload tag that appears to be ignored, and
// leaving the user to guess at that is the failure mode this whole feature
// tries not to have.
func (h *Handler) taskDefinitionTags(ctx context.Context, arn string) map[string]string {
	if arn == "" {
		return nil
	}
	key := serviceutil.RegionKey(h.store.region(ctx), arn)
	tags, aerr := serviceutil.TagsFromStore(ctx, h.store.store, nsTags, key)
	if aerr != nil {
		h.log.Warn("ecs: could not read task definition tags — any tag-driven behaviour is skipped for this task",
			zap.String("task_definition", arn), zap.String("error", aerr.Message))
		return nil
	}
	return tags
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

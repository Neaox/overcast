package ecs

// handler_tagging.go — TagResource, UntagResource, ListTagsForResource handlers.

import (
	"context"
	"net/http"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

var ecsTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterException",
	InvalidCode:     "InvalidParameterException",
	ExceededMessage: "Tag key list exceeds maximum tag limit",
}

// tagStore is the namespace ECS resource tags live in.
func (h *Handler) tagStore() *serviceutil.NSStore {
	return &serviceutil.NSStore{Store: h.store.store, NS: nsTags}
}

// tagKey scopes a resource ARN to the caller's region, which is how every tag
// in the namespace is keyed.
func (h *Handler) tagKey(ctx context.Context, arn string) string {
	return serviceutil.RegionKey(h.store.region(ctx), arn)
}

// tagMap converts the wire's tag list to the map the shared helpers speak.
func tagMap(tags []Tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}
	return out
}

// tagList renders a tag map into the wire's tag list, ordered by key.
func tagList(tags map[string]string) []Tag {
	return serviceutil.TagElements(tags, func(k, v string) Tag {
		return Tag{Key: k, Value: v}
	})
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
	_, aerr := serviceutil.ApplyStoreTags(ctx, h.tagStore(), h.tagKey(ctx, arn), tagMap(tags), ecsTagCfg)
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
	tags, aerr := h.tagStore().Load(ctx, h.tagKey(ctx, arn))
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
	if _, aerr := serviceutil.ApplyStoreTags(ctx, h.tagStore(), h.tagKey(ctx, req.ResourceArn), tagMap(req.Tags), ecsTagCfg); aerr != nil {
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
	if _, aerr := serviceutil.RemoveStoreTags(ctx, h.tagStore(), h.tagKey(ctx, req.ResourceArn), req.TagKeys); aerr != nil {
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
	existing, aerr := h.tagStore().Load(ctx, h.tagKey(ctx, req.ResourceArn))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"tags": tagList(existing)}, "application/x-amz-json-1.1")
}

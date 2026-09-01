package eks

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

var eksTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterException",
	InvalidCode:     "InvalidParameterException",
	ExceededMessage: "Tag key list exceeds maximum tag limit",
}

func eksClusterFromResourceARN(arn string) (region, clusterName string, ok bool) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[2] != "eks" {
		return "", "", false
	}

	region = strings.TrimSpace(parts[3])
	resource := strings.TrimSpace(parts[5])
	if region == "" || resource == "" {
		return "", "", false
	}

	trimAndTakeCluster := func(prefix string) (string, bool) {
		rest := strings.TrimPrefix(resource, prefix)
		if rest == resource {
			return "", false
		}
		segments := strings.Split(rest, "/")
		if len(segments) == 0 || strings.TrimSpace(segments[0]) == "" {
			return "", false
		}
		return segments[0], true
	}

	if c, ok := trimAndTakeCluster("cluster/"); ok {
		return region, c, true
	}
	if c, ok := trimAndTakeCluster("nodegroup/"); ok {
		return region, c, true
	}
	if c, ok := trimAndTakeCluster("fargateprofile/"); ok {
		return region, c, true
	}
	if c, ok := trimAndTakeCluster("addon/"); ok {
		return region, c, true
	}
	if c, ok := trimAndTakeCluster("identityproviderconfig/"); ok {
		return region, c, true
	}
	if c, ok := trimAndTakeCluster("access-entry/"); ok {
		return region, c, true
	}
	if c, ok := trimAndTakeCluster("podidentityassociation/"); ok {
		return region, c, true
	}

	return "", "", false
}

func (s *Service) requireAccessibleTagResource(w http.ResponseWriter, r *http.Request, arn string) bool {
	region, clusterName, ok := eksClusterFromResourceARN(arn)
	if !ok {
		return true
	}
	_, accessible := s.requireAccessibleCluster(w, r, region, clusterName)
	return accessible
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	arnParam := chi.URLParam(r, "resourceArn")
	arn, err := url.PathUnescape(arnParam)
	if err != nil {
		arn = arnParam
	}
	if !s.requireAccessibleTagResource(w, r, arn) {
		return
	}
	ctx := r.Context()
	tags, aerr := s.tagStore().Load(ctx, tagKey(arn))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	arnParam := chi.URLParam(r, "resourceArn")
	arn, err := url.PathUnescape(arnParam)
	if err != nil {
		arn = arnParam
	}
	if !s.requireAccessibleTagResource(w, r, arn) {
		return
	}
	ctx := r.Context()
	var req struct {
		Tags map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.Tags) == 0 {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "tags map must not be empty",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if _, aerr := serviceutil.ApplyStoreTags(ctx, s.tagStore(), tagKey(arn), req.Tags, eksTagCfg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	arnParam := chi.URLParam(r, "resourceArn")
	arn, err := url.PathUnescape(arnParam)
	if err != nil {
		arn = arnParam
	}
	if !s.requireAccessibleTagResource(w, r, arn) {
		return
	}
	ctx := r.Context()
	keys := r.URL.Query()["tagKeys"]
	if len(keys) == 0 {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "at least one tagKeys query parameter is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if _, aerr := serviceutil.RemoveStoreTags(ctx, s.tagStore(), tagKey(arn), keys); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// tagStore is the namespace EKS resource tags live in, keyed by resource ARN.
//
// Every tag path in the service goes through it. EKS used to reach this one
// namespace through two different abstractions, which is how a service ends up
// with two answers to "where are the tags".
func (s *Service) tagStore() *serviceutil.NSStore {
	return &serviceutil.NSStore{Store: s.store, NS: nsTags}
}

// putInlineTags writes an inline tags map (from a create-resource request body)
// into the tag store under the resource ARN. It is a no-op when tags is nil or empty.
func (s *Service) putInlineTags(ctx context.Context, arn string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	if aerr := s.tagStore().Save(ctx, tagKey(arn), tags); aerr != nil {
		return aerr
	}
	return nil
}

// readTagsForARN loads the tag map for an ARN from the tag store.
// Returns an empty map when no tags are found.
func (s *Service) readTagsForARN(ctx context.Context, arn string) map[string]string {
	tags, _ := s.tagStore().Load(ctx, tagKey(arn))
	return tags
}

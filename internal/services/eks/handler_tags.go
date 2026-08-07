package eks

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
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
	tags, aerr := serviceutil.TagsFromStore(ctx, s.store, nsTags, tagKey(arn))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if tags == nil {
		tags = map[string]string{}
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
	pairs := make([]serviceutil.TagPair, 0, len(req.Tags))
	for k, v := range req.Tags {
		pairs = append(pairs, serviceutil.TagPair{Key: k, Value: v})
	}
	if _, aerr := serviceutil.ApplyTagsToStore(ctx, eksTagCfg, nsTags, tagKey(arn), pairs, s.store); aerr != nil {
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
	if _, aerr := serviceutil.RemoveTagsFromStore(ctx, nsTags, tagKey(arn), keys, s.store); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// putInlineTags writes an inline tags map (from a create-resource request body)
// into the tag store under the resource ARN. It is a no-op when tags is nil or empty.
func (s *Service) putInlineTags(ctx context.Context, arn string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	tagStore := &serviceutil.NSStore{Store: s.store, NS: nsTags}
	aerr := tagStore.Save(ctx, arn, tags)
	if aerr != nil {
		return aerr
	}
	return nil
}

// readTagsForARN loads the tag map for an ARN from the tag store.
// Returns an empty map when no tags are found.
func (s *Service) readTagsForARN(ctx context.Context, arn string) map[string]string {
	tagStore := &serviceutil.NSStore{Store: s.store, NS: nsTags}
	tags, _ := tagStore.Load(ctx, arn)
	return tags
}

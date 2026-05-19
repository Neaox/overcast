package eks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

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
	raw, found, err := s.store.Get(ctx, nsTags, tagKey(arn))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"tags": map[string]string{}})
		return
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
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
	// Merge with existing tags.
	existing := map[string]string{}
	if raw, found, err := s.store.Get(ctx, nsTags, tagKey(arn)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if found {
		if err := json.Unmarshal([]byte(raw), &existing); err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInternalError)
			return
		}
	}
	for k, v := range req.Tags {
		existing[k] = v
	}
	merged, err := json.Marshal(existing)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.store.Set(ctx, nsTags, tagKey(arn), string(merged)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
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
	raw, found, err := s.store.Get(ctx, nsTags, tagKey(arn))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
		return
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	for _, k := range keys {
		delete(tags, k)
	}
	updated, err := json.Marshal(tags)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.store.Set(ctx, nsTags, tagKey(arn), string(updated)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
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
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsTags, tagKey(arn), string(raw))
}

// readTagsForARN loads the tag map for an ARN from the tag store.
// Returns an empty map when no tags are found.
func (s *Service) readTagsForARN(ctx context.Context, arn string) map[string]string {
	raw, found, err := s.store.Get(ctx, nsTags, tagKey(arn))
	if err != nil || !found {
		return nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}

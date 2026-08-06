package cognito

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

var cognitoTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterException",
	InvalidCode:     "InvalidParameterException",
	ExceededMessage: "Tag count exceeded the maximum of 50 tags per resource.",
}

// tagResource handles Cognito TagResource.
func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string            `json:"ResourceArn"`
		Tags        map[string]string `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	poolID := extractPoolIDFromARN(req.ResourceArn)
	pool, ok := s.requirePool(r.Context(), w, r, poolID)
	if !ok {
		return
	}

	if pool.Tags == nil {
		pool.Tags = make(map[string]string)
	}
	for k, v := range req.Tags {
		pool.Tags[k] = v
	}

	if aerr := serviceutil.ValidateTags(cognitoTagCfg, pool.Tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if err := s.savePool(r.Context(), pool); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// untagResource handles Cognito UntagResource.
func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"ResourceArn"`
		TagKeys     []string `json:"TagKeys"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	poolID := extractPoolIDFromARN(req.ResourceArn)
	pool, ok := s.requirePool(r.Context(), w, r, poolID)
	if !ok {
		return
	}

	for _, k := range req.TagKeys {
		delete(pool.Tags, k)
	}

	if err := s.savePool(r.Context(), pool); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// listTagsForResource handles Cognito ListTagsForResource.
func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	poolID := extractPoolIDFromARN(req.ResourceArn)
	pool, ok := s.requirePool(r.Context(), w, r, poolID)
	if !ok {
		return
	}

	out := pool.Tags
	if out == nil {
		out = make(map[string]string)
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{"Tags": out})
}

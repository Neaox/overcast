package cognito

import (
	"context"
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

	if aerr := serviceutil.ApplyInlineTags(r.Context(), poolID, req.Tags, cognitoTagCfg,
		func(ctx context.Context, id string) (*UserPool, *protocol.AWSError) {
			pool, err := s.loadPool(ctx, id)
			if err != nil {
				return nil, protocol.Wrap(protocol.ErrInternalError, err)
			}
			if pool == nil {
				return nil, errPoolNotFound(id)
			}
			return pool, nil
		},
		func(ctx context.Context, pool *UserPool) *protocol.AWSError {
			if err := s.savePool(ctx, pool); err != nil {
				return protocol.Wrap(protocol.ErrInternalError, err)
			}
			return nil
		},
	); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
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

	if aerr := serviceutil.RemoveInlineTags(r.Context(), poolID, req.TagKeys,
		func(ctx context.Context, id string) (*UserPool, *protocol.AWSError) {
			pool, err := s.loadPool(ctx, id)
			if err != nil {
				return nil, protocol.Wrap(protocol.ErrInternalError, err)
			}
			if pool == nil {
				return nil, errPoolNotFound(id)
			}
			return pool, nil
		},
		func(ctx context.Context, pool *UserPool) *protocol.AWSError {
			if err := s.savePool(ctx, pool); err != nil {
				return protocol.Wrap(protocol.ErrInternalError, err)
			}
			return nil
		},
	); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
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

	tags, aerr := serviceutil.ListInlineTags(r.Context(), poolID,
		func(ctx context.Context, id string) (*UserPool, *protocol.AWSError) {
			pool, err := s.loadPool(ctx, id)
			if err != nil {
				return nil, protocol.Wrap(protocol.ErrInternalError, err)
			}
			if pool == nil {
				return nil, errPoolNotFound(id)
			}
			return pool, nil
		},
	)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{"Tags": tags})
}

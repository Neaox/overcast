package lambda

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

var lambdaTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterValueException",
	InvalidCode:     "InvalidParameterValueException",
	ExceededMessage: "Tag count exceeded the maximum of 50 tags per resource.",
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	rawARN := chi.URLParam(r, "ResourceARN")
	arn, err := url.PathUnescape(rawARN)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidParameterValueException", Message: "Invalid resource ARN.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	fnName := functionNameFromARN(arn)
	if fnName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Function not found: " + arn,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		Tags map[string]string `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if aerr := serviceutil.ApplyInlineTags(r.Context(), fnName, req.Tags, lambdaTagCfg,
		func(ctx context.Context, name string) (*Function, *protocol.AWSError) {
			fn, aerr := h.ls.getFunction(ctx, name)
			if aerr != nil {
				return nil, aerr
			}
			if fn == nil {
				return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Function not found: " + arn, HTTPStatus: http.StatusNotFound}
			}
			return fn, nil
		},
		func(ctx context.Context, fn *Function) *protocol.AWSError { return h.ls.putFunction(ctx, fn) },
	); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	rawARN := chi.URLParam(r, "ResourceARN")
	arn, err := url.PathUnescape(rawARN)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidParameterValueException", Message: "Invalid resource ARN.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	fnName := functionNameFromARN(arn)
	if fnName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Function not found: " + arn,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	tagKeys := r.URL.Query()["tagKeys"]

	if aerr := serviceutil.RemoveInlineTags(r.Context(), fnName, tagKeys,
		func(ctx context.Context, name string) (*Function, *protocol.AWSError) {
			fn, aerr := h.ls.getFunction(ctx, name)
			if aerr != nil {
				return nil, aerr
			}
			if fn == nil {
				return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Function not found: " + arn, HTTPStatus: http.StatusNotFound}
			}
			return fn, nil
		},
		func(ctx context.Context, fn *Function) *protocol.AWSError { return h.ls.putFunction(ctx, fn) },
	); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	rawARN := chi.URLParam(r, "ResourceARN")
	arn, err := url.PathUnescape(rawARN)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "InvalidParameterValueException", Message: "Invalid resource ARN.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	fnName := functionNameFromARN(arn)
	if fnName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Function not found: " + arn,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	tags, aerr := serviceutil.ListInlineTags(r.Context(), fnName,
		func(ctx context.Context, name string) (*Function, *protocol.AWSError) {
			fn, aerr := h.ls.getFunction(ctx, name)
			if aerr != nil {
				return nil, aerr
			}
			if fn == nil {
				return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Function not found: " + arn, HTTPStatus: http.StatusNotFound}
			}
			return fn, nil
		},
	)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Tags map[string]string `json:"Tags"`
	}{Tags: tags})
}

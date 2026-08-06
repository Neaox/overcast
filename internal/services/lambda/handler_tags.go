package lambda

import (
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

	fn, aerr := h.ls.getFunction(r.Context(), fnName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
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

	if fn.Tags == nil {
		fn.Tags = make(map[string]string)
	}
	for k, v := range req.Tags {
		fn.Tags[k] = v
	}

	if aerr := serviceutil.ValidateTags(lambdaTagCfg, fn.Tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.ls.putFunction(r.Context(), fn); aerr != nil {
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

	fn, aerr := h.ls.getFunction(r.Context(), fnName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Function not found: " + arn,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	tagKeys := r.URL.Query()["tagKeys"]
	for _, k := range tagKeys {
		delete(fn.Tags, k)
	}

	if aerr := h.ls.putFunction(r.Context(), fn); aerr != nil {
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

	fn, aerr := h.ls.getFunction(r.Context(), fnName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Function not found: " + arn,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	tags := fn.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Tags map[string]string `json:"Tags"`
	}{Tags: tags})
}

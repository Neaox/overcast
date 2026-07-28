package lambda

// handler_code_signing.go — the code signing configuration resource.
//
//   - CreateCodeSigningConfig          POST   /2020-04-22/code-signing-configs/
//   - ListCodeSigningConfigs           GET    /2020-04-22/code-signing-configs/
//   - GetCodeSigningConfig             GET    /2020-04-22/code-signing-configs/{arn}
//   - UpdateCodeSigningConfig          PUT    /2020-04-22/code-signing-configs/{arn}
//   - DeleteCodeSigningConfig          DELETE /2020-04-22/code-signing-configs/{arn}
//   - ListFunctionsByCodeSigningConfig GET    /2020-04-22/code-signing-configs/{arn}/functions
//
// Overcast stores these faithfully but never acts on them: validating a
// signature is a security control, and Overcast is not a security boundary.
// What matters here is that a configuration created by CDK or the CLI reads
// back exactly as written, and that a function referencing one is rejected
// when the configuration does not exist — which is the behaviour deploy
// tooling actually depends on.

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

// cscIDAlphabet matches AWS's ARN pattern, which ends in csc-[a-z0-9]{17}.
const cscIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

const cscIDLength = 17

// defaultUntrustedArtifactOnDeployment is AWS's documented default when the
// caller does not specify a policy.
const defaultUntrustedArtifactOnDeployment = "Warn"

// newCodeSigningConfigID returns a fresh "csc-…" id in AWS's shape.
func newCodeSigningConfigID() string {
	buf := make([]byte, cscIDLength)
	// crypto/rand.Read never returns an error on the platforms Go supports.
	_, _ = rand.Read(buf)
	out := make([]byte, cscIDLength)
	for i, b := range buf {
		out[i] = cscIDAlphabet[int(b)%len(cscIDAlphabet)]
	}
	return "csc-" + string(out)
}

// codeSigningConfigIDFromARN extracts the "csc-…" id from an ARN.
//
// The ARN is the resource's own identifier in every path, and the AWS SDKs
// percent-encode it when they build the URL — the colons arrive as %3A. chi
// hands back the raw path segment, so unescape before looking for the last
// colon, or the whole encoded string is mistaken for the id and nothing is
// ever found. A bare "csc-…" id is accepted too, which keeps the handlers
// forgiving of SDKs that do not encode.
func codeSigningConfigIDFromARN(s string) string {
	if s == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(s); err == nil {
		s = decoded
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return s
}

type codeSigningConfigRequest struct {
	Description         string               `json:"Description,omitempty"`
	AllowedPublishers   *AllowedPublishers   `json:"AllowedPublishers,omitempty"`
	CodeSigningPolicies *CodeSigningPolicies `json:"CodeSigningPolicies,omitempty"`
}

type codeSigningConfigEnvelope struct {
	CodeSigningConfig *CodeSigningConfig `json:"CodeSigningConfig"`
}

type listCodeSigningConfigsResponse struct {
	CodeSigningConfigs []*CodeSigningConfig `json:"CodeSigningConfigs"`
	NextMarker         *string              `json:"NextMarker"`
}

type listFunctionsByCodeSigningConfigResponse struct {
	FunctionArns []string `json:"FunctionArns"`
	NextMarker   *string  `json:"NextMarker"`
}

func errCodeSigningConfigNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "Code signing config not found: " + arn,
		HTTPStatus: http.StatusNotFound,
	}
}

// validateAllowedPublishers enforces AWS's 1..20 bound on the profile list.
func validateAllowedPublishers(ap *AllowedPublishers) *protocol.AWSError {
	if ap == nil || len(ap.SigningProfileVersionArns) == 0 {
		return &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "AllowedPublishers must specify at least one signing profile version ARN",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if len(ap.SigningProfileVersionArns) > 20 {
		return &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "AllowedPublishers accepts at most 20 signing profile version ARNs",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

// validateCodeSigningPolicies rejects any value outside AWS's enum.
func validateCodeSigningPolicies(p *CodeSigningPolicies) *protocol.AWSError {
	if p == nil || p.UntrustedArtifactOnDeployment == "" {
		return nil
	}
	switch p.UntrustedArtifactOnDeployment {
	case "Warn", "Enforce":
		return nil
	}
	return &protocol.AWSError{
		Code:       "InvalidParameterValueException",
		Message:    "UntrustedArtifactOnDeployment must be one of: Warn, Enforce",
		HTTPStatus: http.StatusBadRequest,
	}
}

// CreateCodeSigningConfig handles POST /2020-04-22/code-signing-configs/.
func (h *Handler) CreateCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	var req codeSigningConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}
	if aerr := validateAllowedPublishers(req.AllowedPublishers); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := validateCodeSigningPolicies(req.CodeSigningPolicies); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	id := newCodeSigningConfigID()
	policies := req.CodeSigningPolicies
	if policies == nil || policies.UntrustedArtifactOnDeployment == "" {
		policies = &CodeSigningPolicies{UntrustedArtifactOnDeployment: defaultUntrustedArtifactOnDeployment}
	}
	csc := &CodeSigningConfig{
		CodeSigningConfigID:  id,
		CodeSigningConfigArn: protocol.LambdaCodeSigningConfigARN(region, h.cfg.AccountID, id),
		Description:          req.Description,
		AllowedPublishers:    *req.AllowedPublishers,
		CodeSigningPolicies:  policies,
		LastModified:         h.clk.Now().UTC().Format(codeSigningTimeFormat),
	}
	if aerr := h.ls.putCodeSigningConfig(r.Context(), csc); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(codeSigningConfigEnvelope{CodeSigningConfig: csc})
}

// codeSigningTimeFormat is the timestamp format AWS uses for LastModified on
// this resource — ISO-8601 with milliseconds.
const codeSigningTimeFormat = "2006-01-02T15:04:05.000-0700"

// lookupCodeSigningConfig resolves the {arn} path parameter, writing the
// AWS-shaped 404 and returning nil when there is no such configuration.
func (h *Handler) lookupCodeSigningConfig(w http.ResponseWriter, r *http.Request) *CodeSigningConfig {
	arn := chi.URLParam(r, "arn")
	csc, aerr := h.ls.getCodeSigningConfig(r.Context(), codeSigningConfigIDFromARN(arn))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return nil
	}
	if csc == nil {
		protocol.WriteJSONError(w, r, errCodeSigningConfigNotFound(arn))
		return nil
	}
	return csc
}

// GetCodeSigningConfig handles GET /2020-04-22/code-signing-configs/{arn}.
func (h *Handler) GetCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	csc := h.lookupCodeSigningConfig(w, r)
	if csc == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(codeSigningConfigEnvelope{CodeSigningConfig: csc})
}

// UpdateCodeSigningConfig handles PUT /2020-04-22/code-signing-configs/{arn}.
// Every field is optional; omitted fields keep their stored value.
func (h *Handler) UpdateCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	csc := h.lookupCodeSigningConfig(w, r)
	if csc == nil {
		return
	}
	var req codeSigningConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}
	if req.AllowedPublishers != nil {
		if aerr := validateAllowedPublishers(req.AllowedPublishers); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		csc.AllowedPublishers = *req.AllowedPublishers
	}
	if aerr := validateCodeSigningPolicies(req.CodeSigningPolicies); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if req.CodeSigningPolicies != nil && req.CodeSigningPolicies.UntrustedArtifactOnDeployment != "" {
		csc.CodeSigningPolicies = req.CodeSigningPolicies
	}
	if req.Description != "" {
		csc.Description = req.Description
	}
	csc.LastModified = h.clk.Now().UTC().Format(codeSigningTimeFormat)

	if aerr := h.ls.putCodeSigningConfig(r.Context(), csc); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(codeSigningConfigEnvelope{CodeSigningConfig: csc})
}

// DeleteCodeSigningConfig handles DELETE /2020-04-22/code-signing-configs/{arn}.
// A configuration still referenced by a function cannot be deleted — AWS
// answers ResourceConflictException, and deploy tooling relies on that to
// order teardown correctly.
func (h *Handler) DeleteCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	csc := h.lookupCodeSigningConfig(w, r)
	if csc == nil {
		return
	}
	users, aerr := h.functionsUsingCodeSigningConfig(r, csc.CodeSigningConfigArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if len(users) > 0 {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceConflictException",
			Message:    "The code signing configuration is in use by one or more functions and cannot be deleted",
			HTTPStatus: http.StatusConflict,
		})
		return
	}
	if aerr := h.ls.deleteCodeSigningConfig(r.Context(), csc.CodeSigningConfigID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCodeSigningConfigs handles GET /2020-04-22/code-signing-configs/.
func (h *Handler) ListCodeSigningConfigs(w http.ResponseWriter, r *http.Request) {
	configs, aerr := h.ls.listCodeSigningConfigs(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].CodeSigningConfigID < configs[j].CodeSigningConfigID
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listCodeSigningConfigsResponse{CodeSigningConfigs: configs})
}

// ListFunctionsByCodeSigningConfig handles
// GET /2020-04-22/code-signing-configs/{arn}/functions.
func (h *Handler) ListFunctionsByCodeSigningConfig(w http.ResponseWriter, r *http.Request) {
	csc := h.lookupCodeSigningConfig(w, r)
	if csc == nil {
		return
	}
	arns, aerr := h.functionsUsingCodeSigningConfig(r, csc.CodeSigningConfigArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	sort.Strings(arns)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listFunctionsByCodeSigningConfigResponse{FunctionArns: arns})
}

// functionsUsingCodeSigningConfig returns the ARNs of functions referencing the
// configuration. Backs both the in-use check on delete and the list operation.
func (h *Handler) functionsUsingCodeSigningConfig(r *http.Request, cscArn string) ([]string, *protocol.AWSError) {
	fns, aerr := h.ls.listFunctions(r.Context())
	if aerr != nil {
		return nil, aerr
	}
	out := make([]string, 0)
	for _, fn := range fns {
		if fn.CodeSigningConfigArn == cscArn {
			out = append(out, fn.ARN)
		}
	}
	return out, nil
}

// codeSigningConfigExists reports whether an ARN names a configuration that
// exists. Used to reject a function referencing a configuration that was never
// created, which is what AWS's CodeSigningConfigNotFoundException means.
func (h *Handler) codeSigningConfigExists(r *http.Request, arn string) (bool, *protocol.AWSError) {
	csc, aerr := h.ls.getCodeSigningConfig(r.Context(), codeSigningConfigIDFromARN(arn))
	if aerr != nil {
		return false, aerr
	}
	return csc != nil, nil
}

func errCodeSigningConfigNotFoundForFunction(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "CodeSigningConfigNotFoundException",
		Message:    "The specified code signing configuration does not exist: " + arn,
		HTTPStatus: http.StatusNotFound,
	}
}

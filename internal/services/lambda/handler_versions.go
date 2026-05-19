package lambda

// handler_versions.go — implemented handlers for Lambda versions and aliases.
//
// Implemented:
//   - PublishVersion              POST   /2015-03-31/functions/{name}/versions
//   - ListVersionsByFunction      GET    /2015-03-31/functions/{name}/versions
//   - CreateAlias                 POST   /2015-03-31/functions/{name}/aliases
//   - GetAlias                    GET    /2015-03-31/functions/{name}/aliases/{aliasName}
//   - ListAliases                 GET    /2015-03-31/functions/{name}/aliases
//   - UpdateAlias                 PUT    /2015-03-31/functions/{name}/aliases/{aliasName}
//   - DeleteAlias                 DELETE /2015-03-31/functions/{name}/aliases/{aliasName}

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
)

// ─── wire types ──────────────────────────────────────────────────────────────

// publishVersionRequest mirrors the AWS PublishVersion request body.
// https://docs.aws.amazon.com/lambda/latest/api/API_PublishVersion.html
type publishVersionRequest struct {
	CodeSha256  string `json:"CodeSha256,omitempty"`
	Description string `json:"Description,omitempty"`
	RevisionId  string `json:"RevisionId,omitempty"`
}

// versionConfigurationResponse is the FunctionConfiguration response shape
// for a published version. It extends the base config with Version.
type versionConfigurationResponse struct {
	functionConfiguration
	Version string `json:"Version"`
}

// listVersionsResponse is the ListVersionsByFunction response envelope.
type listVersionsResponse struct {
	Versions   []versionConfigurationResponse `json:"Versions"`
	NextMarker string                         `json:"NextMarker,omitempty"`
}

// aliasResponse mirrors the AWS AliasConfiguration wire shape.
// https://docs.aws.amazon.com/lambda/latest/api/API_AliasConfiguration.html
type aliasResponse struct {
	AliasArn        string `json:"AliasArn"`
	Name            string `json:"Name"`
	FunctionVersion string `json:"FunctionVersion"`
	Description     string `json:"Description,omitempty"`
	RevisionId      string `json:"RevisionId"`
}

// createAliasRequest mirrors the AWS CreateAlias request body.
type createAliasRequest struct {
	Name            string `json:"Name"`
	FunctionVersion string `json:"FunctionVersion"`
	Description     string `json:"Description,omitempty"`
}

// updateAliasRequest mirrors the AWS UpdateAlias request body.
type updateAliasRequest struct {
	FunctionVersion string `json:"FunctionVersion,omitempty"`
	Description     string `json:"Description,omitempty"`
	RevisionId      string `json:"RevisionId,omitempty"`
}

// listAliasesResponse is the ListAliases response envelope.
type listAliasesResponse struct {
	Aliases    []aliasResponse `json:"Aliases"`
	NextMarker string          `json:"NextMarker,omitempty"`
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// codeSha256 returns a deterministic SHA-256 hex digest for the function's
// deployment package. If no zip is stored we hash the ARN + last-modified as a
// stable stand-in (matches real-ish behaviour for functions with no code zip).
func codeSha256(fn *Function) string {
	h := sha256.New()
	if len(fn.CodeZip) > 0 {
		h.Write(fn.CodeZip)
	} else {
		// Stable placeholder so repeat calls return the same value.
		h.Write([]byte(fn.ARN + fn.LastModified))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// versionARN returns the qualified ARN for a specific version number.
func versionARN(baseARN string, version int) string {
	return fmt.Sprintf("%s:%d", baseARN, version)
}

// aliasARN returns the qualified ARN for an alias.
func aliasARN(baseARN, aliasName string) string {
	return fmt.Sprintf("%s:%s", baseARN, aliasName)
}

// versionToResponse converts a FunctionVersion to the wire response shape.
func versionToResponse(v *FunctionVersion) versionConfigurationResponse {
	cfg := functionToConfig(&v.Function)
	cfg.FunctionArn = versionARN(v.ARN, v.Version)
	// Use the version-level description when set, else the function's.
	if v.Description != "" {
		cfg.Description = v.Description
	}
	// Override with the version's snapshot CodeSha256 (may differ from $LATEST).
	if v.CodeSha256 != "" {
		cfg.CodeSha256 = v.CodeSha256
	}
	return versionConfigurationResponse{
		functionConfiguration: *cfg,
		Version:               strconv.Itoa(v.Version),
	}
}

// aliasToResponse converts a FunctionAlias to the wire response shape.
func aliasToResponse(a *FunctionAlias) aliasResponse {
	return aliasResponse{
		AliasArn:        a.AliasARN,
		Name:            a.Name,
		FunctionVersion: a.FunctionVersion,
		Description:     a.Description,
		RevisionId:      a.RevisionId,
	}
}

// latestVersionResponse builds the synthetic "$LATEST" entry always included in
// ListVersionsByFunction results.
func (h *Handler) latestVersionResponse(fn *Function) versionConfigurationResponse {
	cfg := functionToConfig(fn)
	return versionConfigurationResponse{
		functionConfiguration: *cfg,
		Version:               "$LATEST",
	}
}

// ─── Versions ─────────────────────────────────────────────────────────────────

// PublishVersion handles POST /2015-03-31/functions/{name}/versions.
// https://docs.aws.amazon.com/lambda/latest/api/API_PublishVersion.html
func (h *Handler) PublishVersion(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("publish version", zap.String("function", name))
	ctx := r.Context()

	var req publishVersionRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterValueException",
				Message:    "Invalid request body: " + err.Error(),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
	}

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	versionNum, err := h.ls.nextVersion(ctx, name)
	if err != nil {
		h.log.Error("publish version: next version", zap.String("function", name), zap.Error(err))
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	sha := codeSha256(fn)
	v := &FunctionVersion{
		Function:    *fn,
		Version:     versionNum,
		Description: req.Description,
		CodeSha256:  sha,
	}
	// Stamp the publish time and a fresh revision ID for this version.
	v.LastModified = h.clk.Now().UTC().Format(time.RFC3339)
	v.RevisionId = uuid.NewString()

	if aerr := h.ls.putVersion(ctx, v); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	resp := versionToResponse(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// ListVersionsByFunction handles GET /2015-03-31/functions/{name}/versions.
// https://docs.aws.amazon.com/lambda/latest/api/API_ListVersionsByFunction.html
func (h *Handler) ListVersionsByFunction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("list versions", zap.String("function", name))
	ctx := r.Context()

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	versions, aerr := h.ls.listVersions(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// AWS always includes $LATEST as the first entry.
	out := make([]versionConfigurationResponse, 0, len(versions)+1)
	out = append(out, h.latestVersionResponse(fn))
	for _, v := range versions {
		out = append(out, versionToResponse(v))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listVersionsResponse{Versions: out})
}

// ─── Aliases ──────────────────────────────────────────────────────────────────

// CreateAlias handles POST /2015-03-31/functions/{name}/aliases.
// https://docs.aws.amazon.com/lambda/latest/api/API_CreateAlias.html
func (h *Handler) CreateAlias(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("create alias", zap.String("function", name))
	ctx := r.Context()

	var req createAliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "Invalid request body: " + err.Error(),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if req.Name == "" || req.FunctionVersion == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "Name and FunctionVersion are required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	// Reject duplicate alias names.
	existing, aerr := h.ls.getAlias(ctx, name, req.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceConflictException",
			Message:    fmt.Sprintf("Alias already exists: %s for function %s", req.Name, name),
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	a := &FunctionAlias{
		FunctionName:    name,
		Name:            req.Name,
		FunctionVersion: req.FunctionVersion,
		Description:     req.Description,
		AliasARN:        aliasARN(fn.ARN, req.Name),
		RevisionId:      uuid.NewString(),
	}
	if aerr := h.ls.putAlias(ctx, a); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(aliasToResponse(a))
}

// GetAlias handles GET /2015-03-31/functions/{name}/aliases/{aliasName}.
// https://docs.aws.amazon.com/lambda/latest/api/API_GetAlias.html
func (h *Handler) GetAlias(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	aliasName := chi.URLParam(r, "aliasName")
	h.log.Debug("get alias", zap.String("function", name), zap.String("alias", aliasName))
	ctx := r.Context()

	a, aerr := h.ls.getAlias(ctx, name, aliasName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if a == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Function not found: %s:%s", name, aliasName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(aliasToResponse(a))
}

// UpdateAlias handles PUT /2015-03-31/functions/{name}/aliases/{aliasName}.
// https://docs.aws.amazon.com/lambda/latest/api/API_UpdateAlias.html
func (h *Handler) UpdateAlias(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	aliasName := chi.URLParam(r, "aliasName")
	h.log.Debug("update alias", zap.String("function", name), zap.String("alias", aliasName))
	ctx := r.Context()

	var req updateAliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterValueException",
			Message:    "Invalid request body: " + err.Error(),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	a, aerr := h.ls.getAlias(ctx, name, aliasName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if a == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Function not found: %s:%s", name, aliasName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if req.FunctionVersion != "" {
		a.FunctionVersion = req.FunctionVersion
	}
	if req.Description != "" {
		a.Description = req.Description
	}
	a.RevisionId = uuid.NewString()

	if aerr := h.ls.putAlias(ctx, a); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(aliasToResponse(a))
}

// ListAliases handles GET /2015-03-31/functions/{name}/aliases.
// https://docs.aws.amazon.com/lambda/latest/api/API_ListAliases.html
func (h *Handler) ListAliases(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("list aliases", zap.String("function", name))
	ctx := r.Context()

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	aliases, aerr := h.ls.listAliases(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	out := make([]aliasResponse, 0, len(aliases))
	for _, a := range aliases {
		out = append(out, aliasToResponse(a))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listAliasesResponse{Aliases: out})
}

// DeleteAlias handles DELETE /2015-03-31/functions/{name}/aliases/{aliasName}.
// https://docs.aws.amazon.com/lambda/latest/api/API_DeleteAlias.html
func (h *Handler) DeleteAlias(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	aliasName := chi.URLParam(r, "aliasName")
	h.log.Debug("delete alias", zap.String("function", name), zap.String("alias", aliasName))
	ctx := r.Context()

	a, aerr := h.ls.getAlias(ctx, name, aliasName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if a == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Function not found: %s:%s", name, aliasName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if aerr := h.ls.deleteAlias(ctx, name, aliasName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

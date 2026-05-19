package appsync

// handler_resolvers.go — function and resolver CRUD handlers.
//
// Functions:
//   - CreateFunction         POST   /v1/apis/{apiId}/functions
//   - GetFunction            GET    /v1/apis/{apiId}/functions/{functionId}
//   - ListFunctions          GET    /v1/apis/{apiId}/functions
//   - UpdateFunction         POST   /v1/apis/{apiId}/functions/{functionId}
//   - DeleteFunction         DELETE /v1/apis/{apiId}/functions/{functionId}
//   - ListResolversByFunction GET   /v1/apis/{apiId}/functions/{functionId}/resolvers
//
// Resolvers:
//   - CreateResolver  POST   /v1/apis/{apiId}/types/{typeName}/resolvers
//   - GetResolver     GET    /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}
//   - ListResolvers   GET    /v1/apis/{apiId}/types/{typeName}/resolvers
//   - UpdateResolver  POST   /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}
//   - DeleteResolver  DELETE /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// TODO(priority:P2): wire resolvers into QueryExecutor execution flow:
//   UNIT resolver: evaluate request template/code → call data source → evaluate response template/code.
//   PIPELINE resolver: execute pipelineConfig.functions[] in order, each function gets
//     its own request/response mapping. Use stash ($context.stash) to pass data between functions.
//   Runtime selection: if resolver has Runtime{Name:"APPSYNC_JS"}, use h.jsEvaluator.
//     Otherwise use h.vtlEvaluator for VTL mapping templates.
// TODO(priority:P3): when a mutation resolver completes, check if any active subscriptions
//   match the mutation's return type and push results via h.subscriptions.Publish().

// ─── Functions ───────────────────────────────────────────────────────────────

// CreateFunction handles POST /v1/apis/{apiId}/functions.
func (h *Handler) CreateFunction(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	var fn FunctionConfiguration
	if !serviceutil.DecodeJSON(w, r, &fn) {
		return
	}

	if fn.Name == "" {
		protocol.WriteJSONError(w, r, badRequestError("name is required"))
		return
	}

	fn.FunctionId = uuid.NewString()
	fn.ApiId = apiID
	fn.FunctionArn = protocol.ARN(h.region(r), h.cfg.AccountID, "appsync",
		fmt.Sprintf("apis/%s/functions/%s", apiID, fn.FunctionId))

	if fn.FunctionVersion == "" {
		fn.FunctionVersion = "2018-05-29"
	}

	if err := h.store.PutFunction(r.Context(), apiID, &fn); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusCreated, map[string]any{"functionConfiguration": &fn})
}

// GetFunction handles GET /v1/apis/{apiId}/functions/{functionId}.
func (h *Handler) GetFunction(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	fnID := chi.URLParam(r, "functionId")

	fn, err := h.store.GetFunction(r.Context(), apiID, fnID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, notFoundError(fmt.Sprintf("Function %s not found.", fnID)))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"functionConfiguration": fn})
}

// ListFunctions handles GET /v1/apis/{apiId}/functions.
func (h *Handler) ListFunctions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	fns, err := h.store.ListFunctions(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"functions": fns})
}

// UpdateFunction handles POST /v1/apis/{apiId}/functions/{functionId}.
func (h *Handler) UpdateFunction(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	fnID := chi.URLParam(r, "functionId")

	existing, err := h.store.GetFunction(r.Context(), apiID, fnID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError(fmt.Sprintf("Function %s not found.", fnID)))
		return
	}

	var fn FunctionConfiguration
	if !serviceutil.DecodeJSON(w, r, &fn) {
		return
	}

	// Preserve server-generated fields.
	fn.FunctionId = existing.FunctionId
	fn.FunctionArn = existing.FunctionArn
	fn.ApiId = apiID

	if fn.FunctionVersion == "" {
		fn.FunctionVersion = existing.FunctionVersion
	}

	if storeErr := h.store.PutFunction(r.Context(), apiID, &fn); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"functionConfiguration": &fn})
}

// DeleteFunction handles DELETE /v1/apis/{apiId}/functions/{functionId}.
func (h *Handler) DeleteFunction(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	fnID := chi.URLParam(r, "functionId")

	if err := h.store.DeleteFunction(r.Context(), apiID, fnID); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── Resolvers ───────────────────────────────────────────────────────────────

// CreateResolver handles POST /v1/apis/{apiId}/types/{typeName}/resolvers.
func (h *Handler) CreateResolver(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	typeName := chi.URLParam(r, "typeName")

	var res Resolver
	if !serviceutil.DecodeJSON(w, r, &res) {
		return
	}

	if res.FieldName == "" {
		protocol.WriteJSONError(w, r, badRequestError("fieldName is required"))
		return
	}

	// Check for duplicate resolver.
	existing, err := h.store.GetResolver(r.Context(), apiID, typeName, res.FieldName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, conflictError(
			fmt.Sprintf("Resolver %s.%s already exists.", typeName, res.FieldName)))
		return
	}

	res.TypeName = typeName
	res.ApiId = apiID
	res.ResolverArn = protocol.ARN(h.region(r), h.cfg.AccountID, "appsync",
		fmt.Sprintf("apis/%s/types/%s/resolvers/%s", apiID, typeName, res.FieldName))

	if res.Kind == "" {
		res.Kind = "UNIT"
	}

	if storeErr := h.store.PutResolver(r.Context(), apiID, &res); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	h.publish(r, events.AppSyncResolverCreated, events.ResourcePayload{Name: res.FieldName, ARN: res.ResolverArn})

	writeJSON(w, r, http.StatusCreated, map[string]any{"resolver": &res})
}

// GetResolver handles GET /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}.
func (h *Handler) GetResolver(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	typeName := chi.URLParam(r, "typeName")
	fieldName := chi.URLParam(r, "fieldName")

	res, err := h.store.GetResolver(r.Context(), apiID, typeName, fieldName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if res == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("Resolver %s.%s not found.", typeName, fieldName)))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"resolver": res})
}

// ListResolvers handles GET /v1/apis/{apiId}/types/{typeName}/resolvers.
func (h *Handler) ListResolvers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	typeName := chi.URLParam(r, "typeName")

	resolvers, err := h.store.ListResolvers(r.Context(), apiID, typeName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"resolvers": resolvers})
}

// UpdateResolver handles POST /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}.
func (h *Handler) UpdateResolver(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	typeName := chi.URLParam(r, "typeName")
	fieldName := chi.URLParam(r, "fieldName")

	existing, err := h.store.GetResolver(r.Context(), apiID, typeName, fieldName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError(
			fmt.Sprintf("Resolver %s.%s not found.", typeName, fieldName)))
		return
	}

	var res Resolver
	if !serviceutil.DecodeJSON(w, r, &res) {
		return
	}

	// Preserve server-generated fields.
	res.TypeName = typeName
	res.FieldName = fieldName
	res.ApiId = apiID
	res.ResolverArn = existing.ResolverArn

	if res.Kind == "" {
		res.Kind = existing.Kind
	}

	if storeErr := h.store.PutResolver(r.Context(), apiID, &res); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"resolver": &res})
}

// DeleteResolver handles DELETE /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}.
func (h *Handler) DeleteResolver(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	typeName := chi.URLParam(r, "typeName")
	fieldName := chi.URLParam(r, "fieldName")

	if err := h.store.DeleteResolver(r.Context(), apiID, typeName, fieldName); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	h.publish(r, events.AppSyncResolverDeleted, events.ResourcePayload{Name: fieldName})

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ListResolversByFunction handles GET /v1/apis/{apiId}/functions/{functionId}/resolvers.
// Returns all resolvers that reference the given function in their pipeline config.
func (h *Handler) ListResolversByFunction(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	functionID := chi.URLParam(r, "functionId")

	// Scan all resolvers for this API (no typeName filter).
	all, err := h.store.ListResolvers(r.Context(), apiID, "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	// Filter to those whose PipelineConfig.functions includes functionID.
	matching := make([]*Resolver, 0)
	for _, res := range all {
		if pipelineContainsFunction(res.PipelineConfig, functionID) {
			matching = append(matching, res)
		}
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"resolvers": matching})
}

// pipelineContainsFunction checks if a PipelineConfig's functions array contains
// the given function ID.
func pipelineContainsFunction(raw json.RawMessage, functionID string) bool {
	if len(raw) == 0 {
		return false
	}
	var pc struct {
		Functions []string `json:"functions"`
	}
	if err := json.Unmarshal(raw, &pc); err != nil {
		return false
	}
	for _, fn := range pc.Functions {
		if fn == functionID {
			return true
		}
	}
	return false
}

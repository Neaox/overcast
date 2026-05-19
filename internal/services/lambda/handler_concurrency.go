package lambda

// handler_concurrency.go — concurrency and provisioned concurrency handlers.
//
// Implemented:
//   - PutFunctionConcurrency          PUT    /2015-03-31/functions/{name}/concurrency
//   - GetFunctionConcurrency          GET    /2015-03-31/functions/{name}/concurrency
//   - DeleteFunctionConcurrency       DELETE /2015-03-31/functions/{name}/concurrency
//   - PutProvisionedConcurrencyConfig PUT    /2015-03-31/functions/{name}/provisioned-concurrency
//   - GetProvisionedConcurrencyConfig GET    /2015-03-31/functions/{name}/provisioned-concurrency

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
)

// ─── wire types ──────────────────────────────────────────────────────────────

type putFunctionConcurrencyRequest struct {
	ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
}

type functionConcurrencyResponse struct {
	ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
}

type putProvisionedConcurrencyRequest struct {
	ProvisionedConcurrentExecutions int `json:"ProvisionedConcurrentExecutions"`
}

type provisionedConcurrencyConfigResponse struct {
	AllocatedProvisionedConcurrentExecutions int    `json:"AllocatedProvisionedConcurrentExecutions"`
	RequestedProvisionedConcurrentExecutions int    `json:"RequestedProvisionedConcurrentExecutions"`
	AvailableProvisionedConcurrentExecutions int    `json:"AvailableProvisionedConcurrentExecutions"`
	Status                                   string `json:"Status"`
	LastModified                             string `json:"LastModified"`
}

// ─── Reserved concurrency ─────────────────────────────────────────────────────

// PutFunctionConcurrency handles PUT /2015-03-31/functions/{name}/concurrency.
func (h *Handler) PutFunctionConcurrency(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("put function concurrency", zap.String("function", name))
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

	var req putFunctionConcurrencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}

	fn.ReservedConcurrency = &req.ReservedConcurrentExecutions
	if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(functionConcurrencyResponse(req))
}

// GetFunctionConcurrency handles GET /2015-03-31/functions/{name}/concurrency.
func (h *Handler) GetFunctionConcurrency(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("get function concurrency", zap.String("function", name))

	fn, aerr := h.ls.getFunction(r.Context(), name)
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
	if fn.ReservedConcurrency == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "No concurrency configured for function: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(functionConcurrencyResponse{
		ReservedConcurrentExecutions: *fn.ReservedConcurrency,
	})
}

// DeleteFunctionConcurrency handles DELETE /2015-03-31/functions/{name}/concurrency.
func (h *Handler) DeleteFunctionConcurrency(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h.log.Debug("delete function concurrency", zap.String("function", name))
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

	fn.ReservedConcurrency = nil
	if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ─── Provisioned concurrency ─────────────────────────────────────────────────

// PutProvisionedConcurrencyConfig handles PUT /2015-03-31/functions/{name}/provisioned-concurrency.
// The Qualifier query parameter is required (version number or alias name).
func (h *Handler) PutProvisionedConcurrencyConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	qualifier := r.URL.Query().Get("Qualifier")
	h.log.Debug("put provisioned concurrency", zap.String("function", name), zap.String("qualifier", qualifier))
	ctx := r.Context()

	if qualifier == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Qualifier"))
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

	var req putProvisionedConcurrencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}

	cfg := &ProvisionedConcurrencyConfig{
		FunctionName:                             name,
		Qualifier:                                qualifier,
		RequestedProvisionedConcurrentExecutions: req.ProvisionedConcurrentExecutions,
		LastModified:                             h.clk.Now().UTC().Format(time.RFC3339),
	}
	if aerr := h.ls.putProvisionedConcurrencyConfig(ctx, cfg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(provisionedConcurrencyConfigResponse{
		AllocatedProvisionedConcurrentExecutions: req.ProvisionedConcurrentExecutions,
		RequestedProvisionedConcurrentExecutions: req.ProvisionedConcurrentExecutions,
		AvailableProvisionedConcurrentExecutions: req.ProvisionedConcurrentExecutions,
		Status:                                   "READY",
		LastModified:                             cfg.LastModified,
	})
}

// GetProvisionedConcurrencyConfig handles GET /2015-03-31/functions/{name}/provisioned-concurrency.
func (h *Handler) GetProvisionedConcurrencyConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	qualifier := r.URL.Query().Get("Qualifier")
	h.log.Debug("get provisioned concurrency", zap.String("function", name), zap.String("qualifier", qualifier))
	ctx := r.Context()

	if qualifier == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Qualifier"))
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

	cfg, aerr := h.ls.getProvisionedConcurrencyConfig(ctx, name, qualifier)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if cfg == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ProvisionedConcurrencyConfigNotFoundException",
			Message:    "No provisioned concurrency configured for function: " + name + ":" + qualifier,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(provisionedConcurrencyConfigResponse{
		AllocatedProvisionedConcurrentExecutions: cfg.RequestedProvisionedConcurrentExecutions,
		RequestedProvisionedConcurrentExecutions: cfg.RequestedProvisionedConcurrentExecutions,
		AvailableProvisionedConcurrentExecutions: cfg.RequestedProvisionedConcurrentExecutions,
		Status:                                   "READY",
		LastModified:                             cfg.LastModified,
	})
}

package lambda

// handler_concurrency.go — concurrency and provisioned concurrency handlers.
//
// Implemented:
//   - PutFunctionConcurrency          PUT    /2017-10-31/functions/{name}/concurrency
//   - GetFunctionConcurrency          GET    /2019-09-30/functions/{name}/concurrency
//   - DeleteFunctionConcurrency       DELETE /2017-10-31/functions/{name}/concurrency
//   - PutProvisionedConcurrencyConfig PUT    /2015-03-31/functions/{name}/provisioned-concurrency
//   - GetProvisionedConcurrencyConfig GET    /2015-03-31/functions/{name}/provisioned-concurrency

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
)

// ─── wire types ──────────────────────────────────────────────────────────────

type putFunctionConcurrencyRequest struct {
	ReservedConcurrentExecutions *int `json:"ReservedConcurrentExecutions"`
}

type functionConcurrencyResponse struct {
	ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
}

type putProvisionedConcurrencyRequest struct {
	ProvisionedConcurrentExecutions int `json:"ProvisionedConcurrentExecutions"`
}

// provisionedConcurrencyConfigResponse is the Put/Get response shape. It
// deliberately has no FunctionArn: AWS carries that only on the list item
// (ProvisionedConcurrencyConfigListItem), not on Get or Put.
type provisionedConcurrencyConfigResponse struct {
	AllocatedProvisionedConcurrentExecutions int    `json:"AllocatedProvisionedConcurrentExecutions"`
	AvailableProvisionedConcurrentExecutions int    `json:"AvailableProvisionedConcurrentExecutions"`
	LastModified                             string `json:"LastModified"`
	RequestedProvisionedConcurrentExecutions int    `json:"RequestedProvisionedConcurrentExecutions"`
	Status                                   string `json:"Status"`
	StatusReason                             string `json:"StatusReason,omitempty"`
}

// provisionedConcurrencyListItem is AWS's ProvisionedConcurrencyConfigListItem:
// the Get/Put shape plus the qualified function ARN, flat on the wire.
type provisionedConcurrencyListItem struct {
	AllocatedProvisionedConcurrentExecutions int    `json:"AllocatedProvisionedConcurrentExecutions"`
	AvailableProvisionedConcurrentExecutions int    `json:"AvailableProvisionedConcurrentExecutions"`
	FunctionArn                              string `json:"FunctionArn"`
	LastModified                             string `json:"LastModified"`
	RequestedProvisionedConcurrentExecutions int    `json:"RequestedProvisionedConcurrentExecutions"`
	Status                                   string `json:"Status"`
	StatusReason                             string `json:"StatusReason,omitempty"`
}

func newProvisionedListItem(cfg provisionedConcurrencyConfigResponse, functionArn string) provisionedConcurrencyListItem {
	return provisionedConcurrencyListItem{
		AllocatedProvisionedConcurrentExecutions: cfg.AllocatedProvisionedConcurrentExecutions,
		AvailableProvisionedConcurrentExecutions: cfg.AvailableProvisionedConcurrentExecutions,
		FunctionArn:                              functionArn,
		LastModified:                             cfg.LastModified,
		RequestedProvisionedConcurrentExecutions: cfg.RequestedProvisionedConcurrentExecutions,
		Status:                                   cfg.Status,
		StatusReason:                             cfg.StatusReason,
	}
}

type listProvisionedConcurrencyConfigsResponse struct {
	NextMarker                    *string                          `json:"NextMarker"`
	ProvisionedConcurrencyConfigs []provisionedConcurrencyListItem `json:"ProvisionedConcurrencyConfigs"`
}

// pool returns the InstancePool backing this handler, or nil when Docker is
// not available (the stub runtime cannot pre-warm anything). It waits for the
// initial Docker probe to report, so nil means "there is no container runtime"
// rather than "the probe has not finished yet" — see runtimeRegistry.poolFor.
func (h *Handler) pool(ctx context.Context) *InstancePool {
	return h.runtimes.poolFor(ctx)
}

// provisionedResponseFor builds the AWS response for one reservation, reading
// the live allocation from the pool so Allocated/Available reflect the
// environments that actually exist rather than echoing the request back.
func (h *Handler) provisionedResponseFor(ctx context.Context, fn *Function, cfg *ProvisionedConcurrencyConfig) provisionedConcurrencyConfigResponse {
	resp := provisionedConcurrencyConfigResponse{
		RequestedProvisionedConcurrentExecutions: cfg.RequestedProvisionedConcurrentExecutions,
		LastModified:                             cfg.LastModified,
		Status:                                   provisionedStatusInProgress,
	}
	pool := h.pool(ctx)
	if pool == nil {
		// No container runtime, so the allocation can never succeed. FAILED
		// with a reason is an AWS-valid status and the truth; READY would claim
		// capacity that does not exist and IN_PROGRESS would imply it is still
		// on its way.
		resp.Status = provisionedStatusFailed
		resp.StatusReason = "Docker is not available, so no execution environments can be allocated."
		return resp
	}
	_, allocated, available, ok := pool.ProvisionedStatus(fn.Name)
	if !ok {
		return resp
	}
	resp.AllocatedProvisionedConcurrentExecutions = allocated
	resp.AvailableProvisionedConcurrentExecutions = available
	if allocated >= cfg.RequestedProvisionedConcurrentExecutions {
		resp.Status = provisionedStatusReady
	} else {
		resp.StatusReason = "Provisioned environments are still starting."
	}
	return resp
}

// qualifiedFunctionARN is the ARN form AWS puts on a provisioned concurrency
// list item: the function ARN suffixed with the version or alias.
func qualifiedFunctionARN(fn *Function, qualifier string) string {
	return fn.ARN + ":" + qualifier
}

// ─── Reserved concurrency ─────────────────────────────────────────────────────

// PutFunctionConcurrency handles PUT /2017-10-31/functions/{name}/concurrency.
func (h *Handler) PutFunctionConcurrency(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	log.Debug("put function concurrency", zap.String("function", name))
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
	if req.ReservedConcurrentExecutions == nil {
		protocol.WriteJSONError(w, r, smithyRequiredConstraint("reservedConcurrentExecutions"))
		return
	}
	if *req.ReservedConcurrentExecutions < 0 {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("1 validation error detected: Value '"+strconv.Itoa(*req.ReservedConcurrentExecutions)+"' at 'reservedConcurrentExecutions' failed to satisfy constraint: Member must have value greater than or equal to 0"))
		return
	}

	fn, changed, aerr := h.ls.mutateFunction(ctx, name, func(current *Function) (bool, *protocol.AWSError) {
		current.ReservedConcurrency = req.ReservedConcurrentExecutions
		return true, nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !changed || fn == nil {
		protocol.WriteJSONError(w, r, lambdaFunctionNotFound(name))
		return
	}

	protocol.WriteRESTJSON(w, r, http.StatusOK, functionConcurrencyResponse{ReservedConcurrentExecutions: *req.ReservedConcurrentExecutions})
}

// GetFunctionConcurrency handles GET /2019-09-30/functions/{name}/concurrency.
func (h *Handler) GetFunctionConcurrency(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	log.Debug("get function concurrency", zap.String("function", name))

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

	protocol.WriteRESTJSON(w, r, http.StatusOK, functionConcurrencyResponse{
		ReservedConcurrentExecutions: *fn.ReservedConcurrency,
	})
}

// DeleteFunctionConcurrency handles DELETE /2017-10-31/functions/{name}/concurrency.
func (h *Handler) DeleteFunctionConcurrency(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	log.Debug("delete function concurrency", zap.String("function", name))
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

	fn, changed, aerr := h.ls.mutateFunction(ctx, name, func(current *Function) (bool, *protocol.AWSError) {
		current.ReservedConcurrency = nil
		return true, nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !changed || fn == nil {
		protocol.WriteJSONError(w, r, lambdaFunctionNotFound(name))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// AWS's ProvisionedConcurrencyStatusEnum.
const (
	provisionedStatusInProgress = "IN_PROGRESS"
	provisionedStatusReady      = "READY"
	provisionedStatusFailed     = "FAILED"
)

// ─── Provisioned concurrency ─────────────────────────────────────────────────

// PutProvisionedConcurrencyConfig handles PUT /2015-03-31/functions/{name}/provisioned-concurrency.
// The Qualifier query parameter is required (version number or alias name).
func (h *Handler) PutProvisionedConcurrencyConfig(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	qualifier := r.URL.Query().Get("Qualifier")
	log.Debug("put provisioned concurrency", zap.String("function", name), zap.String("qualifier", qualifier))
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

	if req.ProvisionedConcurrentExecutions < 1 {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(
			"ProvisionedConcurrentExecutions must be greater than 0.",
		))
		return
	}
	// "You can't allocate more provisioned concurrency than reserved
	// concurrency for a function." — Lambda developer guide, Understanding
	// Lambda function scaling.
	if reserved := reservedConcurrencyOf(fn); reserved >= 0 && req.ProvisionedConcurrentExecutions > reserved {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument(
			"'provisionedConcurrentExecutions' must be less than or equal to the function's reserved concurrency.",
		))
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

	// Actually reserve the environments. They are created in the background,
	// so the response reports IN_PROGRESS until they are up — exactly as AWS
	// does while it allocates.
	if pool := h.pool(ctx); pool != nil {
		pool.SetProvisionedConcurrency(fn, req.ProvisionedConcurrentExecutions)
	} else {
		log.Warn("provisioned concurrency configured but Docker is unavailable — no environments will be allocated",
			zap.String("function", name))
	}

	protocol.WriteRESTJSON(w, r, http.StatusAccepted, h.provisionedResponseFor(ctx, fn, cfg))
}

// DeleteProvisionedConcurrencyConfig handles DELETE /2015-03-31/functions/{name}/provisioned-concurrency.
func (h *Handler) DeleteProvisionedConcurrencyConfig(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	qualifier := r.URL.Query().Get("Qualifier")
	log.Debug("delete provisioned concurrency", zap.String("function", name), zap.String("qualifier", qualifier))
	ctx := r.Context()

	if qualifier == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("Qualifier"))
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
	if aerr := h.ls.deleteProvisionedConcurrencyConfig(ctx, name, qualifier); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// The environments are not torn down on the spot; they lose their
	// exemption from the idle sweep and age out like any warm instance.
	if pool := h.pool(ctx); pool != nil {
		pool.ClearProvisionedConcurrency(name)
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetOrListProvisionedConcurrency handles
// GET /2019-09-30/functions/{name}/provisioned-concurrency. AWS overloads one
// path and method for two operations, told apart by the List query parameter,
// so the dispatch happens here rather than in the router.
func (h *Handler) GetOrListProvisionedConcurrency(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.URL.Query().Get("List"), "ALL") {
		h.ListProvisionedConcurrencyConfigs(w, r)
		return
	}
	h.GetProvisionedConcurrencyConfig(w, r)
}

// ListProvisionedConcurrencyConfigs handles
// GET /2019-09-30/functions/{name}/provisioned-concurrency?List=ALL.
func (h *Handler) ListProvisionedConcurrencyConfigs(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	log.Debug("list provisioned concurrency", zap.String("function", name))
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

	cfgs, aerr := h.ls.listProvisionedConcurrencyConfigs(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	out := make([]provisionedConcurrencyListItem, 0, len(cfgs))
	for _, cfg := range cfgs {
		out = append(out, newProvisionedListItem(
			h.provisionedResponseFor(ctx, fn, cfg),
			qualifiedFunctionARN(fn, cfg.Qualifier),
		))
	}

	protocol.WriteRESTJSON(w, r, http.StatusOK, listProvisionedConcurrencyConfigsResponse{
		ProvisionedConcurrencyConfigs: out,
	})
}

// GetProvisionedConcurrencyConfig handles GET /2015-03-31/functions/{name}/provisioned-concurrency.
func (h *Handler) GetProvisionedConcurrencyConfig(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	name := chi.URLParam(r, "name")
	qualifier := r.URL.Query().Get("Qualifier")
	log.Debug("get provisioned concurrency", zap.String("function", name), zap.String("qualifier", qualifier))
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

	protocol.WriteRESTJSON(w, r, http.StatusOK, h.provisionedResponseFor(ctx, fn, cfg))
}

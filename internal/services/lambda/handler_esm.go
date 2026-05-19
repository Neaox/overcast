package lambda

// handler_esm.go — Lambda event source mapping (ESM) handlers.
//
// Implements:
//   - POST   /2015-03-31/event-source-mappings           CreateEventSourceMapping
//   - GET    /2015-03-31/event-source-mappings           ListEventSourceMappings
//   - GET    /2015-03-31/event-source-mappings/{uuid}    GetEventSourceMapping
//   - PUT    /2015-03-31/event-source-mappings/{uuid}    UpdateEventSourceMapping
//   - DELETE /2015-03-31/event-source-mappings/{uuid}    DeleteEventSourceMapping
//
// Supported event sources: SQS queues, DynamoDB Streams.
// Other sources return a descriptive 400 error.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// createESMRequest is the wire request body for CreateEventSourceMapping.
type createESMRequest struct {
	FunctionName                   string             `json:"FunctionName"`
	EventSourceArn                 string             `json:"EventSourceArn"`
	BatchSize                      int                `json:"BatchSize"`
	StartingPosition               string             `json:"StartingPosition"`
	MaximumBatchingWindowInSeconds int                `json:"MaximumBatchingWindowInSeconds"`
	FilterCriteria                 *FilterCriteria    `json:"FilterCriteria"`
	MaximumRecordAgeInSeconds      *int               `json:"MaximumRecordAgeInSeconds"`
	MaximumRetryAttempts           *int               `json:"MaximumRetryAttempts"`
	TumblingWindowInSeconds        int                `json:"TumblingWindowInSeconds"`
	BisectBatchOnFunctionError     bool               `json:"BisectBatchOnFunctionError"`
	DestinationConfig              *DestinationConfig `json:"DestinationConfig"`
	ScalingConfig                  *ScalingConfig     `json:"ScalingConfig"`
	Enabled                        *bool              `json:"Enabled"` // nil == true (default enabled)
}

// updateESMRequest is the wire request body for UpdateEventSourceMapping.
type updateESMRequest struct {
	FunctionName                   *string            `json:"FunctionName"`
	BatchSize                      *int               `json:"BatchSize"`
	MaximumBatchingWindowInSeconds *int               `json:"MaximumBatchingWindowInSeconds"`
	FilterCriteria                 *FilterCriteria    `json:"FilterCriteria"`
	MaximumRecordAgeInSeconds      *int               `json:"MaximumRecordAgeInSeconds"`
	MaximumRetryAttempts           *int               `json:"MaximumRetryAttempts"`
	TumblingWindowInSeconds        *int               `json:"TumblingWindowInSeconds"`
	BisectBatchOnFunctionError     *bool              `json:"BisectBatchOnFunctionError"`
	DestinationConfig              *DestinationConfig `json:"DestinationConfig"`
	ScalingConfig                  *ScalingConfig     `json:"ScalingConfig"`
	Enabled                        *bool              `json:"Enabled"`
}

// listESMResponse is the wire response for ListEventSourceMappings.
type listESMResponse struct {
	EventSourceMappings []*EventSourceMapping `json:"EventSourceMappings"`
	NextMarker          *string               `json:"NextMarker,omitempty"`
}

// CreateEventSourceMapping handles POST /2015-03-31/event-source-mappings.
func (h *Handler) CreateEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	var req createESMRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.FunctionName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ValidationException", Message: "FunctionName is required", HTTPStatus: http.StatusBadRequest})
		return
	}
	if req.EventSourceArn == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ValidationException", Message: "EventSourceArn is required", HTTPStatus: http.StatusBadRequest})
		return
	}

	// Validate event source type: only SQS and DynamoDB Streams are supported.
	sourceLower := strings.ToLower(req.EventSourceArn)
	isSQS := strings.Contains(sourceLower, ":sqs:")
	isDynamoDBStream := strings.Contains(sourceLower, ":dynamodb:") && strings.Contains(sourceLower, "/stream/")
	if !isSQS && !isDynamoDBStream {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ValidationException", Message: "Unsupported event source type. EventSourceArn must be an SQS queue ARN or a DynamoDB Streams ARN", HTTPStatus: http.StatusBadRequest})
		return
	}

	// Resolve function name → full ARN.
	funcName := functionNameFromARN(req.FunctionName) // no-op if already a plain name
	if funcName == "" {
		funcName = req.FunctionName
	}
	fn, aerr := h.ls.getFunction(r.Context(), funcName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("Function not found: %s", req.FunctionName), HTTPStatus: http.StatusNotFound})
		return
	}
	funcARN := protocol.LambdaARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, fn.Name)

	// Real AWS rejects cross-region ESMs: the event source (SQS queue or
	// DynamoDB stream) must be in the same region as the Lambda function.
	if sourceRegion := serviceutil.ARNRegion(req.EventSourceArn); sourceRegion != "" {
		if fnRegion := serviceutil.ARNRegion(funcARN); fnRegion != "" && fnRegion != sourceRegion {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterValueException",
				Message:    "The provided ARNs don't belong to the same region.",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
	}

	// Apply defaults.
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	initialState := esmStateEnabled
	if !enabled {
		initialState = esmStateDisabled
	}

	esm := &EventSourceMapping{
		UUID:                           uuid.New().String(),
		FunctionArn:                    funcARN,
		EventSourceArn:                 req.EventSourceArn,
		State:                          initialState,
		StateTransitionReason:          "USER_INITIATED",
		BatchSize:                      batchSize,
		StartingPosition:               req.StartingPosition,
		MaximumBatchingWindowInSeconds: req.MaximumBatchingWindowInSeconds,
		FilterCriteria:                 req.FilterCriteria,
		MaximumRecordAgeInSeconds:      req.MaximumRecordAgeInSeconds,
		MaximumRetryAttempts:           req.MaximumRetryAttempts,
		TumblingWindowInSeconds:        req.TumblingWindowInSeconds,
		BisectBatchOnFunctionError:     req.BisectBatchOnFunctionError,
		DestinationConfig:              req.DestinationConfig,
		ScalingConfig:                  req.ScalingConfig,
		LastModified:                   float64(h.clk.Now().UnixMilli()) / 1000,
		LastProcessingResult:           "No records processed",
	}

	if aerr := h.esm.putESM(r.Context(), esm); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Notify delivery manager (if wired) so event delivery begins immediately.
	if h.esmDelivery != nil {
		h.esmDelivery.Start(esm)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(esm)
}

// ListEventSourceMappings handles GET /2015-03-31/event-source-mappings.
// This replaces the stub in handler_stubs.go.
func (h *Handler) ListEventSourceMappings(w http.ResponseWriter, r *http.Request) {
	funcName := r.URL.Query().Get("FunctionName")
	if funcName != "" {
		funcName = functionNameFromARN(funcName)
		if funcName == "" {
			funcName = r.URL.Query().Get("FunctionName")
		}
	}
	eventSourceArn := r.URL.Query().Get("EventSourceArn")

	mappings, aerr := h.esm.listESMs(r.Context(), funcName, eventSourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if mappings == nil {
		mappings = []*EventSourceMapping{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listESMResponse{EventSourceMappings: mappings})
}

// GetEventSourceMapping handles GET /2015-03-31/event-source-mappings/{uuid}.
func (h *Handler) GetEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "uuid")
	esm, aerr := h.esm.getESM(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if esm == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("The event source arn (%s) and/or function provided is incorrect", id), HTTPStatus: http.StatusNotFound})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(esm)
}

// UpdateEventSourceMapping handles PUT /2015-03-31/event-source-mappings/{uuid}.
func (h *Handler) UpdateEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "uuid")
	esm, aerr := h.esm.getESM(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if esm == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("The event source arn (%s) and/or function provided is incorrect", id), HTTPStatus: http.StatusNotFound})
		return
	}

	var req updateESMRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.FunctionName != nil {
		funcName := functionNameFromARN(*req.FunctionName)
		if funcName == "" {
			funcName = *req.FunctionName
		}
		fn, aerr := h.ls.getFunction(r.Context(), funcName)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if fn == nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("Function not found: %s", *req.FunctionName), HTTPStatus: http.StatusNotFound})
			return
		}
		esm.FunctionArn = protocol.LambdaARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, fn.Name)
	}
	if req.BatchSize != nil {
		esm.BatchSize = *req.BatchSize
	}
	if req.MaximumBatchingWindowInSeconds != nil {
		esm.MaximumBatchingWindowInSeconds = *req.MaximumBatchingWindowInSeconds
	}
	if req.FilterCriteria != nil {
		esm.FilterCriteria = req.FilterCriteria
	}
	if req.MaximumRecordAgeInSeconds != nil {
		esm.MaximumRecordAgeInSeconds = req.MaximumRecordAgeInSeconds
	}
	if req.MaximumRetryAttempts != nil {
		esm.MaximumRetryAttempts = req.MaximumRetryAttempts
	}
	if req.TumblingWindowInSeconds != nil {
		esm.TumblingWindowInSeconds = *req.TumblingWindowInSeconds
	}
	if req.BisectBatchOnFunctionError != nil {
		esm.BisectBatchOnFunctionError = *req.BisectBatchOnFunctionError
	}
	if req.DestinationConfig != nil {
		esm.DestinationConfig = req.DestinationConfig
	}
	if req.ScalingConfig != nil {
		esm.ScalingConfig = req.ScalingConfig
	}
	if req.Enabled != nil {
		if *req.Enabled {
			esm.State = esmStateEnabled
		} else {
			esm.State = esmStateDisabled
		}
		esm.StateTransitionReason = "USER_INITIATED"
	}
	esm.LastModified = float64(h.clk.Now().UnixMilli()) / 1000

	if aerr := h.esm.putESM(r.Context(), esm); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Notify delivery manager of state change.
	if h.esmDelivery != nil {
		if esm.State == esmStateEnabled {
			h.esmDelivery.Start(esm)
		} else {
			h.esmDelivery.Stop(id)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(esm)
}

// DeleteEventSourceMapping handles DELETE /2015-03-31/event-source-mappings/{uuid}.
func (h *Handler) DeleteEventSourceMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "uuid")
	esm, aerr := h.esm.getESM(r.Context(), id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if esm == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: fmt.Sprintf("The event source arn (%s) and/or function provided is incorrect", id), HTTPStatus: http.StatusNotFound})
		return
	}

	// Mark as deleting, persist, then remove.
	esm.State = esmStateDeleting
	esm.StateTransitionReason = "USER_INITIATED"
	_ = h.esm.putESM(r.Context(), esm)

	if h.esmDelivery != nil {
		h.esmDelivery.Stop(id)
	}

	if aerr := h.esm.deleteESM(r.Context(), id); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(esm)
}

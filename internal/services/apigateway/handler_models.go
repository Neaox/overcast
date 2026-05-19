package apigateway

// handler_models.go — REST API v1 Models and RequestValidators handlers.
//
// Implemented:
//   CreateModel, GetModel, GetModels, DeleteModel,
//   CreateRequestValidator, GetRequestValidators, DeleteRequestValidator

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- CreateModel ----------------------------------------------------------

type createModelRequest struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      string `json:"schema,omitempty"`
}

// CreateModel handles POST /restapis/{restApiId}/models.
func (h *Handler) CreateModel(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createModelRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errBadRequest("Model name is required"))
		return
	}

	m := &Model{
		ID:          generateShortID(),
		Name:        req.Name,
		ContentType: req.ContentType,
		Description: req.Description,
		Schema:      req.Schema,
	}

	if aerr := h.store.putModel(r.Context(), apiID, m); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, m)
}

// ---- GetModel -------------------------------------------------------------

// GetModel handles GET /restapis/{restApiId}/models/{modelName}.
func (h *Handler) GetModel(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	modelName := chi.URLParam(r, "modelName")

	m, aerr := h.store.getModel(r.Context(), apiID, modelName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, m)
}

// ---- GetModels ------------------------------------------------------------

type getModelsResponse struct {
	Item []*Model `json:"item"`
}

// GetModels handles GET /restapis/{restApiId}/models.
func (h *Handler) GetModels(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	models, aerr := h.store.listModels(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, getModelsResponse{Item: models})
}

// ---- DeleteModel ----------------------------------------------------------

// DeleteModel handles DELETE /restapis/{restApiId}/models/{modelName}.
func (h *Handler) DeleteModel(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	modelName := chi.URLParam(r, "modelName")

	// verify model exists first
	if _, aerr := h.store.getModel(r.Context(), apiID, modelName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteModel(r.Context(), apiID, modelName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// ---- CreateRequestValidator -----------------------------------------------

type createRequestValidatorRequest struct {
	Name                      string `json:"name"`
	ValidateRequestBody       bool   `json:"validateRequestBody"`
	ValidateRequestParameters bool   `json:"validateRequestParameters"`
}

// CreateRequestValidator handles POST /restapis/{restApiId}/requestvalidators.
func (h *Handler) CreateRequestValidator(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createRequestValidatorRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errBadRequest("Request validator name is required"))
		return
	}

	rv := &RequestValidator{
		ID:                        generateShortID(),
		Name:                      req.Name,
		ValidateRequestBody:       req.ValidateRequestBody,
		ValidateRequestParameters: req.ValidateRequestParameters,
	}

	if aerr := h.store.putRequestValidator(r.Context(), apiID, rv); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, rv)
}

// ---- GetRequestValidators ------------------------------------------------

type getRequestValidatorsResponse struct {
	Item []*RequestValidator `json:"item"`
}

// GetRequestValidators handles GET /restapis/{restApiId}/requestvalidators.
func (h *Handler) GetRequestValidators(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	rvs, aerr := h.store.listRequestValidators(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, getRequestValidatorsResponse{Item: rvs})
}

// ---- DeleteRequestValidator -----------------------------------------------

// DeleteRequestValidator handles DELETE /restapis/{restApiId}/requestvalidators/{requestvalidatorId}.
func (h *Handler) DeleteRequestValidator(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	rvID := chi.URLParam(r, "requestvalidatorId")

	if aerr := h.store.deleteRequestValidator(r.Context(), apiID, rvID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

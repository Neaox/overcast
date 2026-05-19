package apigateway

// handler_stages.go — Stage and Deployment management for both REST API v1 and HTTP API v2.
//
// Implemented:
//   REST v1: CreateDeployment, GetDeployments, CreateStage, GetStage, GetStages, UpdateStage, DeleteStage
//   HTTP v2: CreateDeploymentV2, GetDeploymentsV2, CreateStageV2, GetStageV2, GetStagesV2, DeleteStageV2

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// AWS API Gateway returns timestamps on the wire as epoch seconds (a float
// like 1745925600.0), not milliseconds. The AWS SDKs deserialize the value
// as a Date, so emitting milliseconds yields year ~58000. We store
// timestamps internally as `UnixMilli`; these response wrappers convert.

// stageResponse mirrors Stage but emits createdDate/lastUpdatedDate as
// epoch seconds (float) for AWS SDK compatibility.
type stageResponse struct {
	StageName       string            `json:"stageName"`
	DeploymentID    string            `json:"deploymentId"`
	Description     string            `json:"description,omitempty"`
	CreatedDate     float64           `json:"createdDate"`
	LastUpdatedDate float64           `json:"lastUpdatedDate"`
	Tags            map[string]string `json:"tags,omitempty"`
	Variables       map[string]string `json:"variables,omitempty"`
}

func toStageResponse(s *Stage) stageResponse {
	return stageResponse{
		StageName:       s.StageName,
		DeploymentID:    s.DeploymentID,
		Description:     s.Description,
		CreatedDate:     float64(s.CreatedDate) / 1000.0,
		LastUpdatedDate: float64(s.LastUpdatedDate) / 1000.0,
		Tags:            s.Tags,
		Variables:       s.Variables,
	}
}

func toStageResponses(ss []*Stage) []stageResponse {
	out := make([]stageResponse, len(ss))
	for i, s := range ss {
		out[i] = toStageResponse(s)
	}
	return out
}

// deploymentResponse mirrors Deployment with createdDate as epoch seconds.
type deploymentResponse struct {
	ID          string  `json:"id"`
	Description string  `json:"description,omitempty"`
	CreatedDate float64 `json:"createdDate"`
}

func toDeploymentResponse(d *Deployment) deploymentResponse {
	return deploymentResponse{
		ID:          d.ID,
		Description: d.Description,
		CreatedDate: float64(d.CreatedDate) / 1000.0,
	}
}

func toDeploymentResponses(ds []*Deployment) []deploymentResponse {
	out := make([]deploymentResponse, len(ds))
	for i, d := range ds {
		out[i] = toDeploymentResponse(d)
	}
	return out
}

// ===========================================================================
// REST API v1 — Deployments
// ===========================================================================

type createDeploymentRequest struct {
	Description      string            `json:"description,omitempty"`
	StageName        string            `json:"stageName,omitempty"`
	StageDescription string            `json:"stageDescription,omitempty"`
	Variables        map[string]string `json:"variables,omitempty"`
}

func (h *Handler) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createDeploymentRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	dep := &Deployment{
		ID:          generateShortID(),
		Description: req.Description,
		CreatedDate: h.clk.Now().UnixMilli(),
	}

	if aerr := h.store.putDeployment(r.Context(), apiID, dep); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// If stageName is provided, create or update the stage automatically.
	if req.StageName != "" {
		now := h.clk.Now().UnixMilli()
		stage, _ := h.store.getStage(r.Context(), apiID, req.StageName)
		if stage != nil {
			stage.DeploymentID = dep.ID
			stage.LastUpdatedDate = now
		} else {
			stage = &Stage{
				StageName:       req.StageName,
				DeploymentID:    dep.ID,
				Description:     req.StageDescription,
				CreatedDate:     now,
				LastUpdatedDate: now,
				Variables:       req.Variables,
			}
		}
		if aerr := h.store.putStage(r.Context(), apiID, stage); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}

	h.log.Info("deployment created",
		zap.String("api_id", apiID),
		zap.String("deployment_id", dep.ID),
	)

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.APIGatewayDeployed,
			Time:    h.clk.Now(),
			Source:  serviceName,
			Payload: events.ResourcePayload{Name: apiID + "/" + dep.ID},
		})
	}

	protocol.WriteJSON(w, r, http.StatusCreated, dep)
}

// GetDeployments returns all deployments for a REST API.
func (h *Handler) GetDeployments(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	deps, aerr := h.store.listDeployments(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Item []deploymentResponse `json:"item"`
	}{Item: toDeploymentResponses(deps)})
}

// ===========================================================================
// REST API v1 — Stages
// ===========================================================================

type createStageRequest struct {
	StageName    string            `json:"stageName"`
	DeploymentID string            `json:"deploymentId"`
	Description  string            `json:"description,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	Variables    map[string]string `json:"variables,omitempty"`
}

func (h *Handler) CreateStage(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createStageRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.StageName == "" {
		protocol.WriteJSONError(w, r, errBadRequest("stageName is required"))
		return
	}
	if req.DeploymentID == "" {
		protocol.WriteJSONError(w, r, errBadRequest("deploymentId is required"))
		return
	}

	// Check for conflict.
	if existing, _ := h.store.getStage(r.Context(), apiID, req.StageName); existing != nil {
		protocol.WriteJSONError(w, r, errConflict("Stage already exists"))
		return
	}

	now := h.clk.Now().UnixMilli()
	stage := &Stage{
		StageName:       req.StageName,
		DeploymentID:    req.DeploymentID,
		Description:     req.Description,
		CreatedDate:     now,
		LastUpdatedDate: now,
		Tags:            req.Tags,
		Variables:       req.Variables,
	}

	if aerr := h.store.putStage(r.Context(), apiID, stage); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, toStageResponse(stage))
}

func (h *Handler) GetStage(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	stageName := chi.URLParam(r, "stageName")

	stage, aerr := h.store.getStage(r.Context(), apiID, stageName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, toStageResponse(stage))
}

func (h *Handler) GetStages(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	stages, aerr := h.store.listStages(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Item []stageResponse `json:"item"`
	}{Item: toStageResponses(stages)})
}

type updateStageRequest struct {
	PatchOperations []patchOperation `json:"patchOperations,omitempty"`
}

func (h *Handler) UpdateStage(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	stageName := chi.URLParam(r, "stageName")

	stage, aerr := h.store.getStage(r.Context(), apiID, stageName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req updateStageRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	for _, op := range req.PatchOperations {
		if op.Op != "replace" {
			continue
		}
		switch op.Path {
		case "/description":
			stage.Description = op.Value
		case "/deploymentId":
			stage.DeploymentID = op.Value
		}
	}

	stage.LastUpdatedDate = h.clk.Now().UnixMilli()

	if aerr := h.store.putStage(r.Context(), apiID, stage); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, toStageResponse(stage))
}

func (h *Handler) DeleteStage(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	stageName := chi.URLParam(r, "stageName")

	if _, aerr := h.store.getStage(r.Context(), apiID, stageName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteStage(r.Context(), apiID, stageName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// ===========================================================================
// HTTP API v2 — Deployments
// ===========================================================================

type createV2DeploymentRequest struct {
	Description string `json:"description,omitempty"`
}

func (h *Handler) CreateV2Deployment(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createV2DeploymentRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	dep := &DeploymentV2{
		DeploymentID:     generateShortID(),
		Description:      req.Description,
		CreatedDate:      h.clk.Now().Format("2006-01-02T15:04:05Z"),
		DeploymentStatus: "DEPLOYED",
	}

	if aerr := h.store.putV2Deployment(r.Context(), apiID, dep); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, dep)
}

func (h *Handler) GetV2Deployments(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	deps, aerr := h.store.listV2Deployments(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Items []*DeploymentV2 `json:"items"`
	}{Items: deps})
}

// ===========================================================================
// HTTP API v2 — Stages
// ===========================================================================

type createV2StageRequest struct {
	StageName      string            `json:"stageName"`
	DeploymentID   string            `json:"deploymentId,omitempty"`
	Description    string            `json:"description,omitempty"`
	AutoDeploy     bool              `json:"autoDeploy,omitempty"`
	StageVariables map[string]string `json:"stageVariables,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
}

func (h *Handler) CreateV2Stage(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createV2StageRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.StageName == "" {
		protocol.WriteJSONError(w, r, errBadRequest("stageName is required"))
		return
	}

	// Check for conflict.
	if existing, _ := h.store.getV2Stage(r.Context(), apiID, req.StageName); existing != nil {
		protocol.WriteJSONError(w, r, errConflict("Stage already exists"))
		return
	}

	now := h.clk.Now().Format("2006-01-02T15:04:05Z")
	stage := &StageV2{
		StageName:       req.StageName,
		DeploymentID:    req.DeploymentID,
		Description:     req.Description,
		AutoDeploy:      req.AutoDeploy,
		CreatedDate:     now,
		LastUpdatedDate: now,
		StageVariables:  req.StageVariables,
		Tags:            req.Tags,
	}

	if aerr := h.store.putV2Stage(r.Context(), apiID, stage); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, stage)
}

func (h *Handler) GetV2Stage(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	stageName := chi.URLParam(r, "stageName")

	stage, aerr := h.store.getV2Stage(r.Context(), apiID, stageName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, stage)
}

func (h *Handler) GetV2Stages(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	stages, aerr := h.store.listV2Stages(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Items []*StageV2 `json:"items"`
	}{Items: stages})
}

func (h *Handler) DeleteV2Stage(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	stageName := chi.URLParam(r, "stageName")

	if _, aerr := h.store.getV2Stage(r.Context(), apiID, stageName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteV2Stage(r.Context(), apiID, stageName); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

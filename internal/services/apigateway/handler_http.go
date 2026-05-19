package apigateway

// handler_http.go — HTTP API v2 management handlers.
//
// Implemented:
//   CreateApi, GetApi, GetApis, UpdateApi, DeleteApi,
//   CreateRoute, GetRoute, GetRoutes, DeleteRoute,
//   CreateIntegration, GetIntegration, GetIntegrations, DeleteIntegration

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- CreateApi (v2) -------------------------------------------------------

type createV2APIRequest struct {
	Name                     string            `json:"name"`
	ProtocolType             string            `json:"protocolType"` // HTTP, WEBSOCKET
	Description              string            `json:"description,omitempty"`
	RouteSelectionExpression string            `json:"routeSelectionExpression,omitempty"`
	CorsConfiguration        *CorsConfig       `json:"corsConfiguration,omitempty"`
	Tags                     map[string]string `json:"tags,omitempty"`
	Version                  string            `json:"version,omitempty"`
	DisableExecuteAPI        bool              `json:"disableExecuteApiEndpoint,omitempty"`
}

func (h *Handler) CreateV2Api(w http.ResponseWriter, r *http.Request) {
	var req createV2APIRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errBadRequest("Name is required"))
		return
	}
	if req.ProtocolType == "" {
		protocol.WriteJSONError(w, r, errBadRequest("ProtocolType is required"))
		return
	}

	// TODO(priority:P3): implement WEBSOCKET protocol type — currently only HTTP is fully supported
	if req.ProtocolType == "WEBSOCKET" {
		h.log.Debug("CreateApi: WEBSOCKET protocol type stored but execution not implemented")
	}

	apiID := generateAPIID()
	now := h.clk.Now().Format("2006-01-02T15:04:05Z")

	routeSelExpr := req.RouteSelectionExpression
	if routeSelExpr == "" {
		routeSelExpr = "${request.method} ${request.path}"
	}

	api := &APIV2{
		ApiID:                    apiID,
		Name:                     req.Name,
		ProtocolType:             req.ProtocolType,
		Description:              req.Description,
		RouteSelectionExpression: routeSelExpr,
		CorsConfiguration:        req.CorsConfiguration,
		CreatedDate:              now,
		Tags:                     req.Tags,
		Version:                  req.Version,
		DisableExecuteAPI:        req.DisableExecuteAPI,
	}

	if aerr := h.store.putV2API(r.Context(), api); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("HTTP API created",
		zap.String("api_id", apiID),
		zap.String("name", req.Name),
		zap.String("protocol", req.ProtocolType),
	)

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.APIGatewayHTTPAPICreated,
			Time:    h.clk.Now(),
			Source:  serviceName,
			Payload: events.ResourcePayload{Name: req.Name},
		})
	}

	protocol.WriteJSON(w, r, http.StatusCreated, v2APIToResponse(api, middleware.RegionFromContext(r.Context(), h.cfg.Region)))
}

// ---- GetApi (v2) ----------------------------------------------------------

func (h *Handler) GetV2Api(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)

	api, aerr := h.store.getV2API(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, v2APIToResponse(api, region))
}

// ---- GetApis (v2) ---------------------------------------------------------

type getV2APIsResponse struct {
	Items     []v2APIResponse `json:"items"`
	NextToken string          `json:"nextToken,omitempty"`
}

func (h *Handler) GetV2Apis(w http.ResponseWriter, r *http.Request) {
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)

	apis, aerr := h.store.listV2APIs(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	items := make([]v2APIResponse, 0, len(apis))
	for _, api := range apis {
		items = append(items, v2APIToResponse(api, region))
	}

	// TODO(priority:P2): implement pagination
	protocol.WriteJSON(w, r, http.StatusOK, getV2APIsResponse{Items: items})
}

// ---- UpdateApi (v2) -------------------------------------------------------

type updateV2APIRequest struct {
	Name                     string      `json:"name,omitempty"`
	Description              string      `json:"description,omitempty"`
	CorsConfiguration        *CorsConfig `json:"corsConfiguration,omitempty"`
	RouteSelectionExpression string      `json:"routeSelectionExpression,omitempty"`
	Version                  string      `json:"version,omitempty"`
}

func (h *Handler) UpdateV2Api(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)

	api, aerr := h.store.getV2API(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req updateV2APIRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name != "" {
		api.Name = req.Name
	}
	if req.Description != "" {
		api.Description = req.Description
	}
	if req.CorsConfiguration != nil {
		api.CorsConfiguration = req.CorsConfiguration
	}
	if req.RouteSelectionExpression != "" {
		api.RouteSelectionExpression = req.RouteSelectionExpression
	}
	if req.Version != "" {
		api.Version = req.Version
	}

	if aerr := h.store.putV2API(r.Context(), api); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, v2APIToResponse(api, region))
}

// ---- DeleteApi (v2) -------------------------------------------------------

func (h *Handler) DeleteV2Api(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	api, aerr := h.store.getV2API(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Cascade delete: routes, integrations, stages, deployments.
	_ = h.store.deleteAllV2Routes(r.Context(), apiID)
	_ = h.store.deleteAllV2Integrations(r.Context(), apiID)
	_ = h.store.deleteAllV2Stages(r.Context(), apiID)
	_ = h.store.deleteAllV2Deployments(r.Context(), apiID)

	if aerr := h.store.deleteV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("HTTP API deleted",
		zap.String("api_id", apiID),
		zap.String("name", api.Name),
	)

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.APIGatewayHTTPAPIDeleted,
			Time:    h.clk.Now(),
			Source:  serviceName,
			Payload: events.ResourcePayload{Name: api.Name},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- CreateRoute (v2) -----------------------------------------------------

type createV2RouteRequest struct {
	RouteKey          string `json:"routeKey"` // "GET /users", "$default"
	Target            string `json:"target,omitempty"`
	AuthorizationType string `json:"authorizationType,omitempty"`
	AuthorizerID      string `json:"authorizerId,omitempty"`
	APIKeyRequired    bool   `json:"apiKeyRequired,omitempty"`
}

func (h *Handler) CreateV2Route(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createV2RouteRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.RouteKey == "" {
		protocol.WriteJSONError(w, r, errBadRequest("RouteKey is required"))
		return
	}

	route := &RouteV2{
		RouteID:           generateShortID(),
		RouteKey:          req.RouteKey,
		Target:            req.Target,
		AuthorizationType: req.AuthorizationType,
		AuthorizerID:      req.AuthorizerID,
		APIKeyRequired:    req.APIKeyRequired,
	}
	if route.AuthorizationType == "" {
		route.AuthorizationType = "NONE"
	}

	if aerr := h.store.putV2Route(r.Context(), apiID, route); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, route)
}

// ---- GetRoute (v2) --------------------------------------------------------

func (h *Handler) GetV2Route(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	routeID := chi.URLParam(r, "routeId")

	route, aerr := h.store.getV2Route(r.Context(), apiID, routeID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, route)
}

// ---- GetRoutes (v2) -------------------------------------------------------

type getV2RoutesResponse struct {
	Items     []*RouteV2 `json:"items"`
	NextToken string     `json:"nextToken,omitempty"`
}

func (h *Handler) GetV2Routes(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	routes, aerr := h.store.listV2Routes(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, getV2RoutesResponse{Items: routes})
}

// ---- DeleteRoute (v2) -----------------------------------------------------

func (h *Handler) DeleteV2Route(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	routeID := chi.URLParam(r, "routeId")

	if _, aerr := h.store.getV2Route(r.Context(), apiID, routeID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteV2Route(r.Context(), apiID, routeID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- CreateIntegration (v2) -----------------------------------------------

type createV2IntegrationRequest struct {
	IntegrationType      string `json:"integrationType"` // AWS_PROXY, HTTP_PROXY
	IntegrationURI       string `json:"integrationUri,omitempty"`
	IntegrationMethod    string `json:"integrationMethod,omitempty"`
	PayloadFormatVersion string `json:"payloadFormatVersion,omitempty"`
	ConnectionType       string `json:"connectionType,omitempty"`
	TimeoutInMillis      int    `json:"timeoutInMillis,omitempty"`
	Description          string `json:"description,omitempty"`
}

func (h *Handler) CreateV2Integration(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createV2IntegrationRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.IntegrationType == "" {
		protocol.WriteJSONError(w, r, errBadRequest("IntegrationType is required"))
		return
	}

	payloadFmt := req.PayloadFormatVersion
	if payloadFmt == "" {
		payloadFmt = "1.0" // AWS default
	}
	timeout := req.TimeoutInMillis
	if timeout == 0 {
		timeout = 30000 // HTTP API default: 30 seconds
	}

	integ := &IntegrationV2{
		IntegrationID:        generateShortID(),
		IntegrationType:      req.IntegrationType,
		IntegrationURI:       req.IntegrationURI,
		IntegrationMethod:    req.IntegrationMethod,
		PayloadFormatVersion: payloadFmt,
		ConnectionType:       req.ConnectionType,
		TimeoutInMillis:      timeout,
		Description:          req.Description,
	}

	if aerr := h.store.putV2Integration(r.Context(), apiID, integ); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, integ)
}

// ---- GetIntegration (v2) --------------------------------------------------

func (h *Handler) GetV2Integration(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	integID := chi.URLParam(r, "integrationId")

	integ, aerr := h.store.getV2Integration(r.Context(), apiID, integID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, integ)
}

// ---- GetIntegrations (v2) -------------------------------------------------

type getV2IntegrationsResponse struct {
	Items     []*IntegrationV2 `json:"items"`
	NextToken string           `json:"nextToken,omitempty"`
}

func (h *Handler) GetV2Integrations(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")

	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	integs, aerr := h.store.listV2Integrations(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, getV2IntegrationsResponse{Items: integs})
}

// ---- DeleteIntegration (v2) -----------------------------------------------

func (h *Handler) DeleteV2Integration(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	integID := chi.URLParam(r, "integrationId")

	if _, aerr := h.store.getV2Integration(r.Context(), apiID, integID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteV2Integration(r.Context(), apiID, integID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- UpdateRoute (v2) -----------------------------------------------------

// UpdateV2Route handles PATCH /v2/apis/{apiId}/routes/{routeId}.
// AWS docs: https://docs.aws.amazon.com/apigatewayv2/latest/api-reference/apis-apiid-routes-routeid.html
func (h *Handler) UpdateV2Route(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	routeID := chi.URLParam(r, "routeId")

	route, aerr := h.store.getV2Route(r.Context(), apiID, routeID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createV2RouteRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.RouteKey != "" {
		route.RouteKey = req.RouteKey
	}
	if req.Target != "" {
		route.Target = req.Target
	}
	if req.AuthorizationType != "" {
		route.AuthorizationType = req.AuthorizationType
	}
	if req.AuthorizerID != "" {
		route.AuthorizerID = req.AuthorizerID
	}

	if aerr := h.store.putV2Route(r.Context(), apiID, route); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, route)
}

// ---- UpdateIntegration (v2) -----------------------------------------------

// UpdateV2Integration handles PATCH /v2/apis/{apiId}/integrations/{integrationId}.
// AWS docs: https://docs.aws.amazon.com/apigatewayv2/latest/api-reference/apis-apiid-integrations-integrationid.html
func (h *Handler) UpdateV2Integration(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	integID := chi.URLParam(r, "integrationId")

	integ, aerr := h.store.getV2Integration(r.Context(), apiID, integID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createV2IntegrationRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.IntegrationType != "" {
		integ.IntegrationType = req.IntegrationType
	}
	if req.IntegrationURI != "" {
		integ.IntegrationURI = req.IntegrationURI
	}
	if req.IntegrationMethod != "" {
		integ.IntegrationMethod = req.IntegrationMethod
	}
	if req.PayloadFormatVersion != "" {
		integ.PayloadFormatVersion = req.PayloadFormatVersion
	}
	if req.ConnectionType != "" {
		integ.ConnectionType = req.ConnectionType
	}
	if req.TimeoutInMillis != 0 {
		integ.TimeoutInMillis = req.TimeoutInMillis
	}
	if req.Description != "" {
		integ.Description = req.Description
	}

	if aerr := h.store.putV2Integration(r.Context(), apiID, integ); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, integ)
}

// ---- UpdateStage (v2) -----------------------------------------------------

// UpdateV2Stage handles PATCH /v2/apis/{apiId}/stages/{stageName}.
// AWS docs: https://docs.aws.amazon.com/apigatewayv2/latest/api-reference/apis-apiid-stages-stagename.html
func (h *Handler) UpdateV2Stage(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	stageName := chi.URLParam(r, "stageName")

	stage, aerr := h.store.getV2Stage(r.Context(), apiID, stageName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req struct {
		Description    string            `json:"description,omitempty"`
		AutoDeploy     *bool             `json:"autoDeploy,omitempty"`
		DeploymentID   string            `json:"deploymentId,omitempty"`
		StageVariables map[string]string `json:"stageVariables,omitempty"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.Description != "" {
		stage.Description = req.Description
	}
	if req.AutoDeploy != nil {
		stage.AutoDeploy = *req.AutoDeploy
	}
	if req.DeploymentID != "" {
		stage.DeploymentID = req.DeploymentID
	}
	if req.StageVariables != nil {
		if stage.StageVariables == nil {
			stage.StageVariables = make(map[string]string)
		}
		for k, v := range req.StageVariables {
			stage.StageVariables[k] = v
		}
	}

	stage.LastUpdatedDate = h.clk.Now().UTC().Format("2006-01-02T15:04:05Z")

	if aerr := h.store.putV2Stage(r.Context(), apiID, stage); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, stage)
}

// ---- Response helpers -----------------------------------------------------

type v2APIResponse struct {
	ApiID                    string            `json:"apiId"`
	Name                     string            `json:"name"`
	ProtocolType             string            `json:"protocolType"`
	Description              string            `json:"description,omitempty"`
	RouteSelectionExpression string            `json:"routeSelectionExpression,omitempty"`
	CorsConfiguration        *CorsConfig       `json:"corsConfiguration,omitempty"`
	CreatedDate              string            `json:"createdDate"`
	Tags                     map[string]string `json:"tags,omitempty"`
	Version                  string            `json:"version,omitempty"`
	DisableExecuteAPI        bool              `json:"disableExecuteApiEndpoint,omitempty"`
	ApiEndpoint              string            `json:"apiEndpoint,omitempty"`
	ARN                      string            `json:"arn,omitempty"`
}

func v2APIToResponse(api *APIV2, region string) v2APIResponse {
	return v2APIResponse{
		ApiID:                    api.ApiID,
		Name:                     api.Name,
		ProtocolType:             api.ProtocolType,
		Description:              api.Description,
		RouteSelectionExpression: api.RouteSelectionExpression,
		CorsConfiguration:        api.CorsConfiguration,
		CreatedDate:              api.CreatedDate,
		Tags:                     api.Tags,
		Version:                  api.Version,
		DisableExecuteAPI:        api.DisableExecuteAPI,
		ARN:                      protocol.APIV2ARN(region, api.ApiID),
	}
}

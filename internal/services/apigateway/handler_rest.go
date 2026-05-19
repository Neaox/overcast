package apigateway

// handler_rest.go — REST API v1 management handlers.
//
// Implemented:
//   CreateRestApi, GetRestApi, GetRestApis, DeleteRestApi,
//   CreateResource, GetResource, GetResources, DeleteResource,
//   PutMethod, GetMethod, DeleteMethod,
//   PutIntegration, GetIntegration, DeleteIntegration,
//   PutMethodResponse, PutIntegrationResponse

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- CreateRestApi --------------------------------------------------------

type createRestAPIRequest struct {
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	Version           string            `json:"version,omitempty"`
	EndpointConfig    *EndpointConfig   `json:"endpointConfiguration,omitempty"`
	Policy            string            `json:"policy,omitempty"`
	Tags              map[string]string `json:"tags,omitempty"`
	BinaryMediaTypes  []string          `json:"binaryMediaTypes,omitempty"`
	DisableExecuteAPI bool              `json:"disableExecuteApiEndpoint,omitempty"`
}

func (h *Handler) CreateRestApi(w http.ResponseWriter, r *http.Request) {
	var req createRestAPIRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errBadRequest("API name is required"))
		return
	}

	apiID := generateAPIID()
	rootResourceID := generateShortID()
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	now := h.clk.Now().UnixMilli()

	endpointCfg := req.EndpointConfig
	if endpointCfg == nil {
		endpointCfg = &EndpointConfig{Types: []string{"EDGE"}}
	}

	api := &RestAPI{
		ID:                apiID,
		Name:              req.Name,
		Description:       req.Description,
		Version:           req.Version,
		CreatedDate:       now,
		EndpointConfig:    endpointCfg,
		Policy:            req.Policy,
		Tags:              req.Tags,
		BinaryMediaTypes:  req.BinaryMediaTypes,
		DisableExecuteAPI: req.DisableExecuteAPI,
		RootResourceID:    rootResourceID,
	}

	if aerr := h.store.putRestAPI(r.Context(), api); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Create the root "/" resource automatically (matches real AWS).
	root := &Resource{
		ID:       rootResourceID,
		PathPart: "",
		Path:     "/",
	}
	if aerr := h.store.putResource(r.Context(), apiID, root); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("REST API created",
		zap.String("api_id", apiID),
		zap.String("name", req.Name),
	)

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.APIGatewayRestAPICreated,
			Time:    h.clk.Now(),
			Source:  serviceName,
			Payload: events.ResourcePayload{Name: req.Name},
		})
	}

	protocol.WriteJSON(w, r, http.StatusCreated, restAPIToResponse(api, region))
}

// ---- GetRestApi -----------------------------------------------------------

func (h *Handler) GetRestApi(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)

	api, aerr := h.store.getRestAPI(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, restAPIToResponse(api, region))
}

// ---- GetRestApis ----------------------------------------------------------

type getRestAPIsResponse struct {
	Item     []restAPIResponse `json:"item"`
	Position string            `json:"position,omitempty"`
}

func (h *Handler) GetRestApis(w http.ResponseWriter, r *http.Request) {
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	apis, aerr := h.store.listRestAPIs(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	items := make([]restAPIResponse, 0, len(apis))
	for _, api := range apis {
		items = append(items, restAPIToResponse(api, region))
	}

	// TODO(priority:P2): implement pagination when list grows large
	protocol.WriteJSON(w, r, http.StatusOK, getRestAPIsResponse{Item: items})
}

// ---- DeleteRestApi --------------------------------------------------------

func (h *Handler) DeleteRestApi(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	api, aerr := h.store.getRestAPI(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Cascade delete: resources, stages, deployments.
	_ = h.store.deleteAllResources(r.Context(), apiID)
	_ = h.store.deleteAllStages(r.Context(), apiID)
	_ = h.store.deleteAllDeployments(r.Context(), apiID)

	if aerr := h.store.deleteRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("REST API deleted",
		zap.String("api_id", apiID),
		zap.String("name", api.Name),
	)

	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{
			Type:    events.APIGatewayRestAPIDeleted,
			Time:    h.clk.Now(),
			Source:  serviceName,
			Payload: events.ResourcePayload{Name: api.Name},
		})
	}

	w.WriteHeader(http.StatusAccepted)
}

// ---- UpdateRestApi --------------------------------------------------------

type updateRestAPIRequest struct {
	PatchOperations []patchOperation `json:"patchOperations,omitempty"`
}

type patchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
	From  string `json:"from,omitempty"`
}

func (h *Handler) UpdateRestApi(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)

	api, aerr := h.store.getRestAPI(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req updateRestAPIRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	for _, op := range req.PatchOperations {
		if op.Op != "replace" {
			continue
		}
		switch op.Path {
		case "/name":
			api.Name = op.Value
		case "/description":
			api.Description = op.Value
		}
	}

	if aerr := h.store.putRestAPI(r.Context(), api); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, restAPIToResponse(api, region))
}

// ---- CreateResource -------------------------------------------------------

type createResourceRequest struct {
	PathPart string `json:"pathPart"`
}

func (h *Handler) CreateResource(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	parentID := chi.URLParam(r, "parentId")

	// Verify the API exists.
	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Verify the parent resource exists.
	parent, aerr := h.store.getResource(r.Context(), apiID, parentID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req createResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.PathPart == "" {
		protocol.WriteJSONError(w, r, errBadRequest("pathPart is required"))
		return
	}

	resourceID := generateShortID()
	path := parent.Path
	if path == "/" {
		path = "/" + req.PathPart
	} else {
		path = parent.Path + "/" + req.PathPart
	}

	res := &Resource{
		ID:       resourceID,
		ParentID: parentID,
		PathPart: req.PathPart,
		Path:     path,
	}

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, res)
}

// ---- GetResource ----------------------------------------------------------

func (h *Handler) GetResource(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, res)
}

// ---- GetResources ---------------------------------------------------------

type getResourcesResponse struct {
	Item     []*Resource `json:"item"`
	Position string      `json:"position,omitempty"`
}

func (h *Handler) GetResources(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")

	// Verify the API exists.
	if _, aerr := h.store.getRestAPI(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	resources, aerr := h.store.listResources(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// TODO(priority:P2): implement pagination
	protocol.WriteJSON(w, r, http.StatusOK, getResourcesResponse{Item: resources})
}

// ---- DeleteResource -------------------------------------------------------

func (h *Handler) DeleteResource(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")

	// Verify the resource exists.
	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Cannot delete the root resource.
	if res.Path == "/" {
		protocol.WriteJSONError(w, r, errBadRequest("Cannot delete the root resource"))
		return
	}

	if aerr := h.store.deleteResource(r.Context(), apiID, resourceID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// ---- PutMethod ------------------------------------------------------------

type putMethodRequest struct {
	AuthorizationType string          `json:"authorizationType"`
	AuthorizerID      string          `json:"authorizerId,omitempty"`
	APIKeyRequired    bool            `json:"apiKeyRequired,omitempty"`
	RequestParameters map[string]bool `json:"requestParameters,omitempty"`
	// TODO(priority:P3): add requestModels, requestValidatorId, operationName
}

func (h *Handler) PutMethod(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req putMethodRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	authType := req.AuthorizationType
	if authType == "" {
		authType = "NONE"
	}

	method := &Method{
		HTTPMethod:        httpMethod,
		AuthorizationType: authType,
		AuthorizerID:      req.AuthorizerID,
		APIKeyRequired:    req.APIKeyRequired,
		RequestParameters: req.RequestParameters,
	}

	if res.ResourceMethods == nil {
		res.ResourceMethods = make(map[string]*Method)
	}
	res.ResourceMethods[httpMethod] = method

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, method)
}

// ---- GetMethod ------------------------------------------------------------

func (h *Handler) GetMethod(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Method identifier specified",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, method)
}

// ---- DeleteMethod ---------------------------------------------------------

func (h *Handler) DeleteMethod(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if _, ok := res.ResourceMethods[httpMethod]; !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Method identifier specified",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	delete(res.ResourceMethods, httpMethod)
	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- PutIntegration -------------------------------------------------------

type putIntegrationRequest struct {
	Type                string            `json:"type"` // AWS_PROXY, HTTP_PROXY, MOCK, AWS, HTTP
	HTTPMethod          string            `json:"httpMethod,omitempty"`
	URI                 string            `json:"uri,omitempty"`
	ConnectionType      string            `json:"connectionType,omitempty"`
	ContentHandling     string            `json:"contentHandling,omitempty"`
	Credentials         string            `json:"credentials,omitempty"`
	PassthroughBehavior string            `json:"passthroughBehavior,omitempty"`
	RequestParameters   map[string]string `json:"requestParameters,omitempty"`
	RequestTemplates    map[string]string `json:"requestTemplates,omitempty"`
	TimeoutInMillis     int               `json:"timeoutInMillis,omitempty"`
	// TODO(priority:P3): add cacheNamespace, cacheKeyParameters, connectionId (VPC Link)
}

func (h *Handler) PutIntegration(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Method identifier specified",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req putIntegrationRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Type == "" {
		protocol.WriteJSONError(w, r, errBadRequest("Integration type is required"))
		return
	}

	timeout := req.TimeoutInMillis
	if timeout == 0 {
		timeout = 29000 // AWS default: 29 seconds
	}

	integration := &Integration{
		Type:                req.Type,
		HTTPMethod:          req.HTTPMethod,
		URI:                 req.URI,
		ConnectionType:      req.ConnectionType,
		ContentHandling:     req.ContentHandling,
		Credentials:         req.Credentials,
		PassthroughBehavior: req.PassthroughBehavior,
		RequestParameters:   req.RequestParameters,
		RequestTemplates:    req.RequestTemplates,
		TimeoutInMillis:     timeout,
	}

	method.MethodIntegration = integration
	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, integration)
}

// ---- GetIntegration -------------------------------------------------------

func (h *Handler) GetIntegration(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok || method.MethodIntegration == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "No integration defined for method",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, method.MethodIntegration)
}

// ---- DeleteIntegration ----------------------------------------------------

func (h *Handler) DeleteIntegration(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok || method.MethodIntegration == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "No integration defined for method",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	method.MethodIntegration = nil
	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- PutMethodResponse ----------------------------------------------------

type putMethodResponseRequest struct {
	StatusCode         string          `json:"statusCode"`
	ResponseParameters map[string]bool `json:"responseParameters,omitempty"`
	// TODO(priority:P3): add responseModels
}

func (h *Handler) PutMethodResponse(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")
	statusCode := chi.URLParam(r, "statusCode")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Method identifier specified",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req putMethodResponseRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	mresp := &MethodResponse{
		StatusCode:         statusCode,
		ResponseParameters: req.ResponseParameters,
	}

	if method.MethodResponses == nil {
		method.MethodResponses = make(map[string]*MethodResponse)
	}
	method.MethodResponses[statusCode] = mresp

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, mresp)
}

// ---- PutIntegrationResponse -----------------------------------------------

type putIntegrationResponseRequest struct {
	StatusCode         string            `json:"statusCode"`
	SelectionPattern   string            `json:"selectionPattern,omitempty"`
	ResponseParameters map[string]string `json:"responseParameters,omitempty"`
	ResponseTemplates  map[string]string `json:"responseTemplates,omitempty"`
	ContentHandling    string            `json:"contentHandling,omitempty"`
}

func (h *Handler) PutIntegrationResponse(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")
	statusCode := chi.URLParam(r, "statusCode")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok || method.MethodIntegration == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "No integration defined for method",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req putIntegrationResponseRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	iresp := &IntegrationResponse{
		StatusCode:         statusCode,
		SelectionPattern:   req.SelectionPattern,
		ResponseParameters: req.ResponseParameters,
		ResponseTemplates:  req.ResponseTemplates,
		ContentHandling:    req.ContentHandling,
	}

	if method.MethodIntegration.IntegrationResponses == nil {
		method.MethodIntegration.IntegrationResponses = make(map[string]*IntegrationResponse)
	}
	method.MethodIntegration.IntegrationResponses[statusCode] = iresp

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, iresp)
}

// ---- GetMethodResponse ----------------------------------------------------

// GetMethodResponse handles GET /restapis/{restApiId}/resources/{resourceId}/methods/{httpMethod}/responses/{statusCode}.
func (h *Handler) GetMethodResponse(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")
	statusCode := chi.URLParam(r, "statusCode")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Method identifier specified",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	mresp, ok := method.MethodResponses[statusCode]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Response status code specified: " + statusCode,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, mresp)
}

// ---- DeleteMethodResponse -------------------------------------------------

// DeleteMethodResponse handles DELETE /restapis/{restApiId}/resources/{resourceId}/methods/{httpMethod}/responses/{statusCode}.
func (h *Handler) DeleteMethodResponse(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")
	statusCode := chi.URLParam(r, "statusCode")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Method identifier specified",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if _, ok := method.MethodResponses[statusCode]; !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Response status code specified: " + statusCode,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	delete(method.MethodResponses, statusCode)

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- GetIntegrationResponse -----------------------------------------------

// GetIntegrationResponse handles GET /restapis/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration/responses/{statusCode}.
func (h *Handler) GetIntegrationResponse(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")
	statusCode := chi.URLParam(r, "statusCode")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok || method.MethodIntegration == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "No integration defined for method",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	iresp, ok := method.MethodIntegration.IntegrationResponses[statusCode]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Response status code specified: " + statusCode,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, iresp)
}

// ---- DeleteIntegrationResponse --------------------------------------------

// DeleteIntegrationResponse handles DELETE /restapis/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration/responses/{statusCode}.
func (h *Handler) DeleteIntegrationResponse(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")
	statusCode := chi.URLParam(r, "statusCode")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok || method.MethodIntegration == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "No integration defined for method",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if _, ok := method.MethodIntegration.IntegrationResponses[statusCode]; !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Response status code specified: " + statusCode,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	delete(method.MethodIntegration.IntegrationResponses, statusCode)

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- UpdateResource -------------------------------------------------------

// UpdateResource handles PATCH /restapis/{restApiId}/resources/{resourceId}.
// AWS docs: https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateResource.html
func (h *Handler) UpdateResource(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var req struct {
		PatchOperations []patchOperation `json:"patchOperations,omitempty"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	// Track whether pathPart changed so we can recompute path once.
	pathPartChanged := false
	for _, op := range req.PatchOperations {
		if op.Op != "replace" {
			continue
		}
		switch op.Path {
		case "/pathPart":
			res.PathPart = op.Value
			pathPartChanged = true
		}
	}

	// Recompute the full path from parentID when pathPart changes.
	if pathPartChanged {
		newPath, aerr := h.computeResourcePath(r.Context(), apiID, res.ParentID, res.PathPart)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		res.Path = newPath
	}

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, res)
}

// computeResourcePath rebuilds the full path for a resource given its parentID
// and pathPart. It walks up the ancestor chain, honoured by the store.
func (h *Handler) computeResourcePath(ctx context.Context, apiID, parentID, pathPart string) (string, *protocol.AWSError) {
	if parentID == "" {
		return "/" + pathPart, nil
	}
	parent, aerr := h.store.getResource(ctx, apiID, parentID)
	if aerr != nil {
		return "", aerr
	}
	if parent.Path == "/" {
		return "/" + pathPart, nil
	}
	return parent.Path + "/" + pathPart, nil
}

// ---- UpdateMethod ---------------------------------------------------------

// UpdateMethod handles PATCH /restapis/{restApiId}/resources/{resourceId}/methods/{httpMethod}.
// AWS docs: https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateMethod.html
func (h *Handler) UpdateMethod(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "Invalid Method identifier specified",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		PatchOperations []patchOperation `json:"patchOperations,omitempty"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	for _, op := range req.PatchOperations {
		if op.Op != "replace" {
			continue
		}
		switch op.Path {
		case "/authorizationType":
			method.AuthorizationType = op.Value
		case "/authorizerId":
			method.AuthorizerID = op.Value
		case "/apiKeyRequired":
			method.APIKeyRequired = op.Value == "true"
		}
	}

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, method)
}

// ---- UpdateIntegration ----------------------------------------------------

// UpdateIntegration handles PATCH /restapis/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration.
// AWS docs: https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateIntegration.html
func (h *Handler) UpdateIntegration(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "restApiId")
	resourceID := chi.URLParam(r, "resourceId")
	httpMethod := chi.URLParam(r, "httpMethod")

	res, aerr := h.store.getResource(r.Context(), apiID, resourceID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	method, ok := res.ResourceMethods[httpMethod]
	if !ok || method.MethodIntegration == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotFoundException",
			Message:    "No integration defined for method",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		PatchOperations []patchOperation `json:"patchOperations,omitempty"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	integ := method.MethodIntegration
	for _, op := range req.PatchOperations {
		if op.Op != "replace" {
			continue
		}
		switch op.Path {
		case "/type":
			integ.Type = op.Value
		case "/httpMethod":
			integ.HTTPMethod = op.Value
		case "/uri":
			integ.URI = op.Value
		case "/credentials":
			integ.Credentials = op.Value
		case "/passthroughBehavior":
			integ.PassthroughBehavior = op.Value
		case "/contentHandling":
			integ.ContentHandling = op.Value
		case "/connectionType":
			integ.ConnectionType = op.Value
		}
	}

	if aerr := h.store.putResource(r.Context(), apiID, res); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, integ)
}

// ---- Response helpers -----------------------------------------------------

type restAPIResponse struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	CreatedDate       float64           `json:"createdDate"`
	Version           string            `json:"version,omitempty"`
	EndpointConfig    *EndpointConfig   `json:"endpointConfiguration,omitempty"`
	Tags              map[string]string `json:"tags,omitempty"`
	BinaryMediaTypes  []string          `json:"binaryMediaTypes,omitempty"`
	DisableExecuteAPI bool              `json:"disableExecuteApiEndpoint,omitempty"`
	RootResourceID    string            `json:"rootResourceId"`
	ARN               string            `json:"arn,omitempty"`
}

// restAPIToResponse converts a stored RestAPI to the AWS wire format.
// AWS returns createdDate as epoch seconds (float), not milliseconds.
func restAPIToResponse(api *RestAPI, region string) restAPIResponse {
	return restAPIResponse{
		ID:                api.ID,
		Name:              api.Name,
		Description:       api.Description,
		CreatedDate:       float64(api.CreatedDate) / 1000.0,
		Version:           api.Version,
		EndpointConfig:    api.EndpointConfig,
		Tags:              api.Tags,
		BinaryMediaTypes:  api.BinaryMediaTypes,
		DisableExecuteAPI: api.DisableExecuteAPI,
		RootResourceID:    api.RootResourceID,
		ARN:               protocol.RestAPIARN(region, api.ID),
	}
}

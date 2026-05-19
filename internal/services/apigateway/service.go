// Package apigateway provides emulation of Amazon API Gateway (REST v1 + HTTP v2).
//
// Implements:
//
//	REST v1: CreateRestApi, GetRestApi, GetRestApis, DeleteRestApi, UpdateRestApi,
//	  CreateResource, GetResource, GetResources, DeleteResource,
//	  PutMethod, GetMethod, DeleteMethod,
//	  PutIntegration, GetIntegration, DeleteIntegration,
//	  PutMethodResponse, PutIntegrationResponse,
//	  CreateDeployment, GetDeployments, CreateStage, GetStage, GetStages, UpdateStage, DeleteStage,
//	  CreateAuthorizer, GetAuthorizer, GetAuthorizers, DeleteAuthorizer,
//	  CreateApiKey, GetApiKey, GetApiKeys, DeleteApiKey,
//	  CreateUsagePlan, GetUsagePlan, GetUsagePlans, DeleteUsagePlan,
//	  CreateUsagePlanKey, GetUsagePlanKeys, DeleteUsagePlanKey
//	HTTP v2: CreateApi, GetApi, GetApis, UpdateApi, DeleteApi,
//	  CreateRoute, GetRoute, GetRoutes, DeleteRoute,
//	  CreateIntegration, GetIntegration, GetIntegrations, DeleteIntegration,
//	  CreateDeploymentV2, GetDeploymentsV2, CreateStageV2, GetStageV2, GetStagesV2, DeleteStageV2,
//	  CreateAuthorizer, GetAuthorizer, GetAuthorizers, DeleteAuthorizer
//	Execution: Lambda proxy (AWS_PROXY), MOCK integrations
package apigateway

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/domainregistry"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "apigateway"

// Service implements router.Service for API Gateway using REST paths.
type Service struct {
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured API Gateway Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

// InitBus wires the event bus for API lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// InitLambdaInvoker wires the synchronous Lambda invoker for request execution.
func (s *Service) InitLambdaInvoker(invoker events.FunctionSyncInvoker) {
	s.handler.invoker = invoker
}

// InitCognitoValidator wires the Cognito JWT validator for authorizer enforcement.
// When set, COGNITO_USER_POOLS (REST v1) and JWT (HTTP v2) authorizers will
// validate Bearer tokens on every request before forwarding to the integration.
func (s *Service) InitCognitoValidator(v events.CognitoTokenValidator) {
	s.handler.cognitoValidator = v
}

// InitDomainRegistry wires the custom-domain registry. Existing domain names
// are hydrated lazily on first domain-related request. Safe to call with a nil
// registry; the handlers are all nil-guarded.
func (s *Service) InitDomainRegistry(reg *domainregistry.Registry) {
	s.handler.domainRegistry = reg
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service.
// API Gateway v1 uses /restapis and v2 uses /v2/apis.
func (s *Service) RegisterRoutes(r chi.Router) {
	h := s.handler
	stub := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		protocol.NotImplementedJSON(w, req)
	})

	// ---- REST API v1 management -------------------------------------------
	r.Route("/restapis", func(r chi.Router) {
		r.Post("/", h.CreateRestApi)
		r.Get("/", h.GetRestApis)
		r.Get("/{restApiId}", h.GetRestApi)
		r.Delete("/{restApiId}", h.DeleteRestApi)
		r.Patch("/{restApiId}", h.UpdateRestApi)

		// Resources
		r.Post("/{restApiId}/resources/{parentId}", h.CreateResource)
		r.Get("/{restApiId}/resources", h.GetResources)
		r.Get("/{restApiId}/resources/{resourceId}", h.GetResource)
		r.Delete("/{restApiId}/resources/{resourceId}", h.DeleteResource)
		r.Patch("/{restApiId}/resources/{resourceId}", h.UpdateResource)

		// Methods
		r.Put("/{restApiId}/resources/{resourceId}/methods/{httpMethod}", h.PutMethod)
		r.Get("/{restApiId}/resources/{resourceId}/methods/{httpMethod}", h.GetMethod)
		r.Delete("/{restApiId}/resources/{resourceId}/methods/{httpMethod}", h.DeleteMethod)
		r.Patch("/{restApiId}/resources/{resourceId}/methods/{httpMethod}", h.UpdateMethod)

		// Integrations
		r.Put("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration", h.PutIntegration)
		r.Get("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration", h.GetIntegration)
		r.Delete("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration", h.DeleteIntegration)
		r.Patch("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration", h.UpdateIntegration)

		// Method responses
		r.Put("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/responses/{statusCode}", h.PutMethodResponse)
		r.Get("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/responses/{statusCode}", h.GetMethodResponse)
		r.Delete("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/responses/{statusCode}", h.DeleteMethodResponse)

		// Integration responses
		r.Put("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration/responses/{statusCode}", h.PutIntegrationResponse)
		r.Get("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration/responses/{statusCode}", h.GetIntegrationResponse)
		r.Delete("/{restApiId}/resources/{resourceId}/methods/{httpMethod}/integration/responses/{statusCode}", h.DeleteIntegrationResponse)

		// Deployments
		r.Post("/{restApiId}/deployments", h.CreateDeployment)
		r.Get("/{restApiId}/deployments", h.GetDeployments)

		// Stages
		r.Post("/{restApiId}/stages", h.CreateStage)
		r.Get("/{restApiId}/stages", h.GetStages)
		r.Get("/{restApiId}/stages/{stageName}", h.GetStage)
		r.Patch("/{restApiId}/stages/{stageName}", h.UpdateStage)
		r.Delete("/{restApiId}/stages/{stageName}", h.DeleteStage)

		// Models
		r.Post("/{restApiId}/models", h.CreateModel)
		r.Get("/{restApiId}/models", h.GetModels)
		r.Get("/{restApiId}/models/{modelName}", h.GetModel)
		r.Delete("/{restApiId}/models/{modelName}", h.DeleteModel)

		// Authorizers
		r.Post("/{restApiId}/authorizers", h.CreateAuthorizer)
		r.Get("/{restApiId}/authorizers", h.GetAuthorizers)
		r.Get("/{restApiId}/authorizers/{authorizerId}", h.GetAuthorizer)
		r.Delete("/{restApiId}/authorizers/{authorizerId}", h.DeleteAuthorizer)

		// Request Validators
		r.Post("/{restApiId}/requestvalidators", h.CreateRequestValidator)
		r.Get("/{restApiId}/requestvalidators", h.GetRequestValidators)
		r.Delete("/{restApiId}/requestvalidators/{requestvalidatorId}", h.DeleteRequestValidator)

		// REST API execution — Lambda proxy / MOCK
		r.HandleFunc("/{restApiId}/{stageName}/_user_request_/*", h.ExecuteRestAPI)
		r.HandleFunc("/{restApiId}/{stageName}/_user_request_/", h.ExecuteRestAPI)
	})

	// ---- REST API v1: API Keys --------------------------------------------
	r.Route("/apikeys", func(r chi.Router) {
		r.Post("/", h.CreateApiKey)
		r.Get("/", h.GetApiKeys)
		r.Get("/{apiKey}", h.GetApiKey)
		r.Delete("/{apiKey}", h.DeleteApiKey)
	})

	// ---- REST API v1: Usage Plans -----------------------------------------
	r.Route("/usageplans", func(r chi.Router) {
		r.Post("/", h.CreateUsagePlan)
		r.Get("/", h.GetUsagePlans)
		r.Get("/{usagePlanId}", h.GetUsagePlan)
		r.Delete("/{usagePlanId}", h.DeleteUsagePlan)
		r.Post("/{usagePlanId}/keys", h.CreateUsagePlanKey)
		r.Get("/{usagePlanId}/keys", h.GetUsagePlanKeys)
		r.Delete("/{usagePlanId}/keys/{keyId}", h.DeleteUsagePlanKey)
	})

	// ---- REST API v1: Domain Names ----------------------------------------
	r.Route("/domainnames", func(r chi.Router) {
		r.Post("/", h.CreateDomainName)
		r.Get("/", h.GetDomainNames)
		r.Delete("/{domainName}", h.DeleteDomainName)
		r.Post("/{domainName}/basepathmappings", h.CreateBasePathMapping)
		r.Get("/{domainName}/basepathmappings", h.GetBasePathMappings)
	})

	// ---- REST API v1: Account ---------------------------------------------
	r.Get("/account", h.GetAccount)
	r.Patch("/account", h.UpdateAccount)

	// ---- REST API v1: VPC Links -------------------------------------------
	r.Route("/vpclinks", func(r chi.Router) {
		r.Post("/", h.CreateVpcLink)
		r.Get("/", h.GetVpcLinks)
		r.Delete("/{vpcLinkId}", h.DeleteVpcLink)
	})

	// ---- REST API v1: Tags ------------------------------------------------
	r.Put("/tags/*", h.TagResource)
	// POST mirrors PUT — AWS AppRegistry uses POST for TagResource, and its
	// SDK shares this router endpoint; see internal/services/appregistry/service.go.
	r.Post("/tags/*", h.TagResource)
	r.Delete("/tags/*", h.UntagResource)
	r.Get("/tags/*", h.GetTags)

	// ---- HTTP API v2 management -------------------------------------------
	// NOTE: /v2/apis routes are NOT registered here because they share the
	// path with AppSync Events API. Instead they are exposed via
	// V2APIRouter() and wired by the main router using SigV4 service-name
	// dispatch.

	// ---- HTTP API v2: Domain Names ----------------------------------------
	r.Route("/v2/domainnames", func(r chi.Router) {
		r.Post("/", h.CreateV2DomainName)
		r.Get("/", h.GetV2DomainNames)
		r.Delete("/{domainName}", h.DeleteV2DomainName)
		r.Post("/{domainName}/apimappings", h.CreateV2ApiMapping)
		r.Get("/{domainName}/apimappings", h.GetV2ApiMappings)
	})

	// ---- HTTP API v2: VPC Links -------------------------------------------
	r.Route("/v2/vpclinks", func(r chi.Router) {
		r.Post("/", h.CreateV2VpcLink)
		r.Get("/", h.GetV2VpcLinks)
		r.Delete("/{vpcLinkId}", h.DeleteV2VpcLink)
	})

	// ---- HTTP API v2: Tags ------------------------------------------------
	r.Post("/v2/tags/*", h.TagV2Resource)
	r.Delete("/v2/tags/*", h.UntagV2Resource)
	r.Get("/v2/tags/*", h.GetV2Tags)

	// NOTE: v2 execution routes (/v2/apis/{apiId}/stages/...) are registered
	// inside V2APIRouter because the /v2/apis path is dispatched by the main
	// router (shared with AppSync Events API).
	r.HandleFunc("/@connections/{apiId}/{stageName}/*", func(w http.ResponseWriter, req *http.Request) {
		stub.ServeHTTP(w, req)
	})
}

// V2APIRouter returns a chi.Router for the API Gateway v2 /v2/apis management
// routes. This is mounted by the main router via service-name dispatch so that
// it coexists with AppSync Events API on the same path prefix.
func (s *Service) V2APIRouter() chi.Router {
	h := s.handler
	r := chi.NewRouter()

	r.Post("/", h.CreateV2Api)
	r.Get("/", h.GetV2Apis)
	r.Get("/{apiId}", h.GetV2Api)
	r.Patch("/{apiId}", h.UpdateV2Api)
	r.Delete("/{apiId}", h.DeleteV2Api)

	// Routes
	r.Post("/{apiId}/routes", h.CreateV2Route)
	r.Get("/{apiId}/routes", h.GetV2Routes)
	r.Get("/{apiId}/routes/{routeId}", h.GetV2Route)
	r.Delete("/{apiId}/routes/{routeId}", h.DeleteV2Route)
	r.Patch("/{apiId}/routes/{routeId}", h.UpdateV2Route)

	// Integrations
	r.Post("/{apiId}/integrations", h.CreateV2Integration)
	r.Get("/{apiId}/integrations", h.GetV2Integrations)
	r.Get("/{apiId}/integrations/{integrationId}", h.GetV2Integration)
	r.Delete("/{apiId}/integrations/{integrationId}", h.DeleteV2Integration)
	r.Patch("/{apiId}/integrations/{integrationId}", h.UpdateV2Integration)

	// Deployments
	r.Post("/{apiId}/deployments", h.CreateV2Deployment)
	r.Get("/{apiId}/deployments", h.GetV2Deployments)

	// Stages
	r.Post("/{apiId}/stages", h.CreateV2Stage)
	r.Get("/{apiId}/stages", h.GetV2Stages)
	r.Get("/{apiId}/stages/{stageName}", h.GetV2Stage)
	r.Delete("/{apiId}/stages/{stageName}", h.DeleteV2Stage)
	r.Patch("/{apiId}/stages/{stageName}", h.UpdateV2Stage)

	// Authorizers
	r.Post("/{apiId}/authorizers", h.CreateV2Authorizer)
	r.Get("/{apiId}/authorizers", h.GetV2Authorizers)
	r.Get("/{apiId}/authorizers/{authorizerId}", h.GetV2Authorizer)
	r.Delete("/{apiId}/authorizers/{authorizerId}", h.DeleteV2Authorizer)

	// Execution
	// TODO(priority:P2): implement v2 execution routes
	r.HandleFunc("/{apiId}/stages/{stageName}/*", h.ExecuteV2API)

	return r
}

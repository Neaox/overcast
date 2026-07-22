// Package appsync provides emulation of AWS AppSync (managed GraphQL).
//
// Implemented: GraphQL API CRUD, schema upload/status, API key CRUD,
// data source CRUD, function CRUD, resolver CRUD, tagging, GraphQL query
// and mutation execution (NONE/HTTP/Lambda/DynamoDB data sources, UNIT and
// PIPELINE resolvers, VTL and APPSYNC_JS runtimes, full authentication),
// EvaluateMappingTemplate, EvaluateCode, real-time WebSocket subscriptions,
// merged API management (source API associations, schema merging),
// Events API (CRUD + channel namespace management).
package appsync

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "appsync"

// Service implements router.Service for AppSync using REST paths.
type Service struct {
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured AppSync Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	s := newStore(store, cfg.Region)
	sp := newSchemaParser()
	return &Service{
		log:     log,
		handler: newHandler(cfg, s, log, clk, sp),
	}
}

// InitBus wires the event bus for API lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// InitLambdaInvoker wires the Lambda invoker for AWS_LAMBDA data source dispatch.
func (s *Service) InitLambdaInvoker(invoker events.FunctionSyncInvoker) {
	s.handler.invoker = invoker
}

// InitDynamoDBInvoker wires the DynamoDB invoker for AMAZON_DYNAMODB data source dispatch.
func (s *Service) InitDynamoDBInvoker(invoker events.DynamoDBInvoker) {
	s.handler.dynamoInvoker = invoker
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

func (s *Service) TargetPrefix() string { return "AppSync." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if codec.Supports(s.SupportedProtocols(), c) {
			if typed, ok := s.handler.typedOp[opName]; ok {
				typed.Invoke(w, r, c)
				return
			}
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// RegisterRoutes satisfies router.Service.
// AppSync uses REST paths under /v1/apis (and /v1/domainnames, etc.).
//
// NOTE: /v1/tags routes are NOT registered here — see TagsRouter.
func (s *Service) RegisterRoutes(r chi.Router) {
	h := s.handler

	// ── Domain names (stubs) ─────────────────────────────────────────────
	r.Route("/v1/domainnames", func(r chi.Router) {
		r.Post("/", h.CreateDomainName)
		r.Get("/", h.ListDomainNames)
		r.Get("/{domainName}", h.GetDomainName)
		r.Post("/{domainName}", h.UpdateDomainName)
		r.Delete("/{domainName}", h.DeleteDomainName)
		r.Post("/{domainName}/apiassociation", h.AssociateApi)
		r.Get("/{domainName}/apiassociation", h.GetApiAssociation)
		r.Delete("/{domainName}/apiassociation", h.DisassociateApi)
	})

	// ── GraphQL APIs ─────────────────────────────────────────────────────
	r.Route("/v1/apis", func(r chi.Router) {
		r.Post("/", h.CreateGraphqlApi)
		r.Get("/", h.ListGraphqlApis)

		r.Route("/{apiId}", func(r chi.Router) {
			r.Get("/", h.GetGraphqlApi)
			r.Post("/", h.UpdateGraphqlApi)
			r.Delete("/", h.DeleteGraphqlApi)

			// Schema
			r.Post("/schemacreation", h.StartSchemaCreation)
			r.Get("/schemacreation", h.GetSchemaCreationStatus)
			r.Get("/schema", h.GetIntrospectionSchema)

			// API keys
			r.Post("/apikeys", h.CreateApiKey)
			r.Get("/apikeys", h.ListApiKeys)
			r.Post("/apikeys/{keyId}", h.UpdateApiKey)
			r.Delete("/apikeys/{keyId}", h.DeleteApiKey)

			// Data sources
			r.Post("/datasources", h.CreateDataSource)
			r.Get("/datasources", h.ListDataSources)
			r.Get("/datasources/{name}", h.GetDataSource)
			r.Post("/datasources/{name}", h.UpdateDataSource)
			r.Delete("/datasources/{name}", h.DeleteDataSource)

			// Functions
			r.Post("/functions", h.CreateFunction)
			r.Get("/functions", h.ListFunctions)
			r.Get("/functions/{functionId}", h.GetFunction)
			r.Post("/functions/{functionId}", h.UpdateFunction)
			r.Delete("/functions/{functionId}", h.DeleteFunction)

			// Resolvers by function (stub)
			r.Get("/functions/{functionId}/resolvers", h.ListResolversByFunction)

			// Resolvers (nested under types)
			r.Route("/types/{typeName}", func(r chi.Router) {
				// Type management (stubs)
				r.Get("/", h.GetType)
				r.Post("/", h.UpdateType)
				r.Delete("/", h.DeleteType)

				r.Post("/resolvers", h.CreateResolver)
				r.Get("/resolvers", h.ListResolvers)
				r.Get("/resolvers/{fieldName}", h.GetResolver)
				r.Post("/resolvers/{fieldName}", h.UpdateResolver)
				r.Delete("/resolvers/{fieldName}", h.DeleteResolver)
			})

			// Types management (stubs)
			r.Post("/types", h.CreateType)
			r.Get("/types", h.ListTypes)

			// Caching (stubs)
			r.Post("/ApiCaches", h.CreateApiCache)
			r.Get("/ApiCaches", h.GetApiCache)
			r.Post("/ApiCaches/update", h.UpdateApiCache)
			r.Delete("/ApiCaches", h.DeleteApiCache)
			r.Delete("/FlushCache", h.FlushApiCache)

			// Environment variables (stubs)
			r.Put("/environmentVariables", h.PutGraphqlApiEnvironmentVariables)
			r.Get("/environmentVariables", h.GetGraphqlApiEnvironmentVariables)

			// Merged API associations (listed via /v1/apis path)
			r.Get("/sourceApiAssociations", h.ListSourceApiAssociations)

			// Evaluation
			r.Post("/evaluateMappingTemplate", h.EvaluateMappingTemplate)
			r.Post("/evaluateCode", h.EvaluateCode)
		})

		// Catch-all for any unmatched sub-paths.
		r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
			protocol.NotImplementedJSON(w, req)
		})
	})

	// ── Merged API source associations ───────────────────────────────────
	// SDK path: /v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations
	r.Route("/v1/mergedApis/{mergedApiIdentifier}", func(r chi.Router) {
		r.Post("/sourceApiAssociations", h.AssociateSourceGraphqlApi)
		r.Get("/sourceApiAssociations/{associationId}", h.GetSourceApiAssociation)
		r.Delete("/sourceApiAssociations/{associationId}", h.DisassociateSourceGraphqlApi)
		r.Post("/sourceApiAssociations/{associationId}/merge", h.StartSchemaMerge)
	})

	// ── Source API merged associations ───────────────────────────────────
	// SDK path: /v1/sourceApis/{sourceApiIdentifier}/mergedApiAssociations
	r.Route("/v1/sourceApis/{sourceApiIdentifier}", func(r chi.Router) {
		r.Post("/mergedApiAssociations", h.AssociateMergedGraphqlApi)
		r.Delete("/mergedApiAssociations/{associationId}", h.DisassociateMergedGraphqlApi)
	})

	// NOTE: Events API (v2) routes are NOT registered here because they
	// share the /v2/apis path with API Gateway v2. Instead they are
	// exposed via EventsAPIRouter() and wired by the main router using
	// SigV4 service-name dispatch. Tag routes are similarly excluded — see
	// TagsRouter — because /v1/tags is shared with other taggable services
	// (e.g. MSK).

	// ── GraphQL execution endpoint ───────────────────────────────────────
	// GraphQL clients POST queries here. In real AWS, this lives at
	// https://{apiId}.appsync-api.{region}.amazonaws.com/graphql.
	// In the emulator, it is exposed under /_appsync/ so SDK clients can
	// be pointed at a local endpoint URL.
	r.Route("/_appsync/{apiId}", func(r chi.Router) {
		r.Post("/graphql", h.ExecuteGraphQL)
		r.Get("/realtime", h.HandleWebSocket)
	})
}

// EventsAPIRouter returns a chi.Router for the AppSync Events API routes
// that live under /v2/apis. This is mounted by the main router via
// service-name dispatch so that it coexists with API Gateway v2 on the
// same path prefix.
func (s *Service) EventsAPIRouter() chi.Router {
	h := s.handler
	r := chi.NewRouter()
	r.Post("/", h.CreateApi)
	r.Get("/", h.ListApis)

	r.Route("/{apiId}", func(r chi.Router) {
		r.Get("/", h.GetApi)
		r.Post("/", h.UpdateApi)
		r.Delete("/", h.DeleteApi)

		r.Post("/channelNamespaces", h.CreateChannelNamespace)
		r.Get("/channelNamespaces", h.ListChannelNamespaces)
		r.Get("/channelNamespaces/{name}", h.GetChannelNamespace)
		r.Post("/channelNamespaces/{name}", h.UpdateChannelNamespace)
		r.Delete("/channelNamespaces/{name}", h.DeleteChannelNamespace)
	})
	return r
}

// TagsRouter returns a chi.Router for the AppSync tagging routes that live
// under /v1/tags. This is mounted by the main router alongside other
// taggable services' tag routers and dispatched by the resourceArn's
// service segment, since /v1/tags/{resourceArn} is shared across services
// (e.g. MSK) that each own tagging for their own ARNs.
func (s *Service) TagsRouter() chi.Router {
	h := s.handler
	r := chi.NewRouter()
	r.Post("/*", h.TagResource)
	r.Delete("/*", h.UntagResource)
	r.Get("/*", h.ListTagsForResource)
	return r
}

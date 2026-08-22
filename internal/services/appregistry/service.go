// Package appregistry provides emulation of AWS Service Catalog AppRegistry.
//
// Wire protocol: REST-JSON.
// Endpoints are served under /applications, which the main router dispatches
// between this service and AppConfig — see ApplicationsRouter.
//
// The primary use in Overcast is as a grouping primitive: a CloudFormation
// stack (or the resources inside one) can be associated with an application,
// and the web UI uses that association to render a banner on every resource
// detail page and to group resources on the system map. The provisioner also
// honours CDK's `awsApplication` tag — when a CloudFormation template tags a
// resource with `awsApplication=<app-arn>`, the provisioner automatically
// associates that resource with the owning application. See
// docs/services/appregistry.md for the support matrix.
package appregistry

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "appregistry"

// Service implements router.Service for AppRegistry.
type Service struct {
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured AppRegistry Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

// InitBus wires the event bus for application lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// ApplicationsRouter returns the /applications sub-tree, for the main router to
// dispatch to.
//
// AppRegistry no longer registers this path itself. AWS AppConfig models the
// same /applications tree, and on a single-endpoint emulator the two can only
// be told apart by the SigV4 credential scope, so the main router owns the path
// and picks an owner per request — the same shape it already uses for /v2/apis.
// AppRegistry is the dispatcher's fallback owner, which keeps unsigned and
// servicecatalog-scoped traffic answering exactly as it always has. See #854.
func (s *Service) ApplicationsRouter() chi.Router {
	h := s.handler
	r := chi.NewRouter()

	r.Post("/", h.CreateApplication)
	r.Get("/", h.ListApplications)
	r.Get("/{application}", h.GetApplication)
	r.Delete("/{application}", h.DeleteApplication)
	r.Patch("/{application}", h.UpdateApplication)

	// Resource associations
	r.Get("/{application}/resources", h.ListAssociatedResources)
	r.Put("/{application}/resources/{resourceType}/{resource}", h.AssociateResource)
	r.Delete("/{application}/resources/{resourceType}/{resource}", h.DisassociateResource)
	r.Get("/{application}/resources/{resourceType}/{resource}", h.GetAssociatedResource)

	// Attribute group associations (inert tier)
	r.Get("/{application}/attribute-groups", h.ListAssociatedAttributeGroups)
	r.Put("/{application}/attribute-groups/{attributeGroup}", h.AssociateAttributeGroup)
	r.Delete("/{application}/attribute-groups/{attributeGroup}", h.DisassociateAttributeGroup)

	return r
}

// RegisterRoutes satisfies router.Service. /applications is registered by the
// main router — see ApplicationsRouter.
func (s *Service) RegisterRoutes(r chi.Router) {
	h := s.handler

	// Attribute groups top-level (inert tier)
	r.Route("/attribute-groups", func(r chi.Router) {
		r.Post("/", h.CreateAttributeGroup)
		r.Get("/", h.ListAttributeGroups)
		r.Get("/{attributeGroup}", h.GetAttributeGroup)
		r.Patch("/{attributeGroup}", h.UpdateAttributeGroup)
		r.Delete("/{attributeGroup}", h.DeleteAttributeGroup)
	})

	// NOTE: AppRegistry's tag APIs (POST/DELETE/GET /tags/{resourceArn}) share
	// a path with API Gateway's generic tag store. The main router's /tags
	// dispatcher routes "servicecatalog" ARNs to that store by name (#976), and
	// its handlers keep tags in an ARN-keyed map, so the AppRegistry SDK's tag
	// calls work transparently — API Gateway's TagsRouter registers the POST
	// verb (its own SDK uses PUT for TagResource) to cover the last method gap.
}

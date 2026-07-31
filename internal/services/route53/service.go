// Package route53 emulates Amazon Route 53 at inert level: hosted zones,
// resource record sets, tags, and health checks exist as real metadata with
// AWS-faithful validation, defaults, and error codes — but no DNS is served.
//
// Implemented operations (REST-XML at /2013-04-01/):
//
//	Hosted zones:   CreateHostedZone, GetHostedZone, ListHostedZones,
//	                ListHostedZonesByName, GetHostedZoneCount,
//	                UpdateHostedZoneComment, DeleteHostedZone
//	Record sets:    ChangeResourceRecordSets, ListResourceRecordSets
//	Changes:        GetChange
//	Tags:           ChangeTagsForResource, ListTagsForResource, ListTagsForResources
//	Health checks:  CreateHealthCheck, GetHealthCheck, ListHealthChecks,
//	                GetHealthCheckCount, UpdateHealthCheck, DeleteHealthCheck
//
// Route 53 is a global service — resources are stored without a region key.
// Documented divergences: ChangeResourceRecordSets applies synchronously, so
// change status is INSYNC immediately (no PENDING phase), and GetChange
// returns INSYNC for unknown change IDs instead of NoSuchChange so that
// CDK/CLI waiters always converge.
package route53

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "route53"

// Service implements router.Service for Route 53.
type Service struct {
	cfg     *config.Config
	store   state.Store
	clk     clock.Clock
	log     *serviceutil.ServiceLogger
	typedOp map[string]op.Operation
}

// New returns a configured Route 53 service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		cfg:   cfg,
		store: st,
		clk:   clk,
		log:   serviceutil.NewServiceLogger(logger, serviceName),
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

// DispatchQuery satisfies router.QueryDispatcher. Route 53 natively uses
// REST-XML (path-based routing), but the typed operations also accept
// Query-protocol form-encoded POST requests with Action fields.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !serviceutil.AllowProtocolDrift(s.cfg, s.log, opName, c, s.SupportedProtocols()) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "Route 53 does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		// No typed impl for this op — Route 53 has no separate legacy
		// Query dispatch (its non-typed implementation is REST-XML
		// path-routed), so this falls through to the same
		// NotImplementedQueryXML response as an unresolved codec below.
	}
	protocol.NotImplementedQueryXML(w, r)
}

// OwnsAction satisfies router.QueryActionOwner.
func (s *Service) OwnsAction(action string) bool {
	_, ok := s.typedOp[action]
	return ok
}

func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/2013-04-01", func(r chi.Router) {
		// Hosted zone operations
		r.Post("/hostedzone", s.createHostedZone)
		r.Get("/hostedzone", s.listHostedZones)
		r.Get("/hostedzonesbyname", s.listHostedZonesByName)
		r.Get("/hostedzonecount", s.getHostedZoneCount)
		r.Get("/hostedzone/{zoneId}", s.getHostedZone)
		r.Post("/hostedzone/{zoneId}", s.updateHostedZoneComment)
		r.Delete("/hostedzone/{zoneId}", s.deleteHostedZone)

		// Resource record set operations
		r.Post("/hostedzone/{zoneId}/rrset", s.changeResourceRecordSets)
		r.Get("/hostedzone/{zoneId}/rrset", s.listResourceRecordSets)

		// Change status
		r.Get("/change/{changeId}", s.getChange)

		// Tags (hosted zones and health checks)
		r.Post("/tags/{resourceType}", s.listTagsForResources)
		r.Post("/tags/{resourceType}/{resourceId}", s.changeTagsForResource)
		r.Get("/tags/{resourceType}/{resourceId}", s.listTagsForResource)

		// Health checks
		r.Post("/healthcheck", s.createHealthCheck)
		r.Get("/healthcheck", s.listHealthChecks)
		r.Get("/healthcheckcount", s.getHealthCheckCount)
		r.Get("/healthcheck/{healthCheckId}", s.getHealthCheckHandler)
		r.Post("/healthcheck/{healthCheckId}", s.updateHealthCheck)
		r.Delete("/healthcheck/{healthCheckId}", s.deleteHealthCheckHandler)
	})
}

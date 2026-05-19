// Package cloudfront provides emulation of Amazon CloudFront (CDN).
//
// Implemented: Distribution CRUD, Invalidations, Tagging, CreateDistributionWithTags,
// OAC CRUD, Cache Policy CRUD, Origin Request Policy CRUD, Response Headers Policy CRUD,
// Legacy OAI CRUD, Continuous Deployment Policy CRUD, staging distribution routing,
// CloudFront Functions execution (viewer-request/viewer-response via goja).
package cloudfront

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "cloudfront"

// Service implements router.Service for CloudFront using REST paths.
type Service struct {
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured CloudFront Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	s := newStore(store, cfg.Region)
	return &Service{
		log:     log,
		handler: newHandler(cfg, s, log, clk),
	}
}

// InitBus wires the event bus for distribution lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service.
// CloudFront uses /2020-05-31/* for the current API version.
func (s *Service) RegisterRoutes(r chi.Router) {
	h := s.handler

	r.Route("/2020-05-31", func(r chi.Router) {
		// ── Distributions ────────────────────────────────────────────
		r.Post("/distribution", func(w http.ResponseWriter, req *http.Request) {
			if _, ok := req.URL.Query()["WithTags"]; ok {
				h.CreateDistributionWithTags(w, req)
			} else {
				h.CreateDistribution(w, req)
			}
		})
		r.Get("/distribution", h.ListDistributions)
		r.Route("/distribution/{id}", func(r chi.Router) {
			r.Get("/", h.GetDistribution)
			r.Delete("/", h.DeleteDistribution)
			r.Get("/config", h.GetDistributionConfig)
			r.Put("/config", h.UpdateDistribution)

			// Invalidations (stubs)
			r.Post("/invalidation", h.CreateInvalidation)
			r.Get("/invalidation", h.ListInvalidations)
			r.Get("/invalidation/{invalidationId}", h.GetInvalidation)

			// Monitoring (stubs)
			r.Post("/monitoring-subscription", h.CreateMonitoringSubscription)
			r.Get("/monitoring-subscription", h.GetMonitoringSubscription)
			r.Delete("/monitoring-subscription", h.DeleteMonitoringSubscription)
		})

		// ── Tagging ──────────────────────────────────────────────────
		r.Get("/tagging", h.ListTagsForResource)
		r.Post("/tagging", func(w http.ResponseWriter, req *http.Request) {
			switch req.URL.Query().Get("Operation") {
			case "Tag":
				h.TagResource(w, req)
			case "Untag":
				h.UntagResource(w, req)
			default:
				h.TagResource(w, req)
			}
		})

		// ── Origin Access Controls ───────────────────────────────────
		r.Post("/origin-access-control", h.CreateOriginAccessControl)
		r.Get("/origin-access-control", h.ListOriginAccessControls)
		r.Route("/origin-access-control/{id}", func(r chi.Router) {
			r.Get("/", h.GetOriginAccessControl)
			r.Delete("/", h.DeleteOriginAccessControl)
			r.Put("/config", h.UpdateOriginAccessControl)
		})

		// ── Origin Access Identities (legacy) ────────────────────────
		r.Post("/origin-access-identity/cloudfront", h.CreateCloudFrontOriginAccessIdentity)
		r.Get("/origin-access-identity/cloudfront", h.ListCloudFrontOriginAccessIdentities)
		r.Route("/origin-access-identity/cloudfront/{id}", func(r chi.Router) {
			r.Get("/", h.GetCloudFrontOriginAccessIdentity)
			r.Delete("/", h.DeleteCloudFrontOriginAccessIdentity)
			r.Get("/config", h.GetCloudFrontOriginAccessIdentityConfig)
			r.Put("/config", h.UpdateCloudFrontOriginAccessIdentity)
		})

		// ── Cache Policies ───────────────────────────────────────────
		r.Post("/cache-policy", h.CreateCachePolicy)
		r.Get("/cache-policy", h.ListCachePolicies)
		r.Route("/cache-policy/{id}", func(r chi.Router) {
			r.Get("/", h.GetCachePolicy)
			r.Delete("/", h.DeleteCachePolicy)
			r.Put("/", h.UpdateCachePolicy)
			r.Get("/config", h.GetCachePolicyConfig)
		})

		// ── Origin Request Policies ──────────────────────────────────
		r.Post("/origin-request-policy", h.CreateOriginRequestPolicy)
		r.Get("/origin-request-policy", h.ListOriginRequestPolicies)
		r.Route("/origin-request-policy/{id}", func(r chi.Router) {
			r.Get("/", h.GetOriginRequestPolicy)
			r.Delete("/", h.DeleteOriginRequestPolicy)
			r.Put("/", h.UpdateOriginRequestPolicy)
			r.Get("/config", h.GetOriginRequestPolicyConfig)
		})

		// ── Response Headers Policies ────────────────────────────────
		r.Post("/response-headers-policy", h.CreateResponseHeadersPolicy)
		r.Get("/response-headers-policy", h.ListResponseHeadersPolicies)
		r.Route("/response-headers-policy/{id}", func(r chi.Router) {
			r.Get("/", h.GetResponseHeadersPolicy)
			r.Delete("/", h.DeleteResponseHeadersPolicy)
			r.Put("/", h.UpdateResponseHeadersPolicy)
			r.Get("/config", h.GetResponseHeadersPolicyConfig)
		})

		// ── Key Groups ───────────────────────────────────────────────
		r.Post("/key-group", h.CreateKeyGroup)
		r.Get("/key-group", h.ListKeyGroups)
		r.Route("/key-group/{id}", func(r chi.Router) {
			r.Get("/", h.GetKeyGroup)
			r.Delete("/", h.DeleteKeyGroup)
			r.Put("/", h.UpdateKeyGroup)
			r.Get("/config", h.GetKeyGroupConfig)
		})

		// ── Public Keys ──────────────────────────────────────────────
		r.Post("/public-key", h.CreatePublicKey)
		r.Get("/public-key", h.ListPublicKeys)
		r.Route("/public-key/{id}", func(r chi.Router) {
			r.Get("/", h.GetPublicKey)
			r.Delete("/", h.DeletePublicKey)
			r.Put("/config", h.UpdatePublicKey)
			r.Get("/config", h.GetPublicKeyConfig)
		})

		// ── CloudFront Functions ─────────────────────────────────────
		r.Post("/function", h.CreateFunction)
		r.Get("/function", h.ListFunctions)
		r.Route("/function/{name}", func(r chi.Router) {
			r.Get("/", h.GetFunction)
			r.Delete("/", h.DeleteFunction)
			r.Put("/", h.UpdateFunction)
			r.Get("/describe", h.DescribeFunction)
			r.Post("/test", h.TestFunction)
			r.Post("/publish", h.PublishFunction)
		})

		// ── Field-Level Encryption ───────────────────────────────────
		r.Post("/field-level-encryption", h.CreateFieldLevelEncryptionConfig)
		r.Get("/field-level-encryption", h.ListFieldLevelEncryptionConfigs)
		r.Route("/field-level-encryption/{id}", func(r chi.Router) {
			r.Get("/", h.GetFieldLevelEncryption)
			r.Delete("/", h.DeleteFieldLevelEncryption)
			r.Get("/config", h.GetFieldLevelEncryptionConfig)
			r.Put("/config", h.UpdateFieldLevelEncryptionConfig)
		})

		r.Post("/field-level-encryption-profile", h.CreateFieldLevelEncryptionProfile)
		r.Get("/field-level-encryption-profile", h.ListFieldLevelEncryptionProfiles)
		r.Route("/field-level-encryption-profile/{id}", func(r chi.Router) {
			r.Get("/", h.GetFieldLevelEncryptionProfile)
			r.Delete("/", h.DeleteFieldLevelEncryptionProfile)
			r.Get("/config", h.GetFieldLevelEncryptionProfileConfig)
			r.Put("/config", h.UpdateFieldLevelEncryptionProfile)
		})

		// ── Continuous Deployment ────────────────────────────────────
		r.Post("/continuous-deployment-policy", h.CreateContinuousDeploymentPolicy)
		r.Get("/continuous-deployment-policy", h.ListContinuousDeploymentPolicies)
		r.Route("/continuous-deployment-policy/{id}", func(r chi.Router) {
			r.Get("/", h.GetContinuousDeploymentPolicy)
			r.Delete("/", h.DeleteContinuousDeploymentPolicy)
			r.Put("/", h.UpdateContinuousDeploymentPolicy)
			r.Get("/config", h.GetContinuousDeploymentPolicyConfig)
		})

		// ── Realtime Log Configs ─────────────────────────────────────
		r.Post("/realtime-log-config", h.CreateRealtimeLogConfig)
		r.Get("/realtime-log-config", h.ListRealtimeLogConfigs)
		r.Put("/realtime-log-config", h.UpdateRealtimeLogConfig)
		r.Post("/get-realtime-log-config", h.GetRealtimeLogConfig)
		r.Post("/delete-realtime-log-config", h.DeleteRealtimeLogConfig)

		// ── Monitoring (plural path variant) ────────────────────────
		// The AWS SDK uses /distributions/{id}/monitoring-subscription
		// (plural) while single-distribution CRUD uses the singular form.
		r.Route("/distributions/{id}", func(r chi.Router) {
			r.Post("/monitoring-subscription", h.CreateMonitoringSubscription)
			r.Get("/monitoring-subscription", h.GetMonitoringSubscription)
			r.Delete("/monitoring-subscription", h.DeleteMonitoringSubscription)
		})
	})

	// ── Origin Proxy ─────────────────────────────────────────────────
	// Internal endpoint for proxying requests through CloudFront distributions
	// to their configured origins. Not part of the AWS API surface.
	r.HandleFunc("/_cloudfront/{distId}/*", h.ProxyRequest)
}

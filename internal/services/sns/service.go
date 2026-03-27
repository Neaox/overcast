// Package sns is a stub — handlers are implemented test-first.
// See docs/services/sns.md for the support matrix and implementation order.
//
// Implementation order (TDD):
//  1. CreateTopic / DeleteTopic / ListTopics
//  2. Subscribe (sqs, lambda, http protocols) / Unsubscribe / ListSubscriptionsByTopic
//  3. Publish (fan-out to subscriptions)
//  4. GetTopicAttributes / SetTopicAttributes
package sns

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

const serviceName = "sns"

// Service implements router.Service for SNS.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured SNS Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// TargetPrefix returns the X-Amz-Target prefix for SNS dispatch.
func (s *Service) TargetPrefix() string { return "AmazonSimpleNotificationService." }

// RegisterRoutes is a no-op — SNS uses POST / which is handled by the
// router's target dispatcher shared with SQS and DynamoDB.
func (s *Service) RegisterRoutes(r chi.Router) {}

// Dispatch routes to the correct SNS handler based on X-Amz-Target.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	const prefix = "AmazonSimpleNotificationService."
	if len(target) > len(prefix) {
		target = target[len(prefix):]
	}
	if fn, ok := s.handler.ops[target]; ok {
		fn(w, r)
		return
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "InvalidAction",
		Message:    "Unknown SNS action: " + target,
		HTTPStatus: http.StatusBadRequest,
	})
}

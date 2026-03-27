// Package dynamodb implements the AWS DynamoDB API emulator.
// See docs/services/dynamodb.md for the support matrix.
package dynamodb

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

const serviceName = "dynamodb"

// Service implements router.Service for DynamoDB.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured DynamoDB Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

// Name returns the service identifier.
func (s *Service) Name() string { return serviceName }

// TargetPrefix returns the X-Amz-Target prefix for DynamoDB dispatch.
func (s *Service) TargetPrefix() string { return "DynamoDB_20120810." }

// RegisterRoutes is a no-op — DynamoDB uses POST / which is handled by the
// router's target dispatcher shared with SQS and SNS.
func (s *Service) RegisterRoutes(r chi.Router) {}

// Dispatch routes to the correct DynamoDB handler based on X-Amz-Target.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	// Strip prefix: "DynamoDB_20120810.PutItem" → "PutItem"
	const prefix = "DynamoDB_20120810."
	if len(target) > len(prefix) {
		target = target[len(prefix):]
	}
	if fn, ok := s.handler.ops[target]; ok {
		fn(w, r)
		return
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "UnknownOperationException",
		Message:    "Unknown operation: " + target,
		HTTPStatus: http.StatusBadRequest,
	})
}

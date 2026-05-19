// Package sts provides emulation of AWS Security Token Service (STS).
//
// Uses the AWS Query protocol: form-encoded POST body, XML responses.
// Supported operations:
//   - GetCallerIdentity
//   - GetSessionToken
//   - GetFederationToken
//   - AssumeRole
//   - AssumeRoleWithWebIdentity
package sts

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

const serviceName = "sts"

// Service implements router.Service and router.QueryDispatcher for STS.
type Service struct {
	cfg     *config.Config
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured STS Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		log:     log,
		handler: newHandler(cfg, log, clk, store),
	}
}

// InitBus wires the event bus for informational events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. STS has no path-routed endpoints.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// DispatchQuery satisfies router.QueryDispatcher.
// STS uses the AWS Query protocol: form-encoded POST body with Action field, XML responses.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "STS does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	s.handler.dispatch(w, r)
}

// OwnsAction satisfies router.QueryActionOwner.
func (s *Service) OwnsAction(action string) bool {
	return s.handler.ownsAction(action)
}

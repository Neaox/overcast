// Package ssm provides emulation of AWS Systems Manager Parameter Store.
// See docs/services/ssm.md for the support matrix.
package ssm

import (
	"net/http"
	"strings"

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

const serviceName = "ssm"

// Service implements router.Service and router.TargetDispatcher for SSM.
type Service struct {
	cfg     *config.Config
	store   *Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured SSM Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	s := newStore(store, cfg.Region)
	return &Service{
		cfg:     cfg,
		store:   s,
		log:     log,
		handler: newHandler(cfg, s, log, clk),
	}
}

// InitBus wires the event bus for parameter lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. SSM has no path-routed endpoints.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return "AmazonSSM." }

// Dispatch satisfies router.TargetDispatcher. Routes requests by X-Amz-Target suffix.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "SSM does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		// Preserve AWS JSON 1.1 on the existing handler path until JSON
		// wire-byte goldens cover SSM. CBOR uses typed operations.
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}

	suffix := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AmazonSSM.")
	s.dispatchLegacy(w, r, suffix)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, suffix string) {
	if fn, ok := s.handler.ops[suffix]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

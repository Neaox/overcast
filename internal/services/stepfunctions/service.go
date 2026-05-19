// Package stepfunctions provides emulation of AWS Step Functions.
// See docs/services/stepfunctions.md for the support matrix (when available).
//
// Wire protocol: JSON 1.0 (X-Amz-Target: AWSStepFunctions.*) and RPC v2 CBOR.
// Implements: CreateStateMachine, DescribeStateMachine, ListStateMachines,
// StartExecution, DeleteStateMachine.
package stepfunctions

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

const serviceName = "stepfunctions"

// Service implements router.Service and router.TargetDispatcher for Step Functions.
type Service struct {
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured Step Functions Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	s := newStore(store, cfg.Region)
	return &Service{
		log:     log,
		handler: newHandler(cfg, s, log, clk),
	}
}

// InitBus wires the event bus for state machine lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. Step Functions has no path-routed endpoints.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// TargetPrefix satisfies router.TargetDispatcher.
// The AWS SDK sends "AWSStepFunctions." as the target prefix; "AmazonStates." is an older alias.
func (s *Service) TargetPrefix() string { return "AWSStepFunctions." }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Step Functions does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		// Keep AWS JSON 1.0 on the legacy handler path until JSON wire-byte
		// goldens cover this service. CBOR uses the typed operation path.
		if c.Name() != codec.NameRPCv2CBOR {
			if fn, ok := s.handler.ops[opName]; ok {
				fn(w, r)
				return
			}
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, &protocol.AWSError{
			Code:       "UnknownOperationException",
			Message:    "Unknown Step Functions operation: " + opName,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	target := r.Header.Get("X-Amz-Target")
	suffix := target
	if strings.HasPrefix(target, "AWSStepFunctions.") {
		suffix = target[len("AWSStepFunctions."):]
	}
	if fn, ok := s.handler.ops[suffix]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

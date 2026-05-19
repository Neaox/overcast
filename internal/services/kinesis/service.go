// Package kinesis provides emulation of Amazon Kinesis Data Streams.
// See docs/services/kinesis.md for the support matrix.
//
// Wire protocol: JSON 1.1 (X-Amz-Target: Kinesis_20131202.*) and RPC v2 CBOR.
package kinesis

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

const serviceName = "kinesis"

// targetPrefix is the X-Amz-Target prefix for Kinesis Data Streams.
const targetPrefix = "Kinesis_20131202."

// Service implements router.Service and router.TargetDispatcher for Kinesis.
type Service struct {
	handler *Handler
}

// New returns a configured Kinesis Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	s := newKinesisStore(store, cfg.Region)
	return &Service{
		handler: newHandler(cfg, s, log, clk),
	}
}

// InitBus wires the event bus for stream lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. Kinesis has no path-routed endpoints.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return targetPrefix }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Kinesis does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		// Preserve Kinesis' legacy JSON 1.1 wire shape, including empty
		// bodies for successful void operations. CBOR uses the typed path.
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
			Message:    "Unknown Kinesis operation: " + opName,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	target := r.Header.Get("X-Amz-Target")
	suffix := target
	if strings.HasPrefix(target, targetPrefix) {
		suffix = target[len(targetPrefix):]
	}
	if fn, ok := s.handler.ops[suffix]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

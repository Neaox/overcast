// Package bedrock provides a basic emulation of Amazon Bedrock Runtime.
//
// Implemented operations: InvokeModel, Converse.
//
// Both endpoints return canned responses — no actual model inference.
// Enough for AI stacks that call Bedrock to avoid connection-refused errors.
package bedrock

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

const serviceName = "bedrock"

// Service implements router.Service for Bedrock Runtime.
type Service struct {
	log     *serviceutil.ServiceLogger
	cfg     *config.Config
	typedOp map[string]op.Operation
}

// New returns a configured Bedrock Runtime Service.
func New(cfg *config.Config, _ state.Store, logger *zap.Logger, _ clock.Clock) *Service {
	s := &Service{
		log: serviceutil.NewServiceLogger(logger, serviceName),
		cfg: cfg,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return "Bedrock." }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if codec.Supports(s.SupportedProtocols(), c) {
			if typed, ok := s.typedOp[opName]; ok {
				typed.Invoke(w, r, c)
				return
			}
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// RegisterRoutes registers the REST endpoints for Bedrock Runtime.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/_bedrock", func(r chi.Router) {
		r.Post("/model/{modelId}/invoke", s.invokeModel)
		r.Post("/model/{modelId}/converse", s.converse)
	})
}

// ─── Handlers ─────────────────────────────────────────────────

func (s *Service) invokeModel(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"output": map[string]any{
			"text": "This is a canned response from the Overcast Bedrock emulator.",
		},
		"stopReason": "end_turn",
		"usage": map[string]any{
			"inputTokens":  10,
			"outputTokens": 12,
		},
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (s *Service) converse(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"output": map[string]any{
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"text": "This is a canned response from the Overcast Bedrock emulator."},
				},
			},
		},
		"stopReason": "end_turn",
		"usage": map[string]any{
			"inputTokens":  10,
			"outputTokens": 12,
		},
		"metrics": map[string]any{
			"latencyMs": 1,
		},
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

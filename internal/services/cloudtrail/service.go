// Package cloudtrail provides a metadata-only AWS CloudTrail emulator.
//
// Implemented operations (JSON 1.1):
//   - CreateTrail
//   - DescribeTrails
//   - UpdateTrail
//   - DeleteTrail
//   - ListTrails
//   - GetTrailStatus
//   - StartLogging
//   - StopLogging
//   - LookupEvents (inert: always returns empty Events)
package cloudtrail

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName  = "cloudtrail"
	targetPrefix = "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101."
	nsTrails     = "cloudtrail:trails"
)

type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk any) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   st,
		log:     log,
		handler: newHandler(cfg, st, log),
	}
}

func (s *Service) Name() string { return serviceName }

func (s *Service) TargetPrefix() string { return targetPrefix }

func (s *Service) RegisterRoutes(r chi.Router) {}

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "CloudTrail does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
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

	suffix := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	s.dispatchLegacy(w, r, suffix)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, suffix string) {
	if fn, ok := s.handler.ops[suffix]; ok {
		fn(w, r)
		return
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "InvalidAction",
		Message:    "The action " + suffix + " is not valid for this web service.",
		HTTPStatus: http.StatusBadRequest,
	})
}

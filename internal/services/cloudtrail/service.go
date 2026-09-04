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

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const (
	serviceName  = "cloudtrail"
	targetPrefix = "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101."
	nsTrails     = "cloudtrail:trails"
)

// awsapiService is CloudTrail's key in the generated AWS model corpus.
// serviceutil.MustAWSService validates it at package initialisation, so a
// key the models do not carry fails immediately rather than silently
// answering every unimplemented operation with a 400.
var awsapiService = serviceutil.MustAWSService(serviceName)

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
		// Same split as dispatchLegacy, in CBOR: a name AWS does not model is
		// InvalidAction here too, not a 501 claiming it is merely unemulated.
		serviceutil.WriteUnhandledOperation(w, r, c, awsapiService, opName, errInvalidAction(opName))
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
	// A real CloudTrail operation Overcast has not implemented gets an honest
	// 501; InvalidAction stays for a name AWS does not model (#1645). This
	// path serves the JSON families only — Dispatch sends RPC v2 CBOR to the
	// typed operations — so JSON11 writes the bytes WriteJSONError always did.
	serviceutil.WriteUnhandledOperation(w, r, codec.JSON11, awsapiService, suffix, errInvalidAction(suffix))
}

// errInvalidAction is CloudTrail's answer for a target naming no modeled
// operation.
func errInvalidAction(action string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidAction",
		Message:    "The action " + action + " is not valid for this web service.",
		HTTPStatus: http.StatusBadRequest,
	}
}

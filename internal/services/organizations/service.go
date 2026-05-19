// Package organizations provides stub emulation of AWS Organizations.
//
// Implemented operations (JSON 1.1):
//   - POST / with X-Amz-Target: AWSOrganizationsV20161128.DescribeOrganization
//
// This implementation returns a minimal hardcoded organization stub
// suitable for unblocking CDK bootstrap calls. No actual org structure,
// OUs, or cross-account features are supported.
package organizations

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "organizations"

const targetPrefix = "AWSOrganizationsV20161128."

// Service implements router.Service for Organizations.
type Service struct {
	cfg     *config.Config
	log     *serviceutil.ServiceLogger
	typedOp map[string]op.Operation
}

// New returns a configured Organizations service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk any) *Service {
	s := &Service{
		cfg: cfg,
		log: serviceutil.NewServiceLogger(logger, serviceName),
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) RegisterRoutes(r chi.Router) {}

func (s *Service) TargetPrefix() string { return targetPrefix }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Organizations does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}

	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	s.dispatchLegacy(w, r, op)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "DescribeOrganization":
		s.describeOrganization(w, r)
	default:
		protocol.NotImplementedJSON(w, r)
	}
}

func (s *Service) Stop() error { return nil }

func (s *Service) accountID() string {
	if s.cfg != nil && s.cfg.AccountID != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

func (s *Service) describeOrganization(w http.ResponseWriter, r *http.Request) {
	org := map[string]any{
		"Organization": map[string]any{
			"Id":              "o-overcast",
			"Arn":             "arn:aws:organizations::000000000000:organization/o-overcast",
			"MasterAccountId": s.accountID(),
			"MasterUserEmail": "admin@overcast.local",
			"FeatureSet":      "ALL",
			"AvailablePolicyTypes": []map[string]any{
				{"Type": "SERVICE_CONTROL_POLICY", "Status": "ENABLED"},
			},
		},
	}

	protocol.WriteJSON(w, r, http.StatusOK, org)
}

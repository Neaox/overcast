// Package waf provides emulation of AWS WAF v2 (Web Application Firewall).
// See docs/services/waf.md for the support matrix.
//
// Wire protocol: JSON (X-Amz-Target: AWSWAF_20190729.*)
package waf

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName  = "waf"
	targetPrefix = "AWSWAF_20190729."
	nsWebACLs    = "waf:webacls"
)

// WebACL represents a stored WAFv2 web ACL.
type WebACL struct {
	ID               string            `json:"Id"`
	Name             string            `json:"Name"`
	Scope            string            `json:"Scope"`
	ARN              string            `json:"ARN"`
	LockToken        string            `json:"LockToken"`
	Description      string            `json:"Description,omitempty"`
	DefaultAction    map[string]any    `json:"DefaultAction,omitempty"`
	VisibilityConfig map[string]any    `json:"VisibilityConfig,omitempty"`
	Rules            []any             `json:"Rules,omitempty"`
	Tags             map[string]string `json:"Tags,omitempty"`
	CreatedAt        time.Time         `json:"CreatedAt"`
}

// Service implements router.Service and router.TargetDispatcher for WAF v2.
type Service struct {
	handler *Handler
	log     *serviceutil.ServiceLogger
	store   state.Store
	cfg     *config.Config
	clk     clock.Clock
}

// New returns a configured WAF Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	return &Service{
		log:     serviceutil.NewServiceLogger(logger, serviceName),
		handler: newHandler(cfg, store, clk),
		store:   store,
		cfg:     cfg,
		clk:     clk,
	}
}

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}
func (s *Service) TargetPrefix() string        { return targetPrefix }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "WAF does not support wire protocol " + c.Name() + ".",
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
	protocol.NotImplementedJSON(w, r)
}

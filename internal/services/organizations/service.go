// Package organizations provides Tier 1 ("inert") emulation of AWS
// Organizations over AWS JSON 1.1 / 1.0 and Smithy RPC v2 CBOR.
//
// Implemented operations:
//   - DescribeOrganization — hand-written; a single hardcoded organization,
//     which is what CDK bootstrap asks for.
//   - CreatePolicy / DescribePolicy / UpdatePolicy / DeletePolicy /
//     ListPolicies, plus TagResource / UntagResource /
//     ListTagsForResource — Tier 1 over internal/inert: request metadata is
//     stored and returned faithfully, with modeled errors, ARNs and
//     pagination, and nothing else. Attaching a policy to a target does not
//     happen, and no policy is ever enforced (see
//     docs/plans/inert-tier-rollout.md §0).
//
// Everything else — accounts, organizational units, roots, handshakes,
// delegated administrators — is still Tier 0 and returns a protocol-correct
// 501.
package organizations

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/inert"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const serviceName = "organizations"

const targetPrefix = "AWSOrganizationsV20161128."

// Service implements router.Service for Organizations.
type Service struct {
	cfg      *config.Config
	log      *serviceutil.ServiceLogger
	typedOp  map[string]op.Operation
	bindings []inert.Binding
	policies *inert.Store[policyRecord]
	tags     *inert.Tags
}

// New returns a configured Organizations service.
//
// clk is the emulator's injected clock and is what every stored timestamp
// comes from (§3.5). A nil clock falls back to the real one rather than
// panicking, for callers constructing the service outside the router.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.New()
	}
	s := &Service{
		cfg: cfg,
		log: serviceutil.NewServiceLogger(logger, serviceName),
	}
	// Organizations is a global service: its ARNs carry no region, so
	// inert.Config leaves Region nil and keys are written unscoped (§3.5).
	inertCfg := inert.Config{Store: st, Clock: clk, Logger: s.log}
	s.policies = inert.NewStore[policyRecord](inertCfg, nsPolicies)
	s.tags = inert.NewTags(inertCfg, nsTags)
	s.typedOp = s.typedOps()
	s.bindings = s.inertBindings()
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) RegisterRoutes(r chi.Router) {}

func (s *Service) TargetPrefix() string { return targetPrefix }

// Dispatch resolves the operation and runs it, hand-written table first.
//
// §4.5 is the rule this method implements: a hand-written implementation
// always overrides a Tier 1 one, with no configuration and no exception, so
// dispatchHandWritten is consulted before inert.Lookup ever runs. That is
// what makes adding bindings to a partially implemented service safe — new
// operations arrive without becoming able to reach the implemented ones.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	c, opName := codec.FromContext(r.Context())
	if c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Organizations does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
	} else {
		// No claim-first identification reached us — a bare X-Amz-Target
		// request. JSON 1.1 is Organizations' wire protocol everywhere
		// except RPC v2 CBOR, which always arrives with a codec claim.
		c, opName = codec.JSON11, strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	}

	if s.dispatchHandWritten(w, r, c, opName) {
		return
	}
	if binding, ok := inert.Lookup(s.bindings, opName); ok {
		binding.Invoke.Invoke(w, r, c)
		return
	}
	c.WriteError(w, r, protocol.ErrNotImplemented)
}

// dispatchHandWritten runs opName's hand-written implementation if it has
// one, reporting whether it did.
//
// The JSON families keep reaching the raw handler they always have, so
// DescribeOrganization's wire bytes are untouched by this phase; RPC v2 CBOR
// keeps going through the typed operation, which is the only codec that
// path ever served.
func (s *Service) dispatchHandWritten(w http.ResponseWriter, r *http.Request, c codec.Codec, opName string) bool {
	if !slices.Contains(handWrittenOps, opName) {
		return false
	}
	if c.Name() == codec.NameRPCv2CBOR {
		s.typedOp[opName].Invoke(w, r, c)
		return true
	}
	switch opName {
	case "DescribeOrganization":
		s.describeOrganization(w, r)
	}
	return true
}

func (s *Service) Stop() error { return nil }

func (s *Service) accountID() string {
	if s.cfg != nil && s.cfg.AccountID != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

// masterAccountARN renders the ARN of the emulator's single management
// account, following the same `arn:aws:organizations::{accountId}:...`
// template as policyARN.
func (s *Service) masterAccountARN() string {
	return "arn:aws:organizations::" + s.accountID() + ":account/" + organizationID + "/" + s.accountID()
}

func (s *Service) describeOrganization(w http.ResponseWriter, r *http.Request) {
	org := map[string]any{
		"Organization": map[string]any{
			"Id":                 organizationID,
			"Arn":                "arn:aws:organizations::000000000000:organization/" + organizationID,
			"MasterAccountId":    s.accountID(),
			"MasterAccountArn":   s.masterAccountARN(),
			"MasterAccountEmail": "admin@overcast.local",
			"FeatureSet":         "ALL",
			"AvailablePolicyTypes": []map[string]any{
				{"Type": "SERVICE_CONTROL_POLICY", "Status": "ENABLED"},
			},
		},
	}

	protocol.WriteJSON(w, r, http.StatusOK, org)
}

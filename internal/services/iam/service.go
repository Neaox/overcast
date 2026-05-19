// Package iam provides emulation of AWS Identity and Access Management (IAM).
//
// Uses the AWS Query protocol (form-encoded POST body, XML responses).
// Supported operations:
//   - CreateUser, GetUser, ListUsers, DeleteUser
//   - CreateAccessKey, DeleteAccessKey
//   - PutUserPolicy, GetUserPolicy, DeleteUserPolicy
//   - CreateRole, GetRole, ListRoles, DeleteRole
//   - AttachRolePolicy, DetachRolePolicy, ListAttachedRolePolicies
//   - CreateInstanceProfile, DeleteInstanceProfile, GetInstanceProfile
//   - AddRoleToInstanceProfile, RemoveRoleFromInstanceProfile
//   - CreatePolicy, GetPolicy, ListPolicies, DeletePolicy
//   - CreateGroup, DeleteGroup, AddUserToGroup, RemoveUserFromGroup, ListGroupsForUser
package iam

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "iam"

// Service implements router.Service and router.QueryDispatcher for IAM.
// Uses the AWS Query protocol (form-encoded POST body, XML responses).
type Service struct {
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured IAM Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	iamSt := newIAMStore(store, cfg, clk)
	return &Service{
		log:     log,
		handler: newHandler(cfg, iamSt, log, clk),
	}
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. IAM has no path-routed endpoints.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// OwnsAction satisfies router.QueryActionOwner. IAM owns all actions in handler.ops.
func (s *Service) OwnsAction(action string) bool { return s.handler.ownsAction(action) }

// InitBus wires the event bus for resource lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// DispatchQuery satisfies router.QueryDispatcher.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "IAM does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			if iamMutateActions[opName] {
				middleware.InvalidateIAMEnforceCache()
			}
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	s.handler.dispatch(w, r)
}

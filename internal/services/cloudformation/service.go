// Package cloudformation provides emulation of AWS CloudFormation.
//
// Implemented operations:
//   - CreateStack, UpdateStack, DeleteStack
//   - DescribeStacks, ListStacks
//   - GetTemplate
//   - CreateChangeSet, DescribeChangeSet, ExecuteChangeSet, DeleteChangeSet, ListChangeSets
//   - DescribeStackResources, ListStackResources
//   - DescribeStackEvents
//   - GetTemplateSummary
//   - ValidateTemplate
//
// Stack operations are asynchronous: CreateStack returns CREATE_IN_PROGRESS,
// and a background goroutine provisions resources by dispatching internal HTTP
// requests through the emulator's router. DescribeStacks is the polling mechanism.
package cloudformation

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "cloudformation"

// Service implements router.Service and router.QueryDispatcher for CloudFormation.
// Uses the AWS Query protocol (form-encoded POST, XML responses) and
// identifies itself by individual action names.
type Service struct {
	cfg         *config.Config
	log         *serviceutil.ServiceLogger
	handler     *Handler
	provisioner *provisioner
}

// New returns a configured CloudFormation Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	cfnSt := newCFNStore(store, cfg.Region, clk)
	prov := newProvisioner(cfg, cfnSt, clk, log)
	return &Service{
		cfg:         cfg,
		log:         log,
		handler:     newHandler(cfg, cfnSt, log, clk, prov),
		provisioner: prov,
	}
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. CloudFormation has no path-routed endpoints.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// OwnsVersion satisfies router.QueryVersionOwner. CloudFormation's API version
// (2010-05-15) uniquely identifies requests to this service — the same way
// LocalStack and real AWS route Query-protocol requests before inspecting
// the action name.
func (s *Service) OwnsVersion(version string) bool { return version == awsapi.VersionCloudFormation }

// DispatchQuery satisfies router.QueryDispatcher.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "CloudFormation does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	s.handler.dispatch(w, r)
}

// InitBus wires the event bus for stack lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.provisioner.initBus(bus)
}

// InitRouter sets the HTTP handler for internal resource provisioning dispatch.
// Must be called after the router is fully constructed.
func (s *Service) InitRouter(router http.Handler) {
	s.provisioner.initRouter(router)
}

// Stop drains in-flight provisioning goroutines.
func (s *Service) Stop(ctx context.Context) {
	s.provisioner.stop(ctx)
}

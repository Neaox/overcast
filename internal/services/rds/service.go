// Package rds provides emulation of Amazon RDS.
//
// Implemented: CreateDBInstance, DescribeDBInstances, DeleteDBInstance,
// DescribeDBEngineVersions, StopDBInstance, StartDBInstance, ModifyDBInstance,
// CreateDBSubnetGroup, DeleteDBSubnetGroup, DescribeDBSubnetGroups,
// CreateDBParameterGroup, DeleteDBParameterGroup, DescribeDBParameterGroups,
// DescribeOrderableDBInstanceOptions.
// All other operations return HTTP 501 Not Implemented.
package rds

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "rds"

// Service implements router.Service and router.QueryDispatcher for RDS.
// Uses the AWS Query protocol (form-encoded POST, XML responses) and
// identifies itself by the API version "2014-10-31".
type Service struct {
	handler *Handler
	log     *serviceutil.ServiceLogger
}

// New returns a configured RDS Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		handler: newHandler(cfg, newRDSStore(store, cfg.Region), log, clk),
		log:     log,
	}
}

// InitBus wires the event bus for RDS lifecycle events and subscribes to
// Docker container events so instance status stays in sync with container state.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
	bus.Subscribe(events.DockerContainerDied, s.handler.handleContainerEvent)
	bus.Subscribe(events.DockerContainerStopped, s.handler.handleContainerEvent)
	bus.Subscribe(events.DockerContainerStarted, s.handler.handleContainerStarted)
}

// SetDocker wires the Docker client for RDS container management and starts
// the DockGC background remove loop.
func (s *Service) SetDocker(dc *docker.Client) {
	s.handler.docker = dc
	s.handler.puller = docker.NewImagePuller(dc)
	s.handler.gc = docker.NewGC(dc, s.log.ZapLogger(), s.handler.cfg.RDSKeepContainers)
	s.handler.gc.StartRemoveLoop(context.Background())
	s.handler.gc.Sweep(serviceName) // clean up orphaned containers from previous runs
	s.handler.dockerReady.Store(true)
}

// SetVPCResolver wires the EC2 VPC resolver for DB subnet group launches.
func (s *Service) SetVPCResolver(r VPCNetworkResolver) {
	s.handler.vpcResolver = r
}

// ReconcileContainers satisfies router.ContainerReconciler. It is called once
// after Docker becomes available at startup and syncs stored RDS instance
// statuses against the actual container state.
func (s *Service) ReconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	s.handler.reconcileContainers(ctx, containers)
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. Registers emulator-only endpoints.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Get("/_rds/instances/{instanceId}/logs", s.handler.GetInstanceLogs)
}

// OwnsVersion satisfies router.QueryVersionOwner.
func (s *Service) OwnsVersion(version string) bool { return version == awsapi.VersionRDS }

// OwnsAction satisfies router.QueryActionOwner.
func (s *Service) OwnsAction(action string) bool { return s.handler.ownsAction(action) }

// Stop cancels pending lifecycle transitions and cleans up Docker containers
// via the GC. The GC does a Docker-level sweep so even orphaned containers
// (whose store record was already deleted) are caught and removed.
func (s *Service) Stop(ctx context.Context) {
	s.handler.scheduler.Stop(ctx)
	s.handler.dockerWg.Wait()

	if s.handler.gc != nil {
		s.handler.gc.DrainAndSweep(ctx, serviceName)
	}
}

// DispatchQuery satisfies router.QueryDispatcher; routes to the correct handler.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "RDS does not support wire protocol " + c.Name() + ".",
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

// Package ecs provides emulation of Amazon Elastic Container Service (ECS).
//
// Supported: CreateCluster, DescribeClusters, ListClusters, DeleteCluster,
//
//	RegisterTaskDefinition, DescribeTaskDefinition, ListTaskDefinitions,
//	DeregisterTaskDefinition, RunTask, StopTask, DescribeTasks, ListTasks,
//	CreateService, UpdateService, DeleteService, DescribeServices, ListServices
//
// Unsupported: See docs/services/ecs.md (stub operations return 501).
package ecs

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "ecs"

// targetPrefix is the X-Amz-Target prefix for ECS.
const targetPrefix = "AmazonEC2ContainerServiceV20141113."

// Service implements router.Service and router.TargetDispatcher for ECS.
type Service struct {
	handler *Handler
	cfg     *config.Config
	log     *serviceutil.ServiceLogger
}

// New returns a configured ECS Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	s := newECSStore(store, cfg.Region)
	svc := &Service{
		handler: newHandler(cfg, s, log, clk),
		cfg:     cfg,
		log:     log,
	}
	return svc
}

// SetDocker wires a Docker client into the ECS handler, enabling container
// execution for RunTask/StopTask. Called after Docker availability is confirmed.
func (s *Service) SetDocker(dc *docker.Client) {
	s.handler.docker = dc
	s.handler.puller = docker.NewImagePuller(dc)
	s.handler.gc = docker.NewGC(dc, s.log.ZapLogger(), s.handler.cfg.ECSKeepContainers)
	s.handler.gc.StartRemoveLoop(context.Background())
	s.handler.gc.Sweep(serviceName) // clean up orphaned containers from previous runs
	s.handler.dockerReady.Store(true)
}

// SetVPCResolver wires the EC2 VPC resolver for awsvpc task placement.
func (s *Service) SetVPCResolver(r VPCNetworkResolver) {
	s.handler.vpcResolver = r
}

// InitBus wires the event bus for ECS lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
	bus.Subscribe(events.DockerContainerDied, s.handler.handleContainerDied)
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. Registers emulator-only endpoints.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Get("/_ecs/tasks/{taskArn}/logs/{container}", s.handler.GetTaskContainerLogs)
	r.Get("/_ecs/clusters/{cluster}/tasks", s.handler.ListClusterTasks)
}

// Stop cancels pending lifecycle transitions and cleans up Docker containers
// via the GC. The GC does a Docker-level sweep so even orphaned containers
// (whose task record was already deleted) are caught and removed.
func (s *Service) Stop(ctx context.Context) {
	s.handler.scheduler.Stop(ctx)

	if s.handler.gc != nil {
		s.handler.gc.DrainAndSweep(ctx, serviceName)
	}
}

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return targetPrefix }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "ECS does not support wire protocol " + c.Name() + ".",
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

	suffix := r.Header.Get("X-Amz-Target")[len(targetPrefix):]
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

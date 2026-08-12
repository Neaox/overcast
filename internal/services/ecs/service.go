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
	s.handler.puller = docker.NewImagePuller(dc).WithResolver(s.handler.images)
	s.handler.gc = docker.NewGC(dc, s.log.ZapLogger(), s.handler.cfg.ECSKeepContainers, s.handler.instances.Resolve)
	s.handler.gc.StartRemoveLoop(context.Background())
	s.handler.gc.Sweep(serviceName) // clean up orphaned containers from previous runs
	s.handler.dockerReady.Store(true)

	// Volumes second, and in the background: task-scoped volumes whose task did
	// not survive the last run are only removable once the container sweep
	// above has released them, and neither should hold up wiring Docker.
	go s.handler.sweepOrphanedTaskVolumes(context.Background())
}

// SetImageResolver wires the registry resolver used when a container image
// names a registry Overcast serves rather than a public one — an ECR image
// built and pushed by CDK, whose reference points at real AWS. Implemented by
// the ECR service.
//
// Wire this before SetDocker: the puller takes the resolver when it is built.
// The router does, and the Docker probe that calls SetDocker starts after.
func (s *Service) SetImageResolver(r docker.ImageResolver) {
	s.handler.images = r
}

// SetVPCResolver wires the EC2 VPC resolver for awsvpc task placement.
func (s *Service) SetVPCResolver(r VPCNetworkResolver) {
	s.handler.vpcResolver = r
}

// SetEFSResolver wires the EFS volume resolver so task containers can mount
// efsVolumeConfiguration-backed volumes in EFS live mode.
func (s *Service) SetEFSResolver(r EFSVolumeResolver) {
	s.handler.efsResolver = r
}

// SetSecretResolvers wires Secrets Manager and SSM so a container definition's
// `secrets` are fetched and injected as environment variables at task start.
func (s *Service) SetSecretResolvers(sm SecretsManagerResolver, ssm ParameterResolver) {
	s.handler.secrets = sm
	s.handler.parameters = ssm
}

// SetTargetRegistrar wires elbv2 so a service's tasks are registered with the
// target groups named in its loadBalancers.
func (s *Service) SetTargetRegistrar(r TargetRegistrar) {
	s.handler.targets = r
}

// InitLogWriter wires CloudWatch Logs so task containers using the awslogs log
// driver have their output shipped there, as they do on ECS. Applies to every
// task regardless of launch type — the driver is a property of the container
// definition, not of Fargate or EC2.
func (s *Service) InitLogWriter(w events.LogWriter) {
	s.handler.logWriter = w
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
	r.Get("/_overcast/ecs/tasks/{taskArn}/logs/{container}", s.handler.GetTaskContainerLogs)
	r.Get("/_overcast/ecs/clusters/{cluster}/tasks", s.handler.ListClusterTasks)
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

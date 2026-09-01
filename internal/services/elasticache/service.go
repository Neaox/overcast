// Package elasticache provides emulation of Amazon ElastiCache.
//
// Implemented: CreateCacheCluster, DescribeCacheClusters, DeleteCacheCluster,
// CreateServerlessCache, DescribeServerlessCaches, DeleteServerlessCache,
// ModifyServerlessCache,
// ModifyCacheCluster, CreateReplicationGroup, DescribeReplicationGroups,
// DeleteReplicationGroup, ModifyReplicationGroup,
// CreateCacheSubnetGroup, DescribeCacheSubnetGroups, DeleteCacheSubnetGroup,
// CreateCacheParameterGroup, DescribeCacheParameterGroups, DeleteCacheParameterGroup,
// AddTagsToResource, ListTagsForResource, RemoveTagsFromResource.
//
// On CreateCacheCluster, a real Redis container is started (same pattern as RDS).
// The cluster reaches "available" once the engine answers its own protocol, and
// a terminal failure status carrying the reason if it never does — see
// readiness.go.
//
// All other operations return HTTP 501 Not Implemented.
package elasticache

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const serviceName = "elasticache"

// Service implements router.Service and router.QueryDispatcher for ElastiCache.
// Uses the AWS Query protocol (form-encoded POST, XML responses) and
// identifies itself by the API version "2015-02-02".
type Service struct {
	handler *Handler
	log     *serviceutil.ServiceLogger
}

// New returns a configured ElastiCache Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		handler: newHandler(cfg, newCacheStore(store, cfg.Region), log, clk),
		log:     log,
	}
}

// InitBus wires the event bus for ElastiCache lifecycle events and subscribes
// to Docker container events so cluster status stays in sync with container state.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
	bus.Subscribe(events.DockerContainerDied, s.handler.handleContainerEvent)
	bus.Subscribe(events.DockerContainerStopped, s.handler.handleContainerEvent)
	bus.Subscribe(events.DockerContainerStarted, s.handler.handleContainerStarted)
}

// SetVPCResolver wires the EC2 VPC resolver, so a cache whose subnet group
// names a VPC is placed on that VPC's network rather than the default plane.
func (s *Service) SetVPCResolver(r VPCNetworkResolver) {
	s.handler.vpcResolver = r
}

// SetDocker wires the Docker client for ElastiCache container management and
// starts the DockerGC background remove loop.
func (s *Service) SetDocker(dc *docker.Client) {
	s.handler.docker = dc
	s.handler.puller = docker.NewImagePuller(dc)
	s.handler.gc = docker.NewGC(dc, s.log.ZapLogger(), s.handler.cfg.ElastiCacheKeepContainers, s.handler.instances.Resolve)
	s.handler.gc.StartRemoveLoop(s.handler.bgCtx)
	s.handler.gc.Sweep(serviceName) // clean up orphaned containers from previous runs
	s.handler.dockerReady.Store(true)
}

// ReconcileContainers satisfies router.ContainerReconciler. Startup and
// reconnect snapshots repair stored cluster state and replace missing nodes.
func (s *Service) ReconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	s.handler.reconcileContainers(ctx, containers)
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. ElastiCache has no emulator-only
// REST endpoints; all operations go through DispatchQuery.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// OwnsVersion satisfies router.QueryVersionOwner.
func (s *Service) OwnsVersion(version string) bool { return version == awsapi.VersionElastiCache }

// Stop cancels pending lifecycle transitions, waits for in-flight Docker goroutines,
// then cleans up all containers via the GC.
func (s *Service) Stop(ctx context.Context) {
	s.handler.scheduler.Stop(ctx)
	s.handler.dockerLifecycle.Lock()
	s.handler.dockerStopping = true
	s.handler.dockerReady.Store(false)
	s.handler.dockerLifecycle.Unlock()

	// Cancel bgCtx before waiting, not after. It is what the container-start
	// goroutines run on as well as what the GC remove loop exits on, so
	// cancelling it after the wait — as this did — could never shorten one: a
	// start against a daemon that does not answer was ended only by the Docker
	// client's own 30s response-header timeout, spending that out of a budget
	// this shares with every other service's Stop.
	s.handler.bgCancel()

	// Then wait for those goroutines, so none is still talking to the daemon
	// when the GC sweeps below. One cancelled part-way through may leave a
	// container it never recorded; DrainAndSweep's final label-based sweep is
	// what reclaims it, as it does for any other orphan.
	done := make(chan struct{})
	go func() {
		s.handler.dockerWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}

	if s.handler.gc != nil {
		s.handler.gc.DrainAndSweep(ctx, serviceName)
	}
}

// DispatchQuery satisfies router.QueryDispatcher; routes to the correct handler.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !serviceutil.AllowProtocolDrift(s.handler.cfg, s.log, opName, c, s.SupportedProtocols()) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "ElastiCache does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		// No typed impl for this op — fall through to legacy dispatch below.
	}
	if err := r.ParseForm(); err != nil {
		protocol.NotImplementedQueryXML(w, r)
		return
	}
	action := r.FormValue("Action")
	switch action {
	// ── Cache cluster ─────────────────────────────────────────────────────────
	case "CreateCacheCluster":
		s.handler.CreateCacheCluster(w, r)
	case "DescribeCacheClusters":
		s.handler.DescribeCacheClusters(w, r)
	case "DeleteCacheCluster":
		s.handler.DeleteCacheCluster(w, r)
	case "CreateServerlessCache":
		s.handler.CreateServerlessCache(w, r)
	case "DescribeServerlessCaches":
		s.handler.DescribeServerlessCaches(w, r)
	case "DeleteServerlessCache":
		s.handler.DeleteServerlessCache(w, r)
	case "ModifyServerlessCache":
		s.handler.ModifyServerlessCache(w, r)

	// ── Replication groups ────────────────────────────────────────────────────
	case "CreateReplicationGroup":
		s.handler.CreateReplicationGroup(w, r)
	case "DescribeReplicationGroups":
		s.handler.DescribeReplicationGroups(w, r)
	case "DeleteReplicationGroup":
		s.handler.DeleteReplicationGroup(w, r)

	// ── Subnet groups ─────────────────────────────────────────────────────────
	case "CreateCacheSubnetGroup":
		s.handler.CreateCacheSubnetGroup(w, r)
	case "DescribeCacheSubnetGroups":
		s.handler.DescribeCacheSubnetGroups(w, r)
	case "DeleteCacheSubnetGroup":
		s.handler.DeleteCacheSubnetGroup(w, r)

	// ── Tagging ───────────────────────────────────────────────────────────────
	case "AddTagsToResource":
		s.handler.AddTagsToResource(w, r)
	case "ListTagsForResource":
		s.handler.ListTagsForResource(w, r)
	case "RemoveTagsFromResource":
		s.handler.RemoveTagsFromResource(w, r)

	// ── Parameter groups ──────────────────────────────────────────────────────
	case "CreateCacheParameterGroup":
		s.handler.CreateCacheParameterGroup(w, r)
	case "DescribeCacheParameterGroups":
		s.handler.DescribeCacheParameterGroups(w, r)
	case "DeleteCacheParameterGroup":
		s.handler.DeleteCacheParameterGroup(w, r)
	case "DescribeCacheParameters":
		s.handler.DescribeCacheParameters(w, r)

	// ── Modify ────────────────────────────────────────────────────────────────
	case "ModifyCacheCluster":
		s.handler.ModifyCacheCluster(w, r)
	case "ModifyReplicationGroup":
		s.handler.ModifyReplicationGroup(w, r)

	// ── Stubs ─────────────────────────────────────────────────────────────────
	case "RebootCacheCluster":
		s.handler.RebootCacheCluster(w, r)
	case "DescribeCacheEngineVersions":
		s.handler.DescribeCacheEngineVersions(w, r)

	default:
		protocol.NotImplementedQueryXML(w, r)
	}
}

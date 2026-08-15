// Package msk provides emulation of Amazon MSK (Managed Streaming for Kafka).
//
// Implemented operations: CreateCluster, DescribeCluster, ListClusters,
// DeleteCluster, GetBootstrapBrokers, CreateClusterV2, DescribeClusterV2,
// ListClustersV2, CreateConfiguration, DescribeConfiguration,
// ListConfigurations, DeleteConfiguration, UpdateClusterConfiguration,
// ListKafkaVersions, TagResource, UntagResource, ListTagsForResource.
//
// MSK speaks restJson1 and nothing else — the pinned model gives every kafka
// operation an @http binding and no target prefix — so the whole surface is
// reached by method and path, with no X-Amz-Target dispatch.
//
// On CreateCluster, a real Redpanda container is started (same pattern as ElastiCache).
// The cluster reaches "ACTIVE" state once the TCP health check succeeds.
package msk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "msk"

// ── Models ────────────────────────────────────────────────────────────────────

// Cluster is the stored record for an MSK cluster. It is the emulator's own
// shape, not a modeled one — clusterV1View and clusterV2View render it into the
// two API shapes. Notably KafkaVersion has no top-level home in either: AWS
// carries it in currentBrokerSoftwareInfo.
//
// A cluster's tags are deliberately not here. They live in the nsTags namespace
// the tag operations read and write, so that a tag set at create time and one
// set by TagResource are the same tag.
type Cluster struct {
	ClusterArn          string              `json:"clusterArn"`
	ClusterName         string              `json:"clusterName"`
	ClusterType         string              `json:"clusterType,omitempty"`
	State               string              `json:"state"`
	CreationTime        time.Time           `json:"creationTime"`
	CurrentVersion      string              `json:"currentVersion"`
	BrokerNodeGroupInfo BrokerNodeGroupInfo `json:"brokerNodeGroupInfo"`
	NumberOfBrokerNodes int                 `json:"numberOfBrokerNodes"`
	KafkaVersion        string              `json:"kafkaVersion"`
	// Internal — not in API responses.
	DockerContainerID string `json:"_dockerContainerID,omitempty"`
	HostPort          int    `json:"_hostPort,omitempty"`
}

// BrokerNodeGroupInfo describes broker node configuration.
type BrokerNodeGroupInfo struct {
	InstanceType         string   `json:"instanceType,omitempty"`
	ClientSubnets        []string `json:"clientSubnets,omitempty"`
	SecurityGroups       []string `json:"securityGroups,omitempty"`
	BrokerAZDistribution string   `json:"brokerAZDistribution,omitempty"`
}

// ClusterConfiguration represents an MSK configuration.
type ClusterConfiguration struct {
	Arn            string    `json:"arn"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	KafkaVersions  []string  `json:"kafkaVersions,omitempty"`
	CreationTime   time.Time `json:"creationTime"`
	LatestRevision Revision  `json:"latestRevision"`
	State          string    `json:"state"`
}

// Revision represents a configuration revision.
type Revision struct {
	CreationTime time.Time `json:"creationTime"`
	Revision     int64     `json:"revision"`
}

// ── Store ─────────────────────────────────────────────────────────────────────

const (
	nsClusters = "msk:clusters"
	// nsInstance holds this instance's sweep-domain identity, stamped into
	// docker.LabelInstance on every broker container. See
	// serviceutil.InstanceDomain.
	nsInstance       = "msk:instance"
	nsConfigurations = "msk:configurations"
	nsTags           = "msk:tags"
	nsPorts          = "msk:ports"
)

type mskStore struct {
	mu            sync.Mutex
	store         state.Store
	defaultRegion string
}

func newMSKStore(store state.Store, defaultRegion string) *mskStore {
	return &mskStore{store: store, defaultRegion: defaultRegion}
}

func (s *mskStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

func (s *mskStore) putCluster(ctx context.Context, c *Cluster) *protocol.AWSError {
	raw, err := json.Marshal(c)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), c.ClusterArn), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *mskStore) getCluster(ctx context.Context, clusterArn string) (*Cluster, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), clusterArn))
	if err != nil || !ok {
		return nil, errClusterNotFound(clusterArn)
	}
	var c Cluster
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &c, nil
}

func (s *mskStore) listClusters(ctx context.Context) ([]*Cluster, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	clusters := make([]*Cluster, 0, len(pairs))
	for _, p := range pairs {
		var c Cluster
		if err := json.Unmarshal([]byte(p.Value), &c); err != nil {
			continue
		}
		clusters = append(clusters, &c)
	}
	return clusters, nil
}

func (s *mskStore) deleteCluster(ctx context.Context, clusterArn string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), clusterArn)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// tags returns the tag store every MSK tag operation goes through. Tags are
// keyed by the bare resource ARN, which already names its region, and the
// namespace is shared by CreateCluster, CreateClusterV2, TagResource,
// UntagResource and ListTagsForResource — one place, so a tag written by any of
// them is read by all of them.
func (s *mskStore) tags() *serviceutil.NSStore {
	return &serviceutil.NSStore{Store: s.store, NS: nsTags}
}

// deleteTags drops a resource's whole tag blob, which is what a resource's
// deletion does to its tags on AWS.
func (s *mskStore) deleteTags(ctx context.Context, resourceArn string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsTags, resourceArn); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *mskStore) putConfiguration(ctx context.Context, c *ClusterConfiguration) *protocol.AWSError {
	raw, err := json.Marshal(c)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsConfigurations, serviceutil.RegionKey(s.region(ctx), c.Arn), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *mskStore) getConfiguration(ctx context.Context, arn string) (*ClusterConfiguration, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsConfigurations, serviceutil.RegionKey(s.region(ctx), arn))
	if err != nil || !ok {
		return nil, errConfigurationNotFound(arn)
	}
	var c ClusterConfiguration
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &c, nil
}

func (s *mskStore) listConfigurations(ctx context.Context) ([]*ClusterConfiguration, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsConfigurations, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	configs := make([]*ClusterConfiguration, 0, len(pairs))
	for _, p := range pairs {
		var c ClusterConfiguration
		if err := json.Unmarshal([]byte(p.Value), &c); err != nil {
			continue
		}
		configs = append(configs, &c)
	}
	return configs, nil
}

func (s *mskStore) deleteConfiguration(ctx context.Context, arn string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsConfigurations, serviceutil.RegionKey(s.region(ctx), arn)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *mskStore) allocatePort(ctx context.Context, clusterARN string, portBase int) (int, *protocol.AWSError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pairs, err := s.store.Scan(ctx, nsPorts, "")
	if err != nil {
		return 0, protocol.Wrap(protocol.ErrInternalError, err)
	}
	used := make(map[int]bool, len(pairs))
	for _, p := range pairs {
		if port, parseErr := strconv.Atoi(p.Key); parseErr == nil {
			used[port] = true
		}
	}
	for port := portBase; port < portBase+1000; port++ {
		if !used[port] {
			if err := s.store.Set(ctx, nsPorts, strconv.Itoa(port), clusterARN); err != nil {
				return 0, protocol.Wrap(protocol.ErrInternalError, err)
			}
			return port, nil
		}
	}
	return 0, &protocol.AWSError{
		Code:       "InternalFailure",
		Message:    "no free port available for MSK cluster",
		HTTPStatus: http.StatusInternalServerError,
	}
}

func (s *mskStore) releasePort(ctx context.Context, port int) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsPorts, strconv.Itoa(port)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *mskStore) allocatePortFixed(ctx context.Context, clusterARN string, port int) *protocol.AWSError {
	if err := s.store.Set(ctx, nsPorts, strconv.Itoa(port), clusterARN); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ── Error helpers ─────────────────────────────────────────────────────────────

func errClusterNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NotFoundException",
		Message:    fmt.Sprintf("Cluster %s not found.", arn),
		HTTPStatus: http.StatusNotFound,
	}
}

func errConfigurationNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NotFoundException",
		Message:    fmt.Sprintf("Configuration %s not found.", arn),
		HTTPStatus: http.StatusNotFound,
	}
}

// ── Handler ───────────────────────────────────────────────────────────────────

// Handler handles MSK REST JSON requests.
type Handler struct {
	cfg             *config.Config
	store           *mskStore
	log             *serviceutil.ServiceLogger
	clk             clock.Clock
	bus             *events.Bus
	scheduler       *lifecycle.Scheduler
	docker          *docker.Client
	dockerReady     atomic.Bool
	puller          *docker.ImagePuller
	gc              *docker.GC
	dockerWg        sync.WaitGroup
	dockerLifecycle sync.Mutex // closes the start/recovery Add/Wait boundary at shutdown
	dockerStopping  bool
	dockerStarts    sync.Map // cluster ARN -> in-flight start or recovery
	vpcResolver     VPCNetworkResolver
	// instances scopes container sweeps to the containers this instance
	// created, so one Overcast's shutdown does not take down another's running
	// brokers. See docker.LabelInstance.
	instances *serviceutil.InstanceDomain

	// One writer at a time per record — see locks.go.
	clusterLocks serviceutil.RecordLocks
	// One create at a time per region-scoped resource name. Separate from
	// clusterLocks because it guards a name rather than a record: two creates
	// racing over one name mint two different ARNs, so a per-ARN lock never
	// makes them meet. See claimClusterName.
	nameLocks serviceutil.RecordLocks
}

// VPCNetworkResolver resolves a cluster's client subnets back to EC2 VPC
// network state. Implemented by the EC2 service; nil when EC2 is not enabled,
// which leaves every cluster on the default data plane.
type VPCNetworkResolver interface {
	dataplane.VPCResolver
	// VpcIDForSubnet returns the VPC ID that owns the given subnet.
	VpcIDForSubnet(ctx context.Context, subnetID string) string
}

func newHandler(cfg *config.Config, store *mskStore, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	return &Handler{
		cfg:       cfg,
		store:     store,
		log:       log,
		clk:       clk,
		scheduler: lifecycle.NewScheduler(clk),
		instances: serviceutil.NewInstanceDomain(store.store, nsInstance),
	}
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service implements router.Service for MSK.
type Service struct {
	handler *Handler
	log     *serviceutil.ServiceLogger
}

// New returns a configured MSK Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		handler: newHandler(cfg, newMSKStore(store, cfg.Region), log, clk),
		log:     log,
	}
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// Path prefixes MSK owns, in the shape the model binds them. The v2 cluster API
// lives under /api/v2, not under /v2 — every v2 operation's URI in the pinned
// kafka model carries that prefix.
const (
	clustersPath       = "/v1/clusters"
	configurationsPath = "/v1/configurations"
	kafkaVersionsPath  = "/v1/kafka-versions"
	clustersV2Path     = "/api/v2/clusters"
)

// RegisterRoutes satisfies router.Service.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Post(clustersPath, s.handler.createCluster)
	r.Get(clustersPath, s.handler.listClusters)
	r.Post(configurationsPath, s.handler.createConfiguration)
	r.Get(configurationsPath, s.handler.listConfigurations)
	r.Get(kafkaVersionsPath, s.handler.listKafkaVersions)

	// The v1 cluster subtree is served by three dispatch handlers that pick the
	// operation out of the path themselves, because the ARN reaches Overcast in
	// two spellings: percent-encoded into one segment, as every AWS client sends
	// it, and with literal slashes, as CloudFormation's provisioner and this
	// repo's own callers send it. Only the first has the shape the modeled
	// `{ClusterArn}` label binds, so a label alone would not serve the second.
	// internal/router's weaklyServedBindings records what the wildcard costs.
	//
	// What the dispatchers must not do is treat the whole tail as the ARN.
	// Fourteen kafka operations hang a sub-resource off the cluster ARN and MSK
	// emulates two of them, so absorbing "…/nodes" into the ARN answered
	// ListNodes with "Cluster arn:…:cluster/probe/<uuid>/nodes not found" — a
	// plausible reply to a question nobody asked. modeledOperation asks the
	// pinned model where the ARN ends instead; see dispatch.go.
	r.Get(clustersPath+"/*", s.handler.clusterGetDispatch)
	r.Delete(clustersPath+"/*", s.handler.clusterDeleteDispatch)
	r.Put(clustersPath+"/*", s.handler.clusterPutDispatch)

	// V2 cluster API (provisioned + serverless). {clusterArn} is a non-greedy
	// httpLabel, so the SDKs send the ARN percent-encoded into a single
	// segment — see arnFromPath. A label rather than a subtree wildcard is what
	// stops the v2 operations Overcast does not implement — ListClusterOperationsV2
	// at …/{ClusterArn}/operations — reaching describeClusterV2, which strips
	// nothing and would answer them "cluster not found".
	//
	// The label does stop that, but on its own it did not produce the 501 it was
	// assumed to leave behind. PathPrefixes cannot supply one — see its own
	// comment — so the request went to the router's REST fallback, which
	// classified the *decoded* path: the ARN's %2F-encoded slashes reappeared as
	// path segments, no binding matched, and S3's wildcard answered a signed MSK
	// call with <Error><Code>NoSuchKey</Code>…</Error>. claimModeledPath fixes
	// the classification; clusterV2GetDispatch below is what makes the answer
	// MSK's for an unsigned caller too, since the fallback is by design only
	// reached by traffic that positively addresses a non-S3 service.
	//
	// The label route stays, and the wildcard sits beside it rather than
	// replacing it: chi tries a parameter node before a catch-all, so
	// DescribeClusterV2 is still proven served at the binding AWS models.
	r.Post(clustersV2Path, s.handler.createClusterV2)
	r.Get(clustersV2Path, s.handler.listClustersV2)
	r.Get(clustersV2Path+"/{clusterArn}", s.handler.describeClusterV2)
	r.Get(clustersV2Path+"/*", s.handler.clusterV2GetDispatch)

	// Configuration routes with ARN-in-path, on the same two-spelling wildcard
	// as the clusters, and with the same sub-resources beneath the ARN:
	// ListConfigurationRevisions and DescribeConfigurationRevision.
	r.Get(configurationsPath+"/*", s.handler.configurationGetDispatch)
	r.Delete(configurationsPath+"/*", s.handler.configurationDeleteDispatch)

	// NOTE: /v1/tags routes are NOT registered here — see TagsRouter.
}

// PathPrefixes satisfies router.PathPrefixService: in a subset-registered
// router these answer 501 rather than falling into S3's wildcard, where
// /api/v2/clusters reads as the object v2/clusters in a bucket named api.
// /v1/tags is deliberately absent — it is shared with the other taggable
// services and mounted by the router, not by RegisterRoutes.
//
// "In a subset-registered router" is the whole of it, and it is worth stating
// plainly because the opposite was assumed here: the router consults this list
// only for a service config.TestOnlyServiceSubset has *excluded*, to keep an
// absent service's paths out of S3's wildcard. When MSK is enabled — every real
// run — these prefixes register nothing at all, so an MSK path RegisterRoutes
// does not claim is not protected by anything in this file. Its 501 comes from
// the router's REST fallback recognising the modeled binding, and nowhere else.
func (s *Service) PathPrefixes() []string {
	return []string{clustersPath, configurationsPath, kafkaVersionsPath, clustersV2Path}
}

// TagsRouter returns a chi.Router for the MSK tagging routes that live under
// /v1/tags. This is mounted by the main router alongside other taggable
// services' tag routers and dispatched by the resourceArn's service segment,
// since /v1/tags/{resourceArn} is shared across services (e.g. AppSync) that
// each own tagging for their own ARNs.
func (s *Service) TagsRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/*", s.handler.listTagsForResource)
	r.Post("/*", s.handler.tagResource)
	r.Delete("/*", s.handler.untagResource)
	return r
}

// InitBus wires the event bus for MSK lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
	bus.Subscribe(events.DockerContainerDied, s.handler.handleContainerEvent)
	bus.Subscribe(events.DockerContainerStopped, s.handler.handleContainerEvent)
	bus.Subscribe(events.DockerContainerStarted, s.handler.handleContainerStarted)
}

// SetVPCResolver wires the EC2 VPC resolver, so a cluster whose client subnets
// name a VPC has its brokers placed on that VPC's network rather than the
// default plane.
//
// On AWS every provisioned cluster names client subnets, so this is the normal
// case and not a corner one.
func (s *Service) SetVPCResolver(r VPCNetworkResolver) {
	s.handler.vpcResolver = r
}

// SetDocker wires the Docker client for MSK container management and starts
// the DockerGC background remove loop.
func (s *Service) SetDocker(dc *docker.Client) {
	s.handler.docker = dc
	s.handler.puller = docker.NewImagePuller(dc)
	s.handler.gc = docker.NewGC(dc, s.log.ZapLogger(), s.handler.cfg.MSKKeepContainers, s.handler.instances.Resolve)
	s.handler.gc.StartRemoveLoop(context.Background())
	s.handler.gc.Sweep(serviceName) // clean up orphaned containers from previous runs
	s.handler.dockerReady.Store(true)
}

// ReconcileContainers satisfies router.ContainerReconciler.
func (s *Service) ReconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	s.handler.reconcileContainers(ctx, containers)
}

// Stop cancels pending lifecycle transitions, waits for in-flight Docker goroutines,
// then cleans up all containers via the GC.
func (s *Service) Stop(ctx context.Context) {
	s.handler.scheduler.Stop(ctx)
	s.handler.dockerLifecycle.Lock()
	s.handler.dockerStopping = true
	s.handler.dockerReady.Store(false)
	s.handler.dockerLifecycle.Unlock()

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

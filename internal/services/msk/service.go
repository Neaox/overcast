// Package msk provides emulation of Amazon MSK (Managed Streaming for Kafka).
//
// Implemented operations: CreateCluster, DescribeCluster, ListClusters,
// DeleteCluster, GetBootstrapBrokers, CreateConfiguration, DescribeConfiguration,
// ListConfigurations, DeleteConfiguration, ListKafkaVersions,
// TagResource, UntagResource, ListTagsForResource.
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
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "msk"

// ── Models ────────────────────────────────────────────────────────────────────

// Cluster represents an MSK cluster.
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
	Tags                map[string]string   `json:"tags,omitempty"`
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
	nsClusters       = "msk:clusters"
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

func (s *mskStore) getTags(ctx context.Context, arn string) (map[string]string, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsTags, arn)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !ok {
		return map[string]string{}, nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return tags, nil
}

func (s *mskStore) setTags(ctx context.Context, arn string, tags map[string]string) *protocol.AWSError {
	raw, err := json.Marshal(tags)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsTags, arn, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// allocatePort finds and reserves the first free port in [portBase, portBase+1000).
// Protected by a mutex to prevent concurrent callers from claiming the same port.
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
	cfg         *config.Config
	store       *mskStore
	log         *serviceutil.ServiceLogger
	clk         clock.Clock
	bus         *events.Bus
	scheduler   *lifecycle.Scheduler
	docker      *docker.Client
	dockerReady atomic.Bool
	puller      *docker.ImagePuller
	gc          *docker.GC
	dockerWg    sync.WaitGroup
}

func newHandler(cfg *config.Config, store *mskStore, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	return &Handler{
		cfg:       cfg,
		store:     store,
		log:       log,
		clk:       clk,
		scheduler: lifecycle.NewScheduler(clk),
	}
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service implements router.Service for MSK.
type Service struct {
	handler *Handler
	log     *serviceutil.ServiceLogger
	typedOp map[string]op.Operation
}

// New returns a configured MSK Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	h := newHandler(cfg, newMSKStore(store, cfg.Region), log, clk)
	s := &Service{
		handler: h,
		log:     log,
	}
	s.typedOp = s.typedOps()
	return s
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return "Kafka." }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if codec.Supports(s.SupportedProtocols(), c) {
			if typed, ok := s.typedOp[opName]; ok {
				typed.Invoke(w, r, c)
				return
			}
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// RegisterRoutes satisfies router.Service.
//
// ARNs in path parameters contain forward slashes. We use chi wildcard catch-all
// routes (/*) and dispatch to specific handlers based on the path suffix.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Post("/v1/clusters", s.handler.createCluster)
	r.Get("/v1/clusters", s.handler.listClusters)
	r.Post("/v1/configurations", s.handler.createConfiguration)
	r.Get("/v1/configurations", s.handler.listConfigurations)
	r.Get("/v1/kafka-versions", s.handler.listKafkaVersions)

	// Cluster routes with ARN-in-path: dispatched via path suffix.
	r.Get("/v1/clusters/*", s.handler.clusterGetDispatch)
	r.Delete("/v1/clusters/*", s.handler.deleteCluster)
	r.Put("/v1/clusters/*", s.handler.clusterPutDispatch)

	// V2 cluster API (provisioned + serverless)
	r.Post("/v2/clusters", s.handler.createClusterV2)
	r.Get("/v2/clusters/*", s.handler.describeClusterV2)

	// Configuration routes with ARN-in-path
	r.Get("/v1/configurations/*", s.handler.describeConfiguration)
	r.Delete("/v1/configurations/*", s.handler.deleteConfiguration)

	// Tag routes with ARN-in-path
	r.Get("/v1/tags/*", s.handler.listTagsForResource)
	r.Post("/v1/tags/*", s.handler.tagResource)
	r.Delete("/v1/tags/*", s.handler.untagResource)
}

// InitBus wires the event bus for MSK lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
	bus.Subscribe(events.DockerContainerDied, s.handler.handleContainerEvent)
	bus.Subscribe(events.DockerContainerStopped, s.handler.handleContainerEvent)
	bus.Subscribe(events.DockerContainerStarted, s.handler.handleContainerStarted)
}

// SetDocker wires the Docker client for MSK container management and starts
// the DockerGC background remove loop.
func (s *Service) SetDocker(dc *docker.Client) {
	s.handler.docker = dc
	s.handler.puller = docker.NewImagePuller(dc)
	s.handler.gc = docker.NewGC(dc, s.log.ZapLogger(), s.handler.cfg.MSKKeepContainers)
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

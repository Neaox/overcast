package elasticache

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const cacheXMLNS = "http://elasticache.amazonaws.com/doc/2015-02-02/"

var cacheTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterValue",
	InvalidCode:     "InvalidParameterValue",
	ExceededMessage: "Tag list exceeds maximum number of tags allowed.",
}

// Handler handles ElastiCache Query-protocol requests.
type Handler struct {
	cfg         *config.Config
	store       *cacheStore
	log         *serviceutil.ServiceLogger
	clk         clock.Clock
	bus         *events.Bus
	scheduler   *lifecycle.Scheduler
	docker      *docker.Client
	dockerReady atomic.Bool
	puller      *docker.ImagePuller
	gc          *docker.GC
	// vpcResolver maps a cache's VPC onto its Docker network, so a cache placed
	// in a VPC is reachable from the rest of it. nil when EC2 is not enabled,
	// which puts every cache on the default plane.
	vpcResolver VPCNetworkResolver
	dockerWg    sync.WaitGroup // tracks in-flight container-start goroutines
	typedOp     map[string]op.Operation
	// bgCtx is cancelled by Service.Stop so async goroutines and scheduler
	// callbacks can abandon Docker/store work once shutdown begins.
	bgCtx    context.Context
	bgCancel context.CancelFunc

	// One writer at a time per record — see locks.go.
	clusterLocks    serviceutil.RecordLocks
	rgLocks         serviceutil.RecordLocks
	serverlessLocks serviceutil.RecordLocks
}

func newHandler(cfg *config.Config, store *cacheStore, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	bgCtx, bgCancel := context.WithCancel(context.Background())
	h := &Handler{
		cfg:       cfg,
		store:     store,
		log:       log,
		clk:       clk,
		scheduler: lifecycle.NewScheduler(clk),
		bgCtx:     bgCtx,
		bgCancel:  bgCancel,
	}
	h.typedOp = h.typedOps()
	return h
}

func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

// ── Engine defaults ──────────────────────────────────────────────────────────

const (
	defaultRedisVersion     = "7.1"
	defaultRedisPort        = 6379
	defaultMemcachedVersion = "1.6"
	defaultMemcachedPort    = 11211
	defaultValkeyVersion    = "7.2"
	defaultNodeType         = "cache.t3.micro"
)

// cacheEngineImages maps engine → version → Docker image tag.
var cacheEngineImages = map[string]map[string]string{
	"redis": {
		"7.1": "redis:7",
		"7.0": "redis:7",
		"6.x": "redis:6",
	},
	"valkey": {
		"7.2": "valkey/valkey:7",
		"8.0": "valkey/valkey:8",
	},
	"memcached": {
		"1.6": "memcached:1.6",
		"1.5": "memcached:1.5",
	},
}

// enginePort returns the default container port for an engine.
func enginePort(engine string) int {
	if engine == "memcached" {
		return defaultMemcachedPort
	}
	return defaultRedisPort
}

// engineDefaultVersion returns the default version string for an engine.
func engineDefaultVersion(engine string) string {
	switch engine {
	case "memcached":
		return defaultMemcachedVersion
	case "valkey":
		return defaultValkeyVersion
	default:
		return defaultRedisVersion
	}
}

// engineImage returns the Docker image tag for the given engine and version,
// falling back to a sensible default when the exact version is not mapped.
func engineImage(engine, version string) string {
	if byVersion, ok := cacheEngineImages[engine]; ok {
		if img, ok := byVersion[version]; ok {
			return img
		}
	}
	switch engine {
	case "memcached":
		return "memcached:1.6"
	case "valkey":
		return "valkey/valkey:7"
	default:
		return "redis:7"
	}
}

// ── XML response types ───────────────────────────────────────────────────────

type xmlCreateCacheClusterResponse struct {
	XMLName          xml.Name                    `xml:"CreateCacheClusterResponse"`
	Xmlns            string                      `xml:"xmlns,attr"`
	Result           xmlCreateCacheClusterResult `xml:"CreateCacheClusterResult"`
	ResponseMetadata protocol.ResponseMetadata   `xml:"ResponseMetadata"`
}

type xmlCreateCacheClusterResult struct {
	CacheCluster xmlCacheCluster `xml:"CacheCluster"`
}

type xmlDeleteCacheClusterResponse struct {
	XMLName          xml.Name                    `xml:"DeleteCacheClusterResponse"`
	Xmlns            string                      `xml:"xmlns,attr"`
	Result           xmlDeleteCacheClusterResult `xml:"DeleteCacheClusterResult"`
	ResponseMetadata protocol.ResponseMetadata   `xml:"ResponseMetadata"`
}

type xmlDeleteCacheClusterResult struct {
	CacheCluster xmlCacheCluster `xml:"CacheCluster"`
}

type xmlDescribeCacheClustersResponse struct {
	XMLName          xml.Name                       `xml:"DescribeCacheClustersResponse"`
	Xmlns            string                         `xml:"xmlns,attr"`
	Result           xmlDescribeCacheClustersResult `xml:"DescribeCacheClustersResult"`
	ResponseMetadata protocol.ResponseMetadata      `xml:"ResponseMetadata"`
}

type xmlDescribeCacheClustersResult struct {
	CacheClusters xmlCacheClusters `xml:"CacheClusters"`
}

type xmlCacheClusters struct {
	Items []xmlCacheCluster `xml:"CacheCluster"`
}

type xmlCacheCluster struct {
	CacheClusterId            string       `xml:"CacheClusterId"`
	CacheClusterStatus        string       `xml:"CacheClusterStatus"`
	CacheNodeType             string       `xml:"CacheNodeType"`
	Engine                    string       `xml:"Engine"`
	EngineVersion             string       `xml:"EngineVersion"`
	NumCacheNodes             int          `xml:"NumCacheNodes"`
	PreferredAvailabilityZone string       `xml:"PreferredAvailabilityZone,omitempty"`
	CacheSubnetGroupName      string       `xml:"CacheSubnetGroupName,omitempty"`
	ReplicationGroupId        string       `xml:"ReplicationGroupId,omitempty"`
	CacheParameterGroupName   string       `xml:"CacheParameterGroupName,omitempty"`
	ARN                       string       `xml:"ARN"`
	ConfigurationEndpoint     *xmlEndpoint `xml:"ConfigurationEndpoint,omitempty"`
}

type xmlModifyCacheClusterResponse struct {
	XMLName          xml.Name                    `xml:"ModifyCacheClusterResponse"`
	Xmlns            string                      `xml:"xmlns,attr"`
	Result           xmlModifyCacheClusterResult `xml:"ModifyCacheClusterResult"`
	ResponseMetadata protocol.ResponseMetadata   `xml:"ResponseMetadata"`
}

type xmlModifyCacheClusterResult struct {
	CacheCluster xmlCacheCluster `xml:"CacheCluster"`
}

type xmlEndpoint struct {
	Address string `xml:"Address"`
	Port    int    `xml:"Port"`
}

// ── CreateCacheCluster ───────────────────────────────────────────────────────

// CreateCacheCluster creates a new cache cluster and (when Docker is available)
// starts a real Redis container. The cluster transitions to "available" once
// the TCP health check succeeds, matching AWS semantics.
func (h *Handler) CreateCacheCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("CacheClusterId")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("CacheClusterId is required"))
		return
	}

	// Duplicate check.
	if _, aerr := h.store.getCacheCluster(r.Context(), id); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errClusterAlreadyExists(id))
		return
	}

	engine := r.FormValue("Engine")
	if engine == "" {
		engine = "redis"
	}
	if engine != "redis" && engine != "memcached" && engine != "valkey" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("Engine must be redis, valkey, or memcached"))
		return
	}

	engineVersion := r.FormValue("EngineVersion")
	if engineVersion == "" {
		engineVersion = engineDefaultVersion(engine)
	}

	nodeType := r.FormValue("CacheNodeType")
	if nodeType == "" {
		nodeType = defaultNodeType
	}

	numNodes := formInt(r, "NumCacheNodes", 1)
	replicationGroupID := r.FormValue("ReplicationGroupId")
	subnetGroupName := r.FormValue("CacheSubnetGroupName")
	az := r.FormValue("PreferredAvailabilityZone")

	region := h.store.region(r.Context())
	arn := fmt.Sprintf("arn:aws:elasticache:%s:%s:cluster:%s", region, h.cfg.AccountID, id)

	endpoint := &ClusterEndpoint{
		Address: fmt.Sprintf("%s.%s.cfg.%s", id, region, h.cfg.ExternalHostname()),
		Port:    defaultRedisPort,
	}

	cluster := &CacheCluster{
		CacheClusterId:            id,
		CacheClusterStatus:        "creating",
		CacheNodeType:             nodeType,
		Engine:                    engine,
		EngineVersion:             engineVersion,
		NumCacheNodes:             numNodes,
		PreferredAvailabilityZone: az,
		CacheSubnetGroupName:      subnetGroupName,
		ReplicationGroupId:        replicationGroupID,
		ARN:                       arn,
		ConfigurationEndpoint:     endpoint,
	}

	if aerr := h.store.putCacheCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	clusterID := id
	if h.dockerReady.Load() {
		// Docker is available — start a real container. The health check will
		// transition to "available" once the process is listening. We do NOT
		// run the metadata transition here; that would mark the cluster "available"
		// before the container is ready, making the health check a no-op.
		if h.puller != nil {
			h.puller.Prewarm(engineImage(engine, engineVersion))
		}
		h.dockerWg.Add(1)
		go func() {
			defer h.dockerWg.Done()
			bgCtx := middleware.ContextWithRegion(context.Background(), region)
			got, aerr := h.store.getCacheCluster(bgCtx, clusterID)
			if aerr != nil || got == nil {
				return
			}
			if err := h.startCacheContainer(bgCtx, got); err != nil {
				h.log.Warn("failed to start Docker container for ElastiCache cluster — cluster stays in creating state",
					zap.String("cluster", clusterID), zap.Error(err))
				return
			}
			// The start took real time; the cluster may have been deleted
			// meanwhile. Delete could not stop this container — its ID was not
			// persisted yet — so the start goroutine owns the teardown. Merge
			// the container fields into a fresh read rather than persisting the
			// pre-start snapshot, which would put the cluster back to
			// "creating" and leave the delete undone. Same as the typed path.
			if _, aerr := h.mutateCacheCluster(bgCtx, clusterID, func(stored *CacheCluster) *protocol.AWSError {
				if stored.CacheClusterStatus == "deleting" {
					return errRecordMovedOn
				}
				stored.DockerContainerID = got.DockerContainerID
				stored.HostPort = got.HostPort
				stored.ConfigurationEndpoint = got.ConfigurationEndpoint
				return nil
			}); aerr != nil {
				if aerr != errRecordMovedOn {
					h.log.Warn("ElastiCache: persist post-start cluster",
						zap.String("cluster", clusterID), zap.String("error", aerr.Message))
				}
				h.teardownOrphanedContainer(bgCtx, "cache cluster", clusterID, got.DockerContainerID, got.HostPort)
				return
			}
			h.scheduleHealthCheck(region, clusterID, got.ConfigurationEndpoint.Address, got.ConfigurationEndpoint.Port)
		}()
	}
	// Docker is not available — leave the cluster in "creating".
	// The /_health endpoint and web UI banner tell the user why.

	h.publish(r, events.ElastiCacheClusterCreated, events.ResourcePayload{Name: id, ARN: arn})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateCacheClusterResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlCreateCacheClusterResult{CacheCluster: toXMLCacheCluster(cluster)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DescribeCacheClusters ────────────────────────────────────────────────────

func (h *Handler) DescribeCacheClusters(w http.ResponseWriter, r *http.Request) {
	filterID := r.FormValue("CacheClusterId")

	if filterID != "" {
		cluster, aerr := h.store.getCacheCluster(r.Context(), filterID)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		docker.SetBackingHeaders(w, h.dockerReady.Load(), docker.ContainerHealthUnknown)
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeCacheClustersResponse{
			Xmlns: cacheXMLNS,
			Result: xmlDescribeCacheClustersResult{
				CacheClusters: xmlCacheClusters{Items: []xmlCacheCluster{toXMLCacheCluster(cluster)}},
			},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	all, aerr := h.store.listCacheClusters(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	items := make([]xmlCacheCluster, 0, len(all))
	for _, c := range all {
		items = append(items, toXMLCacheCluster(c))
	}
	docker.SetBackingHeaders(w, h.dockerReady.Load(), docker.ContainerHealthUnknown)
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeCacheClustersResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlDescribeCacheClustersResult{CacheClusters: xmlCacheClusters{Items: items}},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── DeleteCacheCluster ───────────────────────────────────────────────────────

func (h *Handler) DeleteCacheCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("CacheClusterId")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("CacheClusterId is required"))
		return
	}

	var containerID string
	var hostPort int
	cluster, aerr := h.mutateCacheCluster(r.Context(), id, func(cluster *CacheCluster) *protocol.AWSError {
		containerID = cluster.DockerContainerID
		hostPort = cluster.HostPort
		cluster.CacheClusterStatus = "deleting"
		return nil
	})
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	h.publish(r, events.ElastiCacheClusterDeleted, events.ResourcePayload{Name: id, ARN: cluster.ARN})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteCacheClusterResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlDeleteCacheClusterResult{CacheCluster: toXMLCacheCluster(cluster)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})

	h.scheduler.CancelScoped(h.store.region(r.Context()), id, "health")

	if h.gc != nil && containerID != "" {
		h.gc.StopNow(containerID)
		h.gc.ScheduleRemove(containerID)
	}
	if hostPort > 0 {
		_ = h.store.releasePort(r.Context(), hostPort) //nolint:errcheck
	}

	h.scheduler.AfterScoped(h.store.region(r.Context()), id, "delete", 50*time.Millisecond, func(ctx context.Context) {
		if aerr := h.store.deleteCacheCluster(ctx, id); aerr != nil {
			h.log.Warn("failed to delete cache cluster record", zap.String("cluster", id), zap.Error(aerr))
		}
	})
}

// ── Tagging ──────────────────────────────────────────────────────────────────

// ── Tagging XML types ─────────────────────────────────────────────────────────

type xmlTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type xmlTagList struct {
	Items []xmlTag `xml:"Tag"`
}

type xmlAddTagsResponse struct {
	XMLName          xml.Name                  `xml:"AddTagsToResourceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlAddTagsResult          `xml:"AddTagsToResourceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlAddTagsResult struct {
	TagList xmlTagList `xml:"TagList"`
}

type xmlListTagsResponse struct {
	XMLName          xml.Name                  `xml:"ListTagsForResourceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlListTagsResult         `xml:"ListTagsForResourceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlListTagsResult struct {
	TagList xmlTagList `xml:"TagList"`
}

type xmlRemoveTagsResponse struct {
	XMLName          xml.Name                  `xml:"RemoveTagsFromResourceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

// ── Tagging handlers ──────────────────────────────────────────────────────────

func (h *Handler) AddTagsToResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ResourceName is required"))
		return
	}
	var pairs []serviceutil.TagPair
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.Tag.%d.Key", i))
		if key == "" {
			break
		}
		pairs = append(pairs, serviceutil.TagPair{Key: key, Value: r.FormValue(fmt.Sprintf("Tags.Tag.%d.Value", i))})
	}
	tags, aerr := serviceutil.ApplyTagsToStore(r.Context(), cacheTagCfg, nsTags, arn, pairs, h.store.store)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	items := make([]xmlTag, 0, len(tags))
	for k, v := range tags {
		items = append(items, xmlTag{Key: k, Value: v})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlAddTagsResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlAddTagsResult{TagList: xmlTagList{Items: items}},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ResourceName is required"))
		return
	}
	tags, aerr := serviceutil.TagsFromStore(r.Context(), h.store.store, nsTags, arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if tags == nil {
		tags = map[string]string{}
	}
	items := make([]xmlTag, 0, len(tags))
	for k, v := range tags {
		items = append(items, xmlTag{Key: k, Value: v})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlListTagsResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlListTagsResult{TagList: xmlTagList{Items: items}},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) RemoveTagsFromResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ResourceName is required"))
		return
	}
	var keys []string
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if key == "" {
			break
		}
		keys = append(keys, key)
	}
	if _, aerr := serviceutil.RemoveTagsFromStore(r.Context(), nsTags, arn, keys, h.store.store); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlRemoveTagsResponse{
		Xmlns:            cacheXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

// ── Docker helpers ───────────────────────────────────────────────────────────

// startCacheContainer creates (or reuses) and starts a Docker container for the
// given cache cluster. Supports redis, valkey, and memcached engines. Updates
// c.DockerContainerID, c.HostPort, and c.ConfigurationEndpoint in place.
// Follows the same reuse-on-restart semantics as RDS.
func (h *Handler) startCacheContainer(ctx context.Context, c *CacheCluster) error {
	image := engineImage(c.Engine, c.EngineVersion)
	port := enginePort(c.Engine)

	containerName := "overcast-elasticache-" + c.CacheClusterId
	containerPort := fmt.Sprintf("%d/tcp", port)

	// Check for an existing container (post-restart reuse).
	if existing, err := h.docker.GetContainerByName(ctx, containerName); err == nil && existing != nil {
		if !existing.HasOvercastLabels(serviceName, c.CacheClusterId) {
			return fmt.Errorf("container %q exists but is not an overcast-managed ElastiCache container — refusing to reuse", containerName)
		}
		h.log.Info("ElastiCache: reusing existing container",
			zap.String("cluster", c.CacheClusterId),
			zap.String("container", existing.ID),
			zap.String("state", existing.State.Status))

		hostPort := 0
		if bindings, ok := existing.NetworkSettings.Ports[containerPort]; ok && len(bindings) > 0 {
			if p, err := strconv.Atoi(bindings[0].HostPort); err == nil {
				hostPort = p
			}
		}
		if hostPort == 0 {
			portBase := h.portBase()
			if hp, aerr := h.store.allocatePort(ctx, c.CacheClusterId, portBase); aerr == nil {
				hostPort = hp
			}
		} else {
			h.store.allocatePortFixed(ctx, c.CacheClusterId, hostPort) //nolint:errcheck
		}
		if !existing.State.Running {
			if err := h.docker.StartContainer(ctx, existing.ID); err != nil {
				return fmt.Errorf("start existing container: %w", err)
			}
		}
		c.DockerContainerID = existing.ID
		c.HostPort = hostPort
		// Re-attach: a container adopted from an earlier run predates the
		// current alias set. Attaching is idempotent.
		if err := h.attachAdoptedToDataPlane(ctx, existing.ID,
			h.vpcForSubnetGroup(ctx, c.CacheSubnetGroupName), h.clusterEndpointAliases(c)); err != nil {
			h.log.Warn("ElastiCache: reused container could not join the data plane — "+
				"its endpoint name will not resolve for sibling containers",
				zap.String("cluster", c.CacheClusterId), zap.Error(err))
		}
		h.setContainerEndpoint(ctx, c)
		return nil
	}

	// Allocate a host port.
	hostPort, aerr := h.store.allocatePort(ctx, c.CacheClusterId, h.portBase())
	if aerr != nil {
		return fmt.Errorf("allocate port: %s", aerr.Message)
	}

	// Pull image (deduplicated per process lifetime).
	if err := h.puller.Ensure(ctx, image); err != nil {
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("pull image: %w", err)
	}

	req := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:        image,
			ExposedPorts: map[string]struct{}{containerPort: {}},
			Labels:       docker.ManagedLabels(serviceName, c.CacheClusterId),
		},
		HostConfig: &docker.HostConfig{AutoRemove: true,
			NetworkMode: dataplane.Primary(h.cfg),
			PortBindings: map[string][]docker.PortBinding{
				containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
		NetworkingConfig: dataplane.PrimaryEndpoints(h.cfg),
	}

	containerID, err := h.docker.CreateContainer(ctx, containerName, req)
	if err != nil {
		if docker.IsConflict(err) {
			h.log.Warn("ElastiCache: name conflict on create, retrying reuse",
				zap.String("cluster", c.CacheClusterId))
			h.store.releasePort(ctx, hostPort) //nolint:errcheck
			return h.startCacheContainer(ctx, c)
		}
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("create container: %w", err)
	}

	// Join the data plane before starting, so the node answers to its endpoint
	// names from the moment it accepts connections. Every name, on the plane
	// every consumer shares — an ECS task included, which is what #872 was.
	if err := h.attachToDataPlane(ctx, containerID, h.vpcForSubnetGroup(ctx, c.CacheSubnetGroupName), h.clusterEndpointAliases(c)); err != nil {
		h.docker.RemoveContainerForce(containerID) //nolint:errcheck
		h.store.releasePort(ctx, hostPort)         //nolint:errcheck
		return fmt.Errorf("ElastiCache %s: %w", c.CacheClusterId, err)
	}

	if err := h.docker.StartContainer(ctx, containerID); err != nil {
		h.docker.RemoveContainerForce(containerID) //nolint:errcheck
		h.store.releasePort(ctx, hostPort)         //nolint:errcheck
		return fmt.Errorf("start container: %w", err)
	}

	c.DockerContainerID = containerID
	c.HostPort = hostPort
	h.setContainerEndpoint(ctx, c)
	return nil
}

// attachToDataPlane joins a cache container to the plane its consumers are on,
// advertising every name the API could hand out for it.
//
// A cache Overcast cannot place in a VPC — no subnet group, no subnets, or no
// EC2 service to ask — lands on the default plane. That is the same plane
// everything else without a VPC is on, so it stays reachable either way.
func (h *Handler) attachToDataPlane(ctx context.Context, containerID, vpcID string, aliases []string) error {
	placement, err := h.placementFor(ctx, vpcID, aliases)
	if err != nil {
		return err
	}
	return dataplane.Attach(ctx, h.docker, h.cfg, containerID, placement)
}

// attachAdoptedToDataPlane is attachToDataPlane for a container reused after a
// restart, which may predate the current plane layout entirely.
func (h *Handler) attachAdoptedToDataPlane(ctx context.Context, containerID, vpcID string, aliases []string) error {
	placement, err := h.placementFor(ctx, vpcID, aliases)
	if err != nil {
		return err
	}
	return dataplane.AttachAdopted(ctx, h.docker, h.cfg, containerID, placement)
}

func (h *Handler) placementFor(ctx context.Context, vpcID string, aliases []string) (dataplane.Placement, error) {
	var resolver dataplane.VPCResolver
	if h.vpcResolver != nil {
		resolver = h.vpcResolver
	}
	placement, err := dataplane.PlaceInVPC(ctx, resolver, vpcID)
	if err != nil {
		return placement, err
	}
	placement.Aliases = aliases
	return placement, nil
}

// setContainerEndpoint updates the cluster's ConfigurationEndpoint to reflect
// the actual container address: the container's own address when Overcast runs
// beside it, 127.0.0.1 + host-port when running natively.
func (h *Handler) setContainerEndpoint(ctx context.Context, c *CacheCluster) {
	c.ConfigurationEndpoint = h.endpointFor(ctx, c.DockerContainerID, c.Engine, c.HostPort)
}

// setReplicationGroupEndpoint updates a replication group's ConfigurationEndpoint
// using the same Docker-vs-native logic as setContainerEndpoint.
func (h *Handler) setReplicationGroupEndpoint(ctx context.Context, rg *ReplicationGroup) {
	rg.ConfigurationEndpoint = h.endpointFor(ctx, rg.DockerContainerID, rg.Engine, rg.HostPort)
}

// endpointFor is the address/port pair the two setters above share: the engine
// port on the container's own address when Overcast is containerised beside
// it, else the published port on loopback.
func (h *Handler) endpointFor(ctx context.Context, containerID, engine string, hostPort int) *ClusterEndpoint {
	if addr := dataplane.ContainerAddr(ctx, h.docker, h.cfg, containerID); addr != "" {
		return &ClusterEndpoint{Address: addr, Port: enginePort(engine)}
	}
	return &ClusterEndpoint{Address: "127.0.0.1", Port: hostPort}
}

// cleanupCacheContainer releases the port reservation for a cache cluster.
// Docker container stop/remove is handled by the GC.
//
//nolint:unused // Kept for explicit Docker cleanup call sites.
func (h *Handler) cleanupCacheContainer(ctx context.Context, clusterID, containerID string, hostPort int) {
	if hostPort > 0 {
		if aerr := h.store.releasePort(ctx, hostPort); aerr != nil {
			h.log.Warn("ElastiCache cleanup: release port",
				zap.String("cluster", clusterID), zap.Int("port", hostPort), zap.Error(aerr))
		}
	}
}

// scheduleHealthCheck polls TCP connectivity and transitions the cluster to
// "available" once Redis responds. After maxRetries the cluster stays in its
// current state — it is never falsely marked "available" when the engine is
// not answering. A Docker container-died event arriving after exhaustion will
// still transition to "stopped".
// region is the region the cluster is stored under — the callbacks run outside
// any request context.
func (h *Handler) scheduleHealthCheck(region, clusterID, host string, port int) {
	const maxRetries = 30
	var attempt int
	var check func(ctx context.Context)
	check = func(ctx context.Context) {
		attempt++
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
		if err == nil {
			conn.Close()
			h.transitionCacheCluster(ctx, clusterID, "available", "creating", "starting")
			return
		}
		if attempt < maxRetries {
			h.scheduler.AfterScoped(region, clusterID, "health", 2*time.Second, check)
		} else {
			h.log.Warn("ElastiCache health check timed out — cluster stays in current state",
				zap.String("cluster", clusterID), zap.Int("attempts", attempt))
		}
	}
	h.scheduler.AfterScoped(region, clusterID, "health", 1*time.Second, check)
}

// teardownOrphanedContainer removes a container whose resource record was
// deleted while the container was still starting. The delete path could not
// stop it — the container ID had not been persisted yet — so the start
// goroutine owns the cleanup, including the port it allocated. Mirrors the
// RDS fix for the same race (#412); the compat teardowns create-and-delete
// fast enough to hit this window on every run.
func (h *Handler) teardownOrphanedContainer(ctx context.Context, kind, id, containerID string, hostPort int) {
	h.log.Info("ElastiCache: "+kind+" deleted while its container was starting — removing container",
		zap.String("id", id), zap.String("container", containerID))
	if containerID != "" {
		if h.gc != nil {
			h.gc.StopNow(containerID)
			h.gc.ScheduleRemove(containerID)
		} else {
			_ = h.docker.StopContainer(ctx, containerID, 10)     //nolint:errcheck
			_ = h.docker.RemoveContainer(ctx, containerID, true) //nolint:errcheck
		}
	}
	if hostPort > 0 {
		_ = h.store.releasePort(ctx, hostPort) //nolint:errcheck
	}
}

// startReplicationGroupContainer creates (or reuses) and starts a single Docker
// container for the given replication group (primary node). Updates rg.DockerContainerID,
// rg.HostPort, and rg.ConfigurationEndpoint in place.
func (h *Handler) startReplicationGroupContainer(ctx context.Context, rg *ReplicationGroup) error {
	image := engineImage(rg.Engine, rg.EngineVersion)
	port := enginePort(rg.Engine)

	containerName := "overcast-elasticache-rg-" + rg.ReplicationGroupId
	containerPort := fmt.Sprintf("%d/tcp", port)
	resourceLabel := "rg:" + rg.ReplicationGroupId

	if existing, err := h.docker.GetContainerByName(ctx, containerName); err == nil && existing != nil {
		if !existing.HasOvercastLabels(serviceName, resourceLabel) {
			return fmt.Errorf("container %q exists but is not an overcast-managed replication group container — refusing to reuse", containerName)
		}
		h.log.Info("ElastiCache: reusing existing replication group container",
			zap.String("rg", rg.ReplicationGroupId),
			zap.String("container", existing.ID))

		hostPort := 0
		if bindings, ok := existing.NetworkSettings.Ports[containerPort]; ok && len(bindings) > 0 {
			if p, err := strconv.Atoi(bindings[0].HostPort); err == nil {
				hostPort = p
			}
		}
		if hostPort == 0 {
			if hp, aerr := h.store.allocatePort(ctx, resourceLabel, h.portBase()); aerr == nil {
				hostPort = hp
			}
		} else {
			h.store.allocatePortFixed(ctx, resourceLabel, hostPort) //nolint:errcheck
		}
		if !existing.State.Running {
			if err := h.docker.StartContainer(ctx, existing.ID); err != nil {
				return fmt.Errorf("start existing container: %w", err)
			}
		}
		rg.DockerContainerID = existing.ID
		rg.HostPort = hostPort
		if err := h.attachAdoptedToDataPlane(ctx, existing.ID, "", h.replicationGroupEndpointAliases(rg)); err != nil {
			h.log.Warn("ElastiCache: reused container could not join the data plane — "+
				"its endpoint name will not resolve for sibling containers",
				zap.String("rg", rg.ReplicationGroupId), zap.Error(err))
		}
		h.setReplicationGroupEndpoint(ctx, rg)
		return nil
	}

	hostPort, aerr := h.store.allocatePort(ctx, resourceLabel, h.portBase())
	if aerr != nil {
		return fmt.Errorf("allocate port: %s", aerr.Message)
	}

	if err := h.puller.Ensure(ctx, image); err != nil {
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("pull image: %w", err)
	}

	req := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:        image,
			ExposedPorts: map[string]struct{}{containerPort: {}},
			Labels:       docker.ManagedLabels(serviceName, resourceLabel),
		},
		HostConfig: &docker.HostConfig{AutoRemove: true,
			NetworkMode: dataplane.Primary(h.cfg),
			PortBindings: map[string][]docker.PortBinding{
				containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
		NetworkingConfig: dataplane.PrimaryEndpoints(h.cfg),
	}

	containerID, err := h.docker.CreateContainer(ctx, containerName, req)
	if err != nil {
		if docker.IsConflict(err) {
			h.store.releasePort(ctx, hostPort) //nolint:errcheck
			return h.startReplicationGroupContainer(ctx, rg)
		}
		h.store.releasePort(ctx, hostPort) //nolint:errcheck
		return fmt.Errorf("create container: %w", err)
	}

	// A replication group record carries no CacheSubnetGroupName, so there is
	// no VPC to derive and it lands on the default plane. That is reachable
	// from everywhere today; it becomes a gap only when placement starts to
	// restrict, so the field is a prerequisite of that phase, not this one.
	if err := h.attachToDataPlane(ctx, containerID, "", h.replicationGroupEndpointAliases(rg)); err != nil {
		h.docker.RemoveContainerForce(containerID) //nolint:errcheck
		h.store.releasePort(ctx, hostPort)         //nolint:errcheck
		return fmt.Errorf("ElastiCache %s: %w", rg.ReplicationGroupId, err)
	}

	if err := h.docker.StartContainer(ctx, containerID); err != nil {
		h.docker.RemoveContainerForce(containerID) //nolint:errcheck
		h.store.releasePort(ctx, hostPort)         //nolint:errcheck
		return fmt.Errorf("start container: %w", err)
	}

	rg.DockerContainerID = containerID
	rg.HostPort = hostPort
	h.setReplicationGroupEndpoint(ctx, rg)
	return nil
}

// Endpoint hostnames. A cache endpoint is a data-plane name: it points at the
// engine container, not at Overcast. AWS's shapes are
// `{cluster}.{hash}.cfg.{region}.cache.amazonaws.com` for a cluster and
// `{group}.{hash}.ng.{region}.cache.amazonaws.com` for a replication group;
// Overcast mints the same grammar with the account-specific hash dropped, on
// each base it could hand a caller. See dataplane.Hostnames for why the whole
// set is registered rather than the configured name alone.

func clusterEndpointHostname(id, region, base string) string {
	return fmt.Sprintf("%s.%s.cfg.%s", id, region, base)
}

func replicationGroupEndpointHostname(id, region, base string) string {
	return fmt.Sprintf("%s.%s.ng.cfg.%s", id, region, base)
}

func (h *Handler) clusterEndpointAliases(c *CacheCluster) []string {
	if c == nil || c.CacheClusterId == "" {
		return nil
	}
	return dataplane.Hostnames(h.cfg, func(base string) string {
		return clusterEndpointHostname(c.CacheClusterId, h.region(), base)
	}, advertisedAddress(c.ConfigurationEndpoint))
}

func (h *Handler) replicationGroupEndpointAliases(rg *ReplicationGroup) []string {
	if rg == nil || rg.ReplicationGroupId == "" {
		return nil
	}
	return dataplane.Hostnames(h.cfg, func(base string) string {
		return replicationGroupEndpointHostname(rg.ReplicationGroupId, h.region(), base)
	}, advertisedAddress(rg.ConfigurationEndpoint))
}

// advertisedAddress is whatever a stored record already hands out, so a
// container keeps answering to a name minted before the current configuration.
func advertisedAddress(ep *ClusterEndpoint) string {
	if ep == nil {
		return ""
	}
	return ep.Address
}

// vpcForSubnetGroup returns the VPC a cache subnet group belongs to, or "" for
// a cache that named no group or named one Overcast has no record of. Both
// mean the default plane rather than an error: a cache with nowhere better to
// be is still a working cache.
//
// A subnet group whose own VpcId was never populated is resolved through its
// subnets, which is how RDS fills the same field in.
func (h *Handler) vpcForSubnetGroup(ctx context.Context, name string) string {
	if name == "" {
		return ""
	}
	sg, aerr := h.store.getCacheSubnetGroup(ctx, name)
	if aerr != nil || sg == nil {
		return ""
	}
	if sg.VpcId != "" {
		return sg.VpcId
	}
	return h.vpcForSubnets(ctx, sg.SubnetIds)
}

// vpcForSubnets resolves the first subnet that names a VPC. A serverless cache
// carries subnet IDs directly rather than through a group.
func (h *Handler) vpcForSubnets(ctx context.Context, subnetIDs []string) string {
	if h.vpcResolver == nil {
		return ""
	}
	for _, id := range subnetIDs {
		if vpcID := h.vpcResolver.VpcIDForSubnet(ctx, id); vpcID != "" {
			return vpcID
		}
	}
	return ""
}

func (h *Handler) region() string {
	if h.cfg != nil && h.cfg.Region != "" {
		return h.cfg.Region
	}
	return "us-east-1"
}

// cleanupReplicationGroupContainer releases the port for a replication group container.
// Docker container stop/remove is handled by the GC.
//
//nolint:unused // Kept for explicit Docker cleanup call sites.
func (h *Handler) cleanupReplicationGroupContainer(ctx context.Context, rgID, containerID string, hostPort int) {
	if hostPort > 0 {
		if aerr := h.store.releasePort(ctx, hostPort); aerr != nil {
			h.log.Warn("ElastiCache cleanup: release RG port",
				zap.String("rg", rgID), zap.Int("port", hostPort), zap.Error(aerr))
		}
	}
}

// scheduleReplicationGroupHealthCheck polls TCP connectivity and transitions the
// replication group to "available" once the container responds. region is the
// region the replication group is stored under.
func (h *Handler) scheduleReplicationGroupHealthCheck(region, rgID, host string, port int) {
	const maxRetries = 30
	var attempt int
	var check func(ctx context.Context)
	check = func(ctx context.Context) {
		attempt++
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
		if err == nil {
			conn.Close()
			h.transitionReplicationGroup(ctx, rgID, "available", "creating", "starting")
			return
		}
		if attempt < maxRetries {
			h.scheduler.AfterScoped(region, rgID, "rg-health", 2*time.Second, check)
		} else {
			h.log.Warn("ElastiCache RG health check timed out", zap.String("rg", rgID), zap.Int("attempts", attempt))
			h.transitionReplicationGroup(ctx, rgID, "available", "creating", "starting")
		}
	}
	h.scheduler.AfterScoped(region, rgID, "rg-health", 1*time.Second, check)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) portBase() int {
	if h.cfg.ElastiCachePortBase > 0 {
		return h.cfg.ElastiCachePortBase
	}
	return 63790
}

// VPCNetworkResolver resolves cache placement against EC2 VPC network state.
// Implemented by the EC2 service; nil when EC2 is not enabled.
type VPCNetworkResolver interface {
	dataplane.VPCResolver
	// VpcIDForSubnet returns the VPC ID that owns the given subnet.
	VpcIDForSubnet(ctx context.Context, subnetID string) string
}

// ── ModifyCacheCluster ───────────────────────────────────────────────────────

func (h *Handler) ModifyCacheCluster(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("CacheClusterId")
	if id == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("CacheClusterId is required"))
		return
	}

	cluster, aerr := h.store.getCacheCluster(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	if v := r.FormValue("CacheNodeType"); v != "" {
		cluster.CacheNodeType = v
	}
	if v := r.FormValue("EngineVersion"); v != "" {
		cluster.EngineVersion = v
	}
	if v := r.FormValue("NumCacheNodes"); v != "" {
		cluster.NumCacheNodes = formInt(r, "NumCacheNodes", cluster.NumCacheNodes)
	}
	if v := r.FormValue("CacheParameterGroupName"); v != "" {
		cluster.CacheParameterGroupName = v
	}

	cluster.CacheClusterStatus = "modifying"
	if aerr := h.store.putCacheCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	h.publish(r, events.ElastiCacheClusterModified, events.ResourcePayload{Name: id, ARN: cluster.ARN})

	// Schedule transition back to available.
	h.scheduler.AfterScoped(h.store.region(r.Context()), id, "available", 0, func(ctx context.Context) {
		got, aerr := h.store.getCacheCluster(ctx, id)
		if aerr != nil {
			return
		}
		if got.CacheClusterStatus == "modifying" {
			got.CacheClusterStatus = "available"
			h.store.putCacheCluster(ctx, got) //nolint:errcheck
		}
	})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifyCacheClusterResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlModifyCacheClusterResult{CacheCluster: toXMLCacheCluster(cluster)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func toXMLCacheCluster(c *CacheCluster) xmlCacheCluster {
	out := xmlCacheCluster{
		CacheClusterId:            c.CacheClusterId,
		CacheClusterStatus:        c.CacheClusterStatus,
		CacheNodeType:             c.CacheNodeType,
		Engine:                    c.Engine,
		EngineVersion:             c.EngineVersion,
		NumCacheNodes:             c.NumCacheNodes,
		PreferredAvailabilityZone: c.PreferredAvailabilityZone,
		CacheSubnetGroupName:      c.CacheSubnetGroupName,
		ReplicationGroupId:        c.ReplicationGroupId,
		CacheParameterGroupName:   c.CacheParameterGroupName,
		ARN:                       c.ARN,
	}
	if c.ConfigurationEndpoint != nil {
		out.ConfigurationEndpoint = &xmlEndpoint{
			Address: c.ConfigurationEndpoint.Address,
			Port:    c.ConfigurationEndpoint.Port,
		}
	}
	return out
}

func formInt(r *http.Request, key string, def int) int {
	v := r.FormValue(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

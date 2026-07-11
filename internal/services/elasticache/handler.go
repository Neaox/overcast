package elasticache

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const cacheXMLNS = "http://elasticache.amazonaws.com/doc/2015-02-02/"

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
	dockerWg    sync.WaitGroup // tracks in-flight container-start goroutines
	typedOp     map[string]op.Operation
	// bgCtx is cancelled by Service.Stop so async goroutines and scheduler
	// callbacks can abandon Docker/store work once shutdown begins.
	bgCtx    context.Context
	bgCancel context.CancelFunc
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
			bgCtx := context.Background()
			got, aerr := h.store.getCacheCluster(bgCtx, clusterID)
			if aerr != nil || got == nil {
				return
			}
			if err := h.startCacheContainer(bgCtx, got); err != nil {
				h.log.Warn("failed to start Docker container for ElastiCache cluster — falling back to metadata-only",
					zap.String("cluster", clusterID), zap.Error(err))
				// Container failed: fall back so the cluster isn't stuck in "creating".
				h.clusterFallbackAvailable(clusterID)
				return
			}
			if aerr := h.store.putCacheCluster(bgCtx, got); aerr != nil {
				h.log.Warn("ElastiCache: persist post-start cluster",
					zap.String("cluster", clusterID), zap.String("error", aerr.Message))
				return
			}
			h.scheduleHealthCheck(clusterID, got.ConfigurationEndpoint.Address, got.ConfigurationEndpoint.Port)
		}()
	} else {
		// No Docker — use the metadata-only transition so the cluster becomes
		// "available" immediately (0 delay runs synchronously on a real clock).
		h.scheduler.After(clusterID+":available", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getCacheCluster(ctx, clusterID)
			if aerr != nil {
				return
			}
			if got.CacheClusterStatus == "creating" {
				got.CacheClusterStatus = "available"
				h.store.putCacheCluster(ctx, got) //nolint:errcheck
			}
		})
	}

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

	cluster, aerr := h.store.getCacheCluster(r.Context(), id)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	containerID := cluster.DockerContainerID
	hostPort := cluster.HostPort

	cluster.CacheClusterStatus = "deleting"
	if aerr := h.store.putCacheCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	h.publish(r, events.ElastiCacheClusterDeleted, events.ResourcePayload{Name: id, ARN: cluster.ARN})

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteCacheClusterResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlDeleteCacheClusterResult{CacheCluster: toXMLCacheCluster(cluster)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})

	h.scheduler.Cancel(id + ":health")

	if h.gc != nil && containerID != "" {
		h.gc.StopNow(containerID)
		h.gc.ScheduleRemove(containerID)
	}
	if hostPort > 0 {
		_ = h.store.releasePort(r.Context(), hostPort) //nolint:errcheck
	}

	h.scheduler.After(id+":delete", 50*time.Millisecond, func() {
		ctx := context.Background()
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
	tags, aerr := h.store.getTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	// Tags arrive as Tags.Tag.N.Key / Tags.Tag.N.Value
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.Tag.%d.Key", i))
		if key == "" {
			break
		}
		tags[key] = r.FormValue(fmt.Sprintf("Tags.Tag.%d.Value", i))
	}
	if aerr := h.store.setTags(r.Context(), arn, tags); aerr != nil {
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
	tags, aerr := h.store.getTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
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
	tags, aerr := h.store.getTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if key == "" {
			break
		}
		delete(tags, key)
	}
	if aerr := h.store.setTags(r.Context(), arn, tags); aerr != nil {
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

	// Ensure Docker network exists.
	network := h.network()
	if _, err := h.docker.CreateNetwork(ctx, network); err != nil {
		h.log.Warn("ElastiCache: failed to create network (may already exist)",
			zap.String("network", network), zap.Error(err))
	}

	req := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:        image,
			ExposedPorts: map[string]struct{}{containerPort: {}},
			Labels:       docker.ManagedLabels(serviceName, c.CacheClusterId),
		},
		HostConfig: &docker.HostConfig{AutoRemove: true,
			NetworkMode: network,
			PortBindings: map[string][]docker.PortBinding{
				containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
		NetworkingConfig: &docker.NetworkingConfig{
			EndpointsConfig: map[string]*docker.EndpointSettings{
				network: {},
			},
		},
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

// setContainerEndpoint updates the cluster's ConfigurationEndpoint to reflect
// the actual container address: Docker network IP when running inside a
// container, 127.0.0.1 + host-port when running natively.
func (h *Handler) setContainerEndpoint(ctx context.Context, c *CacheCluster) {
	port := enginePort(c.Engine)
	network := h.network()
	if _, err := os.Stat("/.dockerenv"); err == nil {
		hostname, _ := os.Hostname()
		if hostname != "" {
			_ = h.docker.ConnectNetwork(ctx, network, hostname)
		}
		info, err := h.docker.InspectContainer(ctx, c.DockerContainerID)
		if err == nil {
			if ep, ok := info.NetworkSettings.Networks[network]; ok && ep.IPAddress != "" {
				c.ConfigurationEndpoint = &ClusterEndpoint{Address: ep.IPAddress, Port: port}
				return
			}
		}
	}
	c.ConfigurationEndpoint = &ClusterEndpoint{Address: "127.0.0.1", Port: c.HostPort}
}

// setReplicationGroupEndpoint updates a replication group's ConfigurationEndpoint
// using the same Docker-vs-native logic as setContainerEndpoint.
func (h *Handler) setReplicationGroupEndpoint(ctx context.Context, rg *ReplicationGroup) {
	port := enginePort(rg.Engine)
	network := h.network()
	if _, err := os.Stat("/.dockerenv"); err == nil {
		hostname, _ := os.Hostname()
		if hostname != "" {
			_ = h.docker.ConnectNetwork(ctx, network, hostname)
		}
		info, err := h.docker.InspectContainer(ctx, rg.DockerContainerID)
		if err == nil {
			if ep, ok := info.NetworkSettings.Networks[network]; ok && ep.IPAddress != "" {
				rg.ConfigurationEndpoint = &ClusterEndpoint{Address: ep.IPAddress, Port: port}
				return
			}
		}
	}
	rg.ConfigurationEndpoint = &ClusterEndpoint{Address: "127.0.0.1", Port: rg.HostPort}
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
// "available" once Redis responds. Falls back to "available" after maxRetries.
func (h *Handler) scheduleHealthCheck(clusterID, host string, port int) {
	const maxRetries = 30
	var attempt int
	var check func()
	check = func() {
		attempt++
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
		if err == nil {
			conn.Close()
			ctx := context.Background()
			got, aerr := h.store.getCacheCluster(ctx, clusterID)
			if aerr != nil {
				return
			}
			if got.CacheClusterStatus == "creating" || got.CacheClusterStatus == "starting" {
				got.CacheClusterStatus = "available"
				h.store.putCacheCluster(ctx, got) //nolint:errcheck
			}
			return
		}
		if attempt < maxRetries {
			h.scheduler.After(clusterID+":health", 2*time.Second, check)
		} else {
			h.log.Warn("ElastiCache health check timed out", zap.String("cluster", clusterID), zap.Int("attempts", attempt))
			ctx := context.Background()
			got, aerr := h.store.getCacheCluster(ctx, clusterID)
			if aerr != nil {
				return
			}
			if got.CacheClusterStatus == "creating" || got.CacheClusterStatus == "starting" {
				got.CacheClusterStatus = "available"
				h.store.putCacheCluster(ctx, got) //nolint:errcheck
			}
		}
	}
	h.scheduler.After(clusterID+":health", 1*time.Second, check)
}

// clusterFallbackAvailable sets a cluster to "available" if it is still in
// "creating" or "starting". Used when Docker start fails.
func (h *Handler) clusterFallbackAvailable(clusterID string) {
	ctx := context.Background()
	got, aerr := h.store.getCacheCluster(ctx, clusterID)
	if aerr != nil {
		return
	}
	if got.CacheClusterStatus == "creating" || got.CacheClusterStatus == "starting" {
		got.CacheClusterStatus = "available"
		h.store.putCacheCluster(ctx, got) //nolint:errcheck
	}
}

// rgFallbackAvailable sets a replication group to "available" if it is still
// in "creating" or "starting". Used when Docker start fails.
func (h *Handler) rgFallbackAvailable(rgID string) {
	ctx := context.Background()
	got, aerr := h.store.getReplicationGroup(ctx, rgID)
	if aerr != nil {
		return
	}
	if got.Status == "creating" || got.Status == "starting" {
		got.Status = "available"
		h.store.putReplicationGroup(ctx, got) //nolint:errcheck
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

	network := h.network()
	if _, err := h.docker.CreateNetwork(ctx, network); err != nil {
		h.log.Warn("ElastiCache: failed to create network (may already exist)",
			zap.String("network", network), zap.Error(err))
	}

	req := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:        image,
			ExposedPorts: map[string]struct{}{containerPort: {}},
			Labels:       docker.ManagedLabels(serviceName, resourceLabel),
		},
		HostConfig: &docker.HostConfig{AutoRemove: true,
			NetworkMode: network,
			PortBindings: map[string][]docker.PortBinding{
				containerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
		NetworkingConfig: &docker.NetworkingConfig{
			EndpointsConfig: map[string]*docker.EndpointSettings{
				network: {},
			},
		},
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
// replication group to "available" once the container responds.
func (h *Handler) scheduleReplicationGroupHealthCheck(rgID, host string, port int) {
	const maxRetries = 30
	var attempt int
	var check func()
	check = func() {
		attempt++
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
		if err == nil {
			conn.Close()
			ctx := context.Background()
			got, aerr := h.store.getReplicationGroup(ctx, rgID)
			if aerr != nil {
				return
			}
			if got.Status == "creating" || got.Status == "starting" {
				got.Status = "available"
				h.store.putReplicationGroup(ctx, got) //nolint:errcheck
			}
			return
		}
		if attempt < maxRetries {
			h.scheduler.After(rgID+":rg-health", 2*time.Second, check)
		} else {
			h.log.Warn("ElastiCache RG health check timed out", zap.String("rg", rgID), zap.Int("attempts", attempt))
			ctx := context.Background()
			got, aerr := h.store.getReplicationGroup(ctx, rgID)
			if aerr != nil {
				return
			}
			if got.Status == "creating" || got.Status == "starting" {
				got.Status = "available"
				h.store.putReplicationGroup(ctx, got) //nolint:errcheck
			}
		}
	}
	h.scheduler.After(rgID+":rg-health", 1*time.Second, check)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) portBase() int {
	if h.cfg.ElastiCachePortBase > 0 {
		return h.cfg.ElastiCachePortBase
	}
	return 63790
}

func (h *Handler) network() string {
	if h.cfg.ElastiCacheNetwork != "" {
		return h.cfg.ElastiCacheNetwork
	}
	return "overcast_elasticache"
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
	h.scheduler.After(id+":available", 0, func() {
		ctx := context.Background()
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

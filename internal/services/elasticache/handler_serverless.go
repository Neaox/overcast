package elasticache

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/protocol"
)

type xmlCreateServerlessCacheResponse struct {
	XMLName          xml.Name                       `xml:"CreateServerlessCacheResponse"`
	Xmlns            string                         `xml:"xmlns,attr"`
	Result           xmlCreateServerlessCacheResult `xml:"CreateServerlessCacheResult"`
	ResponseMetadata protocol.ResponseMetadata      `xml:"ResponseMetadata"`
}

type xmlCreateServerlessCacheResult struct {
	ServerlessCache xmlServerlessCache `xml:"ServerlessCache"`
}

type xmlDescribeServerlessCachesResponse struct {
	XMLName          xml.Name                          `xml:"DescribeServerlessCachesResponse"`
	Xmlns            string                            `xml:"xmlns,attr"`
	Result           xmlDescribeServerlessCachesResult `xml:"DescribeServerlessCachesResult"`
	ResponseMetadata protocol.ResponseMetadata         `xml:"ResponseMetadata"`
}

type xmlDescribeServerlessCachesResult struct {
	NextToken        string              `xml:"NextToken,omitempty"`
	ServerlessCaches xmlServerlessCaches `xml:"ServerlessCaches"`
}

type xmlServerlessCaches struct {
	Items []xmlServerlessCache `xml:"ServerlessCache"`
}

type xmlDeleteServerlessCacheResponse struct {
	XMLName          xml.Name                       `xml:"DeleteServerlessCacheResponse"`
	Xmlns            string                         `xml:"xmlns,attr"`
	Result           xmlDeleteServerlessCacheResult `xml:"DeleteServerlessCacheResult"`
	ResponseMetadata protocol.ResponseMetadata      `xml:"ResponseMetadata"`
}

type xmlDeleteServerlessCacheResult struct {
	ServerlessCache xmlServerlessCache `xml:"ServerlessCache"`
}

type xmlModifyServerlessCacheResponse struct {
	XMLName          xml.Name                       `xml:"ModifyServerlessCacheResponse"`
	Xmlns            string                         `xml:"xmlns,attr"`
	Result           xmlModifyServerlessCacheResult `xml:"ModifyServerlessCacheResult"`
	ResponseMetadata protocol.ResponseMetadata      `xml:"ResponseMetadata"`
}

type xmlModifyServerlessCacheResult struct {
	ServerlessCache xmlServerlessCache `xml:"ServerlessCache"`
}

type xmlServerlessCache struct {
	ServerlessCacheName   string               `xml:"ServerlessCacheName"`
	Description           string               `xml:"Description"`
	Status                string               `xml:"Status"`
	Engine                string               `xml:"Engine"`
	MajorEngineVersion    string               `xml:"MajorEngineVersion"`
	FullEngineVersion     string               `xml:"FullEngineVersion"`
	CacheUsageLimits      *xmlCacheUsageLimits `xml:"CacheUsageLimits,omitempty"`
	SubnetIds             xmlStringSet         `xml:"SubnetIds,omitempty"`
	SecurityGroupIds      xmlStringSet         `xml:"SecurityGroupIds,omitempty"`
	SnapshotArnsToRestore xmlStringSet         `xml:"SnapshotArnsToRestore,omitempty"`
	Endpoint              *xmlEndpoint         `xml:"Endpoint,omitempty"`
	ReaderEndpoint        *xmlEndpoint         `xml:"ReaderEndpoint,omitempty"`
	ARN                   string               `xml:"ARN"`
	SnapshotRetention     int                  `xml:"SnapshotRetentionLimit"`
	DailySnapshotTime     string               `xml:"DailySnapshotTime,omitempty"`
	NetworkType           string               `xml:"NetworkType,omitempty"`
	UserGroupId           string               `xml:"UserGroupId,omitempty"`
	KmsKeyId              string               `xml:"KmsKeyId,omitempty"`
	CreateTime            string               `xml:"CreateTime,omitempty"`
}

type xmlStringSet struct {
	Items []string `xml:"member"`
}

type xmlCacheUsageLimits struct {
	DataStorage   *xmlDataStorageLimit `xml:"DataStorage,omitempty"`
	ECPUPerSecond *xmlECPULimit        `xml:"ECPUPerSecond,omitempty"`
}

type xmlDataStorageLimit struct {
	Maximum int    `xml:"Maximum"`
	Unit    string `xml:"Unit"`
}

type xmlECPULimit struct {
	Maximum int `xml:"Maximum"`
}

func (h *Handler) CreateServerlessCache(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.FormValue("ServerlessCacheName"))
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ServerlessCacheName is required"))
		return
	}

	if _, aerr := h.store.getServerlessCache(r.Context(), name); aerr == nil {
		protocol.WriteQueryXMLError(w, r, errServerlessCacheAlreadyExists(name))
		return
	}

	engine := r.FormValue("Engine")
	if engine == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("Engine is required"))
		return
	}
	if engine != "redis" && engine != "memcached" && engine != "valkey" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("Engine must be redis, valkey, or memcached"))
		return
	}

	major := r.FormValue("MajorEngineVersion")
	fullVersion := engineDefaultVersion(engine)
	if major == "" {
		major = fullVersion
	}

	region := h.store.region(r.Context())
	arn := fmt.Sprintf("arn:aws:elasticache:%s:%s:serverlesscache:%s", region, h.cfg.AccountID, name)
	endpoint := &ClusterEndpoint{Address: fmt.Sprintf("%s.%s.serverless.%s", name, region, h.cfg.ExternalHostname()), Port: enginePort(engine)}

	cache := &ServerlessCache{
		ServerlessCacheName:   name,
		Description:           r.FormValue("Description"),
		Status:                "creating",
		Engine:                engine,
		MajorEngineVersion:    major,
		FullEngineVersion:     fullVersion,
		CacheUsageLimits:      formCacheUsageLimits(r),
		ARN:                   arn,
		CreateTime:            h.clk.Now().UTC().Format(time.RFC3339Nano),
		Endpoint:              endpoint,
		ReaderEndpoint:        endpoint,
		SubnetIds:             formStringList(r, "SubnetIds.SubnetId"),
		SecurityGroupIds:      formStringList(r, "SecurityGroupIds.SecurityGroupId"),
		SnapshotArnsToRestore: formStringList(r, "SnapshotArnsToRestore.SnapshotArn"),
		SnapshotRetention:     formInt(r, "SnapshotRetentionLimit", 0),
		DailySnapshotTime:     r.FormValue("DailySnapshotTime"),
		NetworkType:           r.FormValue("NetworkType"),
		UserGroupId:           r.FormValue("UserGroupId"),
		KmsKeyId:              r.FormValue("KmsKeyId"),
	}
	if cache.NetworkType == "" {
		cache.NetworkType = "ipv4"
	}

	if aerr := h.store.putServerlessCache(r.Context(), cache); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if tags := formTags(r); len(tags) > 0 {
		h.store.setTags(r.Context(), arn, tags) //nolint:errcheck
	}

	if h.dockerReady.Load() {
		if h.puller != nil {
			h.puller.Prewarm(engineImage(engine, fullVersion))
		}
		h.dockerWg.Add(1)
		go func(cacheName string) {
			defer h.dockerWg.Done()
			bgCtx := context.Background()
			got, aerr := h.store.getServerlessCache(bgCtx, cacheName)
			if aerr != nil || got == nil {
				return
			}
			if err := h.startServerlessCacheContainer(bgCtx, got); err != nil {
				h.log.Warn("failed to start Docker container for serverless cache — falling back to metadata-only",
					zap.String("cache", cacheName), zap.Error(err))
				h.serverlessFallbackAvailable(cacheName)
				return
			}
			if aerr := h.store.putServerlessCache(bgCtx, got); aerr != nil {
				h.log.Warn("ElastiCache: persist post-start serverless cache",
					zap.String("cache", cacheName), zap.String("error", aerr.Message))
				return
			}
			h.scheduleServerlessHealthCheck(cacheName, got.Endpoint.Address, got.Endpoint.Port)
		}(name)
	} else {
		h.scheduler.After(name+":serverless-available", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getServerlessCache(ctx, name)
			if aerr == nil && got.Status == "creating" {
				got.Status = "available"
				h.store.putServerlessCache(ctx, got) //nolint:errcheck
			}
		})
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateServerlessCacheResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlCreateServerlessCacheResult{ServerlessCache: toXMLServerlessCache(cache)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) DescribeServerlessCaches(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.FormValue("ServerlessCacheName"))
	if name != "" {
		cache, aerr := h.store.getServerlessCache(r.Context(), name)
		if aerr != nil {
			protocol.WriteQueryXMLError(w, r, aerr)
			return
		}
		protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeServerlessCachesResponse{
			Xmlns: cacheXMLNS,
			Result: xmlDescribeServerlessCachesResult{
				ServerlessCaches: xmlServerlessCaches{Items: []xmlServerlessCache{toXMLServerlessCache(cache)}},
			},
			ResponseMetadata: protocol.QueryResponseMetadata(r),
		})
		return
	}

	caches, aerr := h.store.listServerlessCaches(r.Context())
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	sort.Slice(caches, func(i, j int) bool {
		return caches[i].ServerlessCacheName < caches[j].ServerlessCacheName
	})
	start := 0
	if token := r.FormValue("NextToken"); token != "" {
		parsed, err := strconv.Atoi(token)
		if err != nil || parsed < 0 || parsed > len(caches) {
			protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("NextToken is invalid"))
			return
		}
		start = parsed
	}
	maxResults := formInt(r, "MaxResults", 50)
	if maxResults <= 0 {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("MaxResults must be greater than zero"))
		return
	}
	end := start + maxResults
	nextToken := ""
	if end < len(caches) {
		nextToken = strconv.Itoa(end)
	} else {
		end = len(caches)
	}
	items := make([]xmlServerlessCache, 0, end-start)
	for _, cache := range caches[start:end] {
		items = append(items, toXMLServerlessCache(cache))
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeServerlessCachesResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlDescribeServerlessCachesResult{NextToken: nextToken, ServerlessCaches: xmlServerlessCaches{Items: items}},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) ModifyServerlessCache(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.FormValue("ServerlessCacheName"))
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ServerlessCacheName is required"))
		return
	}
	cache, aerr := h.store.getServerlessCache(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if v := r.FormValue("Description"); v != "" {
		cache.Description = v
	}
	if v := r.FormValue("Engine"); v != "" {
		if v != "redis" && v != "memcached" && v != "valkey" {
			protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("Engine must be redis, valkey, or memcached"))
			return
		}
		cache.Engine = v
		cache.FullEngineVersion = engineDefaultVersion(v)
	}
	if v := r.FormValue("MajorEngineVersion"); v != "" {
		cache.MajorEngineVersion = v
	}
	if r.FormValue("CacheUsageLimits.DataStorage.Maximum") != "" || r.FormValue("CacheUsageLimits.DataStorage.Unit") != "" || r.FormValue("CacheUsageLimits.ECPUPerSecond.Maximum") != "" {
		cache.CacheUsageLimits = formCacheUsageLimits(r)
	}
	if r.FormValue("SecurityGroupIds.SecurityGroupId.1") != "" {
		cache.SecurityGroupIds = formStringList(r, "SecurityGroupIds.SecurityGroupId")
	}
	if v := r.FormValue("SnapshotRetentionLimit"); v != "" {
		cache.SnapshotRetention = formInt(r, "SnapshotRetentionLimit", cache.SnapshotRetention)
	}
	if v := r.FormValue("DailySnapshotTime"); v != "" {
		cache.DailySnapshotTime = v
	}
	if v := r.FormValue("UserGroupId"); v != "" {
		cache.UserGroupId = v
	}
	if strings.EqualFold(r.FormValue("RemoveUserGroup"), "true") {
		cache.UserGroupId = ""
	}
	cache.Status = "modifying"
	if aerr := h.store.putServerlessCache(r.Context(), cache); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	h.scheduler.After(name+":serverless-available", 0, func() {
		ctx := context.Background()
		got, aerr := h.store.getServerlessCache(ctx, name)
		if aerr == nil && got.Status == "modifying" {
			got.Status = "available"
			h.store.putServerlessCache(ctx, got) //nolint:errcheck
		}
	})
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifyServerlessCacheResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlModifyServerlessCacheResult{ServerlessCache: toXMLServerlessCache(cache)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) DeleteServerlessCache(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.FormValue("ServerlessCacheName"))
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ServerlessCacheName is required"))
		return
	}
	cache, aerr := h.store.getServerlessCache(r.Context(), name)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	containerID := cache.DockerContainerID
	hostPort := cache.HostPort
	cache.Status = "deleting"
	if aerr := h.store.putServerlessCache(r.Context(), cache); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteServerlessCacheResponse{
		Xmlns:            cacheXMLNS,
		Result:           xmlDeleteServerlessCacheResult{ServerlessCache: toXMLServerlessCache(cache)},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})

	h.scheduler.Cancel(name + ":serverless-health")
	if h.gc != nil && containerID != "" {
		h.gc.StopNow(containerID)
		h.gc.ScheduleRemove(containerID)
	}
	if hostPort > 0 {
		_ = h.store.releasePort(r.Context(), hostPort) //nolint:errcheck
	}
	h.scheduler.After(name+":serverless-delete", 50*time.Millisecond, func() {
		ctx := context.Background()
		if aerr := h.store.deleteServerlessCache(ctx, name); aerr != nil {
			h.log.Warn("failed to delete serverless cache record", zap.String("cache", name), zap.Error(aerr))
		}
	})
}

func (h *Handler) startServerlessCacheContainer(ctx context.Context, c *ServerlessCache) error {
	image := engineImage(c.Engine, c.FullEngineVersion)
	port := enginePort(c.Engine)
	containerName := "overcast-elasticache-serverless-" + c.ServerlessCacheName
	containerPort := fmt.Sprintf("%d/tcp", port)
	resourceLabel := "serverless:" + c.ServerlessCacheName

	if existing, err := h.docker.GetContainerByName(ctx, containerName); err == nil && existing != nil {
		if !existing.HasOvercastLabels(serviceName, resourceLabel) {
			return fmt.Errorf("container %q exists but is not an overcast-managed serverless cache container — refusing to reuse", containerName)
		}
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
		c.DockerContainerID = existing.ID
		c.HostPort = hostPort
		h.setServerlessEndpoint(ctx, c)
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
		h.log.Warn("ElastiCache: failed to create network (may already exist)", zap.String("network", network), zap.Error(err))
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
		NetworkingConfig: &docker.NetworkingConfig{EndpointsConfig: map[string]*docker.EndpointSettings{network: {}}},
	}
	containerID, err := h.docker.CreateContainer(ctx, containerName, req)
	if err != nil {
		if docker.IsConflict(err) {
			h.store.releasePort(ctx, hostPort) //nolint:errcheck
			return h.startServerlessCacheContainer(ctx, c)
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
	h.setServerlessEndpoint(ctx, c)
	return nil
}

func (h *Handler) setServerlessEndpoint(ctx context.Context, c *ServerlessCache) {
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
				endpoint := &ClusterEndpoint{Address: ep.IPAddress, Port: port}
				c.Endpoint = endpoint
				c.ReaderEndpoint = endpoint
				return
			}
		}
	}
	endpoint := &ClusterEndpoint{Address: "127.0.0.1", Port: c.HostPort}
	c.Endpoint = endpoint
	c.ReaderEndpoint = endpoint
}

func (h *Handler) scheduleServerlessHealthCheck(name, host string, port int) {
	const maxRetries = 30
	var attempt int
	var check func()
	check = func() {
		attempt++
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
		if err == nil {
			conn.Close()
			ctx := context.Background()
			got, aerr := h.store.getServerlessCache(ctx, name)
			if aerr == nil && (got.Status == "creating" || got.Status == "starting") {
				got.Status = "available"
				h.store.putServerlessCache(ctx, got) //nolint:errcheck
			}
			return
		}
		if attempt < maxRetries {
			h.scheduler.After(name+":serverless-health", 2*time.Second, check)
			return
		}
		h.log.Warn("ElastiCache serverless health check timed out", zap.String("cache", name), zap.Int("attempts", attempt))
		h.serverlessFallbackAvailable(name)
	}
	h.scheduler.After(name+":serverless-health", 1*time.Second, check)
}

func (h *Handler) serverlessFallbackAvailable(name string) {
	ctx := context.Background()
	got, aerr := h.store.getServerlessCache(ctx, name)
	if aerr == nil && (got.Status == "creating" || got.Status == "starting") {
		got.Status = "available"
		h.store.putServerlessCache(ctx, got) //nolint:errcheck
	}
}

func toXMLServerlessCache(c *ServerlessCache) xmlServerlessCache {
	out := xmlServerlessCache{
		ServerlessCacheName: c.ServerlessCacheName,
		Description:         c.Description,
		Status:              c.Status,
		Engine:              c.Engine,
		MajorEngineVersion:  c.MajorEngineVersion,
		FullEngineVersion:   c.FullEngineVersion,
		CacheUsageLimits:    toXMLCacheUsageLimits(c.CacheUsageLimits),
		ARN:                 c.ARN,
		SnapshotRetention:   c.SnapshotRetention,
		DailySnapshotTime:   c.DailySnapshotTime,
		NetworkType:         c.NetworkType,
		UserGroupId:         c.UserGroupId,
		KmsKeyId:            c.KmsKeyId,
		CreateTime:          c.CreateTime,
	}
	if len(c.SubnetIds) > 0 {
		out.SubnetIds = xmlStringSet{Items: c.SubnetIds}
	}
	if len(c.SecurityGroupIds) > 0 {
		out.SecurityGroupIds = xmlStringSet{Items: c.SecurityGroupIds}
	}
	if len(c.SnapshotArnsToRestore) > 0 {
		out.SnapshotArnsToRestore = xmlStringSet{Items: c.SnapshotArnsToRestore}
	}
	if c.Endpoint != nil {
		out.Endpoint = &xmlEndpoint{Address: c.Endpoint.Address, Port: c.Endpoint.Port}
	}
	if c.ReaderEndpoint != nil {
		out.ReaderEndpoint = &xmlEndpoint{Address: c.ReaderEndpoint.Address, Port: c.ReaderEndpoint.Port}
	}
	return out
}

func toXMLCacheUsageLimits(limits CacheUsageLimits) *xmlCacheUsageLimits {
	out := &xmlCacheUsageLimits{}
	if limits.DataStorage.Maximum > 0 || limits.DataStorage.Unit != "" {
		out.DataStorage = &xmlDataStorageLimit{Maximum: limits.DataStorage.Maximum, Unit: limits.DataStorage.Unit}
	}
	if limits.ECPUPerSecond.Maximum > 0 {
		out.ECPUPerSecond = &xmlECPULimit{Maximum: limits.ECPUPerSecond.Maximum}
	}
	if out.DataStorage == nil && out.ECPUPerSecond == nil {
		return nil
	}
	return out
}

func formStringList(r *http.Request, prefix string) []string {
	out := []string{}
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("%s.%d", prefix, i))
		if v == "" {
			break
		}
		out = append(out, v)
	}
	return out
}

func formTags(r *http.Request) map[string]string {
	tags := map[string]string{}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.Tag.%d.Key", i))
		if key == "" {
			break
		}
		tags[key] = r.FormValue(fmt.Sprintf("Tags.Tag.%d.Value", i))
	}
	return tags
}

func formCacheUsageLimits(r *http.Request) CacheUsageLimits {
	return CacheUsageLimits{
		DataStorage: DataStorageLimit{
			Maximum: formInt(r, "CacheUsageLimits.DataStorage.Maximum", 0),
			Unit:    r.FormValue("CacheUsageLimits.DataStorage.Unit"),
		},
		ECPUPerSecond: ECPULimit{
			Maximum: formInt(r, "CacheUsageLimits.ECPUPerSecond.Maximum", 0),
		},
	}
}

package elasticache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsClusters        = "elasticache:clusters"
	nsReplication     = "elasticache:replication-groups"
	nsServerless      = "elasticache:serverless-caches"
	nsSubnetGroups    = "elasticache:subnet-groups"
	nsParameterGroups = "elasticache:parameter-groups"
	nsTags            = "elasticache:tags"
	nsPorts           = "elasticache:ports"
)

// CacheCluster represents a stored ElastiCache cache cluster.
type CacheCluster struct {
	CacheClusterId            string           `json:"CacheClusterId"`
	CacheClusterStatus        string           `json:"CacheClusterStatus"`
	CacheNodeType             string           `json:"CacheNodeType"`
	Engine                    string           `json:"Engine"`
	EngineVersion             string           `json:"EngineVersion"`
	NumCacheNodes             int              `json:"NumCacheNodes"`
	PreferredAvailabilityZone string           `json:"PreferredAvailabilityZone,omitempty"`
	CacheSubnetGroupName      string           `json:"CacheSubnetGroupName,omitempty"`
	ReplicationGroupId        string           `json:"ReplicationGroupId,omitempty"`
	CacheParameterGroupName   string           `json:"CacheParameterGroupName,omitempty"`
	ARN                       string           `json:"ARN"`
	ConfigurationEndpoint     *ClusterEndpoint `json:"ConfigurationEndpoint,omitempty"`
	// Docker fields — internal only, not returned in API responses.
	DockerContainerID string `json:"DockerContainerID,omitempty"`
	HostPort          int    `json:"HostPort,omitempty"`
}

// ClusterEndpoint is the endpoint for a cache cluster or replication group.
type ClusterEndpoint struct {
	Address string `json:"Address"`
	Port    int    `json:"Port"`
}

// ReplicationGroup represents a stored ElastiCache replication group.
type ReplicationGroup struct {
	ReplicationGroupId     string           `json:"ReplicationGroupId"`
	Description            string           `json:"Description"`
	Status                 string           `json:"Status"`
	ARN                    string           `json:"ARN"`
	AutomaticFailover      string           `json:"AutomaticFailover"`
	MultiAZ                string           `json:"MultiAZ"`
	CacheNodeType          string           `json:"CacheNodeType"`
	Engine                 string           `json:"Engine"`
	EngineVersion          string           `json:"EngineVersion"`
	SnapshotRetentionLimit int              `json:"SnapshotRetentionLimit"`
	MemberClusters         []string         `json:"MemberClusters,omitempty"`
	ConfigurationEndpoint  *ClusterEndpoint `json:"ConfigurationEndpoint,omitempty"`
	// Docker fields — internal only, not returned in API responses.
	DockerContainerID string `json:"DockerContainerID,omitempty"`
	HostPort          int    `json:"HostPort,omitempty"`
}

// ServerlessCache represents a stored ElastiCache serverless cache.
type ServerlessCache struct {
	ServerlessCacheName   string           `json:"ServerlessCacheName"`
	Description           string           `json:"Description"`
	Status                string           `json:"Status"`
	Engine                string           `json:"Engine"`
	MajorEngineVersion    string           `json:"MajorEngineVersion"`
	FullEngineVersion     string           `json:"FullEngineVersion"`
	CacheUsageLimits      CacheUsageLimits `json:"CacheUsageLimits,omitempty"`
	ARN                   string           `json:"ARN"`
	CreateTime            string           `json:"CreateTime"`
	Endpoint              *ClusterEndpoint `json:"Endpoint,omitempty"`
	ReaderEndpoint        *ClusterEndpoint `json:"ReaderEndpoint,omitempty"`
	SubnetIds             []string         `json:"SubnetIds,omitempty"`
	SecurityGroupIds      []string         `json:"SecurityGroupIds,omitempty"`
	SnapshotArnsToRestore []string         `json:"SnapshotArnsToRestore,omitempty"`
	SnapshotRetention     int              `json:"SnapshotRetentionLimit"`
	DailySnapshotTime     string           `json:"DailySnapshotTime,omitempty"`
	NetworkType           string           `json:"NetworkType,omitempty"`
	UserGroupId           string           `json:"UserGroupId,omitempty"`
	KmsKeyId              string           `json:"KmsKeyId,omitempty"`
	DockerContainerID     string           `json:"DockerContainerID,omitempty"`
	HostPort              int              `json:"HostPort,omitempty"`
}

type CacheUsageLimits struct {
	DataStorage   DataStorageLimit `json:"DataStorage,omitempty"`
	ECPUPerSecond ECPULimit        `json:"ECPUPerSecond,omitempty"`
}

type DataStorageLimit struct {
	Maximum int    `json:"Maximum,omitempty"`
	Unit    string `json:"Unit,omitempty"`
}

type ECPULimit struct {
	Maximum int `json:"Maximum,omitempty"`
}

// CacheParameterGroup represents a stored ElastiCache cache parameter group.
type CacheParameterGroup struct {
	CacheParameterGroupName   string `json:"CacheParameterGroupName"`
	CacheParameterGroupFamily string `json:"CacheParameterGroupFamily"`
	Description               string `json:"Description"`
	ARN                       string `json:"ARN"`
}

// CacheSubnetGroup represents a stored ElastiCache cache subnet group.
type CacheSubnetGroup struct {
	CacheSubnetGroupName        string   `json:"CacheSubnetGroupName"`
	CacheSubnetGroupDescription string   `json:"CacheSubnetGroupDescription"`
	ARN                         string   `json:"ARN"`
	VpcId                       string   `json:"VpcId"`
	SubnetIds                   []string `json:"SubnetIds"`
}

type cacheStore struct {
	mu            sync.Mutex
	store         state.Store
	defaultRegion string
}

func newCacheStore(store state.Store, defaultRegion string) *cacheStore {
	return &cacheStore{store: store, defaultRegion: defaultRegion}
}

func (s *cacheStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// ── CacheCluster store ────────────────────────────────────────────────────────

func (s *cacheStore) putCacheCluster(ctx context.Context, c *CacheCluster) *protocol.AWSError {
	raw, err := json.Marshal(c)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), c.CacheClusterId), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) getCacheCluster(ctx context.Context, id string) (*CacheCluster, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, errClusterNotFound(id)
	}
	var c CacheCluster
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &c, nil
}

func (s *cacheStore) deleteCacheCluster(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) listCacheClusters(ctx context.Context) ([]*CacheCluster, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsClusters, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	clusters := make([]*CacheCluster, 0, len(pairs))
	for _, p := range pairs {
		var c CacheCluster
		if err := json.Unmarshal([]byte(p.Value), &c); err != nil {
			continue
		}
		clusters = append(clusters, &c)
	}
	return clusters, nil
}

// ── ReplicationGroup store ────────────────────────────────────────────────────

func (s *cacheStore) putReplicationGroup(ctx context.Context, rg *ReplicationGroup) *protocol.AWSError {
	raw, err := json.Marshal(rg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsReplication, serviceutil.RegionKey(s.region(ctx), rg.ReplicationGroupId), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) getReplicationGroup(ctx context.Context, id string) (*ReplicationGroup, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsReplication, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil || !ok {
		return nil, errReplicationGroupNotFound(id)
	}
	var rg ReplicationGroup
	if err := json.Unmarshal([]byte(raw), &rg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &rg, nil
}

func (s *cacheStore) deleteReplicationGroup(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsReplication, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) listReplicationGroups(ctx context.Context) ([]*ReplicationGroup, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsReplication, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	groups := make([]*ReplicationGroup, 0, len(pairs))
	for _, p := range pairs {
		var rg ReplicationGroup
		if err := json.Unmarshal([]byte(p.Value), &rg); err != nil {
			continue
		}
		groups = append(groups, &rg)
	}
	return groups, nil
}

// ── ServerlessCache store ────────────────────────────────────────────────────

func (s *cacheStore) putServerlessCache(ctx context.Context, c *ServerlessCache) *protocol.AWSError {
	raw, err := json.Marshal(c)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsServerless, serviceutil.RegionKey(s.region(ctx), c.ServerlessCacheName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) getServerlessCache(ctx context.Context, name string) (*ServerlessCache, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsServerless, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil || !ok {
		return nil, errServerlessCacheNotFound(name)
	}
	var c ServerlessCache
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &c, nil
}

func (s *cacheStore) deleteServerlessCache(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsServerless, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) listServerlessCaches(ctx context.Context) ([]*ServerlessCache, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsServerless, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	caches := make([]*ServerlessCache, 0, len(pairs))
	for _, p := range pairs {
		var c ServerlessCache
		if err := json.Unmarshal([]byte(p.Value), &c); err != nil {
			continue
		}
		caches = append(caches, &c)
	}
	return caches, nil
}

// ── CacheSubnetGroup store ────────────────────────────────────────────────────

func (s *cacheStore) putCacheSubnetGroup(ctx context.Context, sg *CacheSubnetGroup) *protocol.AWSError {
	raw, err := json.Marshal(sg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsSubnetGroups, serviceutil.RegionKey(s.region(ctx), sg.CacheSubnetGroupName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) getCacheSubnetGroup(ctx context.Context, name string) (*CacheSubnetGroup, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsSubnetGroups, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil || !ok {
		return nil, errSubnetGroupNotFound(name)
	}
	var sg CacheSubnetGroup
	if err := json.Unmarshal([]byte(raw), &sg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &sg, nil
}

func (s *cacheStore) deleteCacheSubnetGroup(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsSubnetGroups, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) listCacheSubnetGroups(ctx context.Context) ([]*CacheSubnetGroup, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsSubnetGroups, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	groups := make([]*CacheSubnetGroup, 0, len(pairs))
	for _, p := range pairs {
		var sg CacheSubnetGroup
		if err := json.Unmarshal([]byte(p.Value), &sg); err != nil {
			continue
		}
		groups = append(groups, &sg)
	}
	return groups, nil
}

// ── CacheParameterGroup store ─────────────────────────────────────────────────

func (s *cacheStore) putCacheParameterGroup(ctx context.Context, pg *CacheParameterGroup) *protocol.AWSError {
	raw, err := json.Marshal(pg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsParameterGroups, serviceutil.RegionKey(s.region(ctx), pg.CacheParameterGroupName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) getCacheParameterGroup(ctx context.Context, name string) (*CacheParameterGroup, *protocol.AWSError) {
	raw, ok, err := s.store.Get(ctx, nsParameterGroups, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil || !ok {
		return nil, errParameterGroupNotFound(name)
	}
	var pg CacheParameterGroup
	if err := json.Unmarshal([]byte(raw), &pg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &pg, nil
}

func (s *cacheStore) deleteCacheParameterGroup(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsParameterGroups, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) listCacheParameterGroups(ctx context.Context) ([]*CacheParameterGroup, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsParameterGroups, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	groups := make([]*CacheParameterGroup, 0, len(pairs))
	for _, p := range pairs {
		var pg CacheParameterGroup
		if err := json.Unmarshal([]byte(p.Value), &pg); err != nil {
			continue
		}
		groups = append(groups, &pg)
	}
	return groups, nil
}

// ── Tags store ────────────────────────────────────────────────────────────────

func (s *cacheStore) getTags(ctx context.Context, arn string) (map[string]string, *protocol.AWSError) {
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

func (s *cacheStore) setTags(ctx context.Context, arn string, tags map[string]string) *protocol.AWSError {
	raw, err := json.Marshal(tags)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsTags, arn, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ── Port allocation ───────────────────────────────────────────────────────────

// allocatePort finds and reserves the first free port in [portBase, portBase+1000).
// Protected by a mutex to prevent concurrent callers from claiming the same port.
func (s *cacheStore) allocatePort(ctx context.Context, clusterID string, portBase int) (int, *protocol.AWSError) {
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
			if err := s.store.Set(ctx, nsPorts, strconv.Itoa(port), clusterID); err != nil {
				return 0, protocol.Wrap(protocol.ErrInternalError, err)
			}
			return port, nil
		}
	}
	return 0, &protocol.AWSError{
		Code:       "InternalFailure",
		Message:    "no free port available for ElastiCache cluster",
		HTTPStatus: http.StatusInternalServerError,
	}
}

func (s *cacheStore) releasePort(ctx context.Context, port int) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsPorts, strconv.Itoa(port)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *cacheStore) allocatePortFixed(ctx context.Context, clusterID string, port int) *protocol.AWSError {
	if err := s.store.Set(ctx, nsPorts, strconv.Itoa(port), clusterID); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ── Errors ────────────────────────────────────────────────────────────────────

func errClusterNotFound(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "CacheClusterNotFound",
		Message:    fmt.Sprintf("Cache cluster %s not found.", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errClusterAlreadyExists(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "CacheClusterAlreadyExists",
		Message:    fmt.Sprintf("Cache cluster %s already exists.", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errReplicationGroupNotFound(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ReplicationGroupNotFoundFault",
		Message:    fmt.Sprintf("Replication group %s not found.", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errReplicationGroupAlreadyExists(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ReplicationGroupAlreadyExistsFault",
		Message:    fmt.Sprintf("Replication group %s already exists.", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errServerlessCacheNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ServerlessCacheNotFoundFault",
		Message:    fmt.Sprintf("Serverless cache %s not found.", name),
		HTTPStatus: http.StatusNotFound,
	}
}

func errServerlessCacheAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ServerlessCacheAlreadyExistsFault",
		Message:    fmt.Sprintf("Serverless cache %s already exists.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errSubnetGroupNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "CacheSubnetGroupNotFoundFault",
		Message:    fmt.Sprintf("Cache subnet group '%s' not found.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errSubnetGroupAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "CacheSubnetGroupAlreadyExists",
		Message:    fmt.Sprintf("Cache subnet group '%s' already exists.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errParameterGroupNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "CacheParameterGroupNotFound",
		Message:    fmt.Sprintf("Cache parameter group %s not found.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errParameterGroupAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "CacheParameterGroupAlreadyExists",
		Message:    fmt.Sprintf("Cache parameter group %s already exists.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errInvalidParameterValue(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterValue",
		Message:    msg,
		HTTPStatus: http.StatusBadRequest,
	}
}

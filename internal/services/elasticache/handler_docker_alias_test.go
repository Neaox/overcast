package elasticache

import (
	"slices"
	"testing"

	"github.com/Neaox/overcast/internal/config"
)

func TestEndpointAliases_serverlessCache(t *testing.T) {
	// Given: a serverless cache with its synthetic endpoint hostname.
	h := &Handler{cfg: &config.Config{Region: "ap-southeast-2", Hostname: "localhost"}}
	cache := &ServerlessCache{
		ServerlessCacheName: "debug-cache",
		Endpoint:            &ClusterEndpoint{Address: "debug-cache.ap-southeast-2.serverless.localhost", Port: 6379},
		ReaderEndpoint:      &ClusterEndpoint{Address: "debug-cache.ap-southeast-2.serverless.localhost", Port: 6379},
	}

	// When: aliases are built for Docker DNS.
	got := h.serverlessEndpointAliases(cache)

	// Then: Docker receives the exact endpoint hostname as an alias.
	want := []string{"debug-cache.ap-southeast-2.serverless.localhost"}
	if !slices.Equal(got, want) {
		t.Fatalf("aliases = %#v, want %#v", got, want)
	}
}

func TestEndpointAliases_cacheClusterAndReplicationGroup(t *testing.T) {
	// Given: cache cluster and replication group endpoints with synthetic hostnames.
	h := &Handler{cfg: &config.Config{Region: "us-east-1", Hostname: "localhost"}}
	cluster := &CacheCluster{CacheClusterId: "cluster-1", ConfigurationEndpoint: &ClusterEndpoint{Address: "cluster-1.us-east-1.cfg.localhost", Port: 6379}}
	rg := &ReplicationGroup{ReplicationGroupId: "rg-1", ConfigurationEndpoint: &ClusterEndpoint{Address: "rg-1.us-east-1.ng.cfg.localhost", Port: 6379}}

	// When: aliases are built for Docker DNS.
	clusterAliases := h.clusterEndpointAliases(cluster)
	rgAliases := h.replicationGroupEndpointAliases(rg)

	// Then: each resource advertises its configured endpoint hostname.
	if want := []string{"cluster-1.us-east-1.cfg.localhost"}; !slices.Equal(clusterAliases, want) {
		t.Fatalf("cluster aliases = %#v, want %#v", clusterAliases, want)
	}
	if want := []string{"rg-1.us-east-1.ng.cfg.localhost"}; !slices.Equal(rgAliases, want) {
		t.Fatalf("replication group aliases = %#v, want %#v", rgAliases, want)
	}
}

func TestEndpointAliases_preserveCanonicalNameWhenEndpointIsIP(t *testing.T) {
	// Given: Docker startup has already rewritten the stored endpoint to a direct container IP.
	h := &Handler{cfg: &config.Config{Region: "ap-southeast-2", Hostname: "localhost"}}
	cache := &ServerlessCache{
		ServerlessCacheName: "debug-cache",
		Endpoint:            &ClusterEndpoint{Address: "172.18.0.3", Port: 6379},
		ReaderEndpoint:      &ClusterEndpoint{Address: "172.18.0.3", Port: 6379},
	}

	// When: aliases are built for Docker DNS.
	got := h.serverlessEndpointAliases(cache)

	// Then: the originally advertised synthetic endpoint is still registered.
	want := []string{"debug-cache.ap-southeast-2.serverless.localhost"}
	if !slices.Equal(got, want) {
		t.Fatalf("aliases = %#v, want %#v", got, want)
	}
}

package elasticache

import (
	"slices"
	"testing"

	"github.com/Neaox/overcast/internal/config"
)

// An endpoint address is minted on the hostname the *calling client* reached
// Overcast on, so the same node is handed out under several names and whichever
// process resolves one later is often not the one that received it. Docker
// aliases are exact-match, so every base has to be registered — see #872, where
// registering only the configured name left an ECS task unable to reach a cache
// node, hanging rather than failing.
//
// The expected sets below are therefore the full base list, not one name.

// aliasBases is the hostname set a resource can be minted under with the
// default configuration: the three split-horizon domains plus plain localhost.
func want(prefix string, bases ...string) []string {
	if len(bases) == 0 {
		bases = []string{
			"localhost.overcast.sh",
			"localhost.localstack.cloud",
			"localhost.floci.io",
			"localhost",
		}
	}
	out := make([]string, 0, len(bases))
	for _, base := range bases {
		out = append(out, prefix+base)
	}
	return out
}

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

	// Then: the name is registered under every base it could be minted with,
	// and the already-advertised address does not appear twice.
	if expected := want("debug-cache.ap-southeast-2.serverless."); !slices.Equal(got, expected) {
		t.Fatalf("aliases = %#v, want %#v", got, expected)
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

	// Then: each resource advertises its endpoint hostname on every base.
	if expected := want("cluster-1.us-east-1.cfg."); !slices.Equal(clusterAliases, expected) {
		t.Fatalf("cluster aliases = %#v, want %#v", clusterAliases, expected)
	}
	if expected := want("rg-1.us-east-1.ng.cfg."); !slices.Equal(rgAliases, expected) {
		t.Fatalf("replication group aliases = %#v, want %#v", rgAliases, expected)
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

	// Then: the synthetic names are still registered, and the IP is dropped —
	// an address is not usable as a Docker alias.
	if expected := want("debug-cache.ap-southeast-2.serverless."); !slices.Equal(got, expected) {
		t.Fatalf("aliases = %#v, want %#v", got, expected)
	}
}

// A cache minted under a hostname the configuration no longer lists keeps
// answering to it, so a task holding the old name is not stranded by a config
// change.
func TestEndpointAliases_keepsAnAdvertisedNameOutsideTheConfiguredBases(t *testing.T) {
	// Given: a stored endpoint on a base the current configuration does not list.
	h := &Handler{cfg: &config.Config{Region: "us-east-1", Hostname: "localhost"}}
	cluster := &CacheCluster{
		CacheClusterId:        "cluster-1",
		ConfigurationEndpoint: &ClusterEndpoint{Address: "cluster-1.us-east-1.cfg.retired.example", Port: 6379},
	}

	// When: aliases are built.
	got := h.clusterEndpointAliases(cluster)

	// Then: the retired name is carried alongside the current set.
	if !slices.Contains(got, "cluster-1.us-east-1.cfg.retired.example") {
		t.Fatalf("aliases = %#v, want the advertised name retained", got)
	}
	for _, expected := range want("cluster-1.us-east-1.cfg.") {
		if !slices.Contains(got, expected) {
			t.Fatalf("aliases = %#v, missing %q", got, expected)
		}
	}
}

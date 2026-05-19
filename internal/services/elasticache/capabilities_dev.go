//go:build dev

package elasticache

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "elasticache", Operation: "AddTagsToResource", Category: "General", Status: capabilities.StatusSupported, Notes: "ARN-scoped tag storage"},
		capabilities.Capability{Service: "elasticache", Operation: "CreateCacheCluster", Category: "General", Status: capabilities.StatusSupported, Notes: "Docker-backed (redis/valkey/memcached); async creating→available via TCP health check; port auto-alloc"},
		capabilities.Capability{Service: "elasticache", Operation: "CreateCacheParameterGroup", Category: "General", Status: capabilities.StatusSupported, Notes: "Stores name, family, description, and ARN"},
		capabilities.Capability{Service: "elasticache", Operation: "CreateCacheSubnetGroup", Category: "General", Status: capabilities.StatusSupported, Notes: "Stores name, description, and subnet IDs"},
		capabilities.Capability{Service: "elasticache", Operation: "CreateReplicationGroup", Category: "General", Status: capabilities.StatusSupported, Notes: "Docker-backed (single primary node); async creating→available via TCP health check"},
		capabilities.Capability{Service: "elasticache", Operation: "DeleteCacheCluster", Category: "General", Status: capabilities.StatusSupported, Notes: "Sets status to \"deleting\"; stops and removes Docker container asynchronously"},
		capabilities.Capability{Service: "elasticache", Operation: "DeleteCacheParameterGroup", Category: "General", Status: capabilities.StatusSupported, Notes: "Removes stored parameter group"},
		capabilities.Capability{Service: "elasticache", Operation: "DeleteCacheSubnetGroup", Category: "General", Status: capabilities.StatusSupported, Notes: "Removes stored subnet group"},
		capabilities.Capability{Service: "elasticache", Operation: "DeleteReplicationGroup", Category: "General", Status: capabilities.StatusSupported, Notes: "Sets status to \"deleting\"; stops and removes Docker container asynchronously"},
		capabilities.Capability{Service: "elasticache", Operation: "DescribeCacheClusters", Category: "General", Status: capabilities.StatusSupported, Notes: "List all or filter by CacheClusterId"},
		capabilities.Capability{Service: "elasticache", Operation: "DescribeCacheEngineVersions", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "elasticache", Operation: "DescribeCacheParameterGroups", Category: "General", Status: capabilities.StatusSupported, Notes: "List all or filter by name"},
		capabilities.Capability{Service: "elasticache", Operation: "DescribeCacheParameters", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns curated static parameters for the group's family; supports Source filter and MaxRecords/Marker pagination"},
		capabilities.Capability{Service: "elasticache", Operation: "DescribeCacheSubnetGroups", Category: "General", Status: capabilities.StatusSupported, Notes: "List all or filter by name"},
		capabilities.Capability{Service: "elasticache", Operation: "DescribeReplicationGroups", Category: "General", Status: capabilities.StatusSupported, Notes: "List all or filter by ReplicationGroupId"},
		capabilities.Capability{Service: "elasticache", Operation: "ListTagsForResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Returns all tags for an ARN"},
		capabilities.Capability{Service: "elasticache", Operation: "ModifyCacheCluster", Category: "General", Status: capabilities.StatusSupported, Notes: "Metadata-only; updates nodeType, engineVersion, numNodes, parameterGroup; modifying→available"},
		capabilities.Capability{Service: "elasticache", Operation: "ModifyReplicationGroup", Category: "General", Status: capabilities.StatusSupported, Notes: "Metadata-only; updates description, nodeType, failover, multiAZ; modifying→available"},
		capabilities.Capability{Service: "elasticache", Operation: "RebootCacheCluster", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "elasticache", Operation: "RemoveTagsFromResource", Category: "General", Status: capabilities.StatusSupported, Notes: "Removes specific tag keys"},
	)
}

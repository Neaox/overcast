# ElastiCache — Managed In-Memory Cache

> AWS docs: https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/Welcome.html

ElastiCache uses the AWS Query protocol (form-encoded POST, XML responses). Operations are
identified by the `Action` parameter with API version `2015-02-02`.

When Docker is available, `CreateCacheCluster` and `CreateReplicationGroup` start real
containers with automatic port allocation from `ELASTICACHE_PORT_BASE` (default 63790).
A TCP health check polls until the port is reachable before transitioning to "available".
When Docker is unavailable, operations are metadata-only and status transitions immediately.

Supported engines: **redis** (`redis:6`, `redis:7`), **valkey** (`valkey/valkey:7`, `valkey/valkey:8`),
**memcached** (`memcached:1.5`, `memcached:1.6`).

> [!NOTE]
> Replication groups start a single primary container only — no multi-node replication is
> wired up between replicas.

---

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 20           |

---

## Endpoints

### General

| Operation                      | Status       | Notes                                                                                                             | AWS Docs                                                                                                        |
| ------------------------------ | ------------ | ----------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `AddTagsToResource`            | ✅ Supported | ARN-scoped tag storage                                                                                            | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_AddTagsToResource.html)            |
| `CreateCacheCluster`           | ✅ Supported | Docker-backed (redis/valkey/memcached); async creating→available via TCP health check; port auto-alloc            | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateCacheCluster.html)           |
| `CreateCacheParameterGroup`    | ✅ Supported | Stores name, family, description, and ARN                                                                         | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateCacheParameterGroup.html)    |
| `CreateCacheSubnetGroup`       | ✅ Supported | Stores name, description, and subnet IDs                                                                          | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateCacheSubnetGroup.html)       |
| `CreateReplicationGroup`       | ✅ Supported | Docker-backed (single primary node); async creating→available via TCP health check                                | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateReplicationGroup.html)       |
| `DeleteCacheCluster`           | ✅ Supported | Sets status to "deleting"; stops and removes Docker container asynchronously                                      | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteCacheCluster.html)           |
| `DeleteCacheParameterGroup`    | ✅ Supported | Removes stored parameter group                                                                                    | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteCacheParameterGroup.html)    |
| `DeleteCacheSubnetGroup`       | ✅ Supported | Removes stored subnet group                                                                                       | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteCacheSubnetGroup.html)       |
| `DeleteReplicationGroup`       | ✅ Supported | Sets status to "deleting"; stops and removes Docker container asynchronously                                      | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteReplicationGroup.html)       |
| `DescribeCacheClusters`        | ✅ Supported | List all or filter by CacheClusterId                                                                              | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheClusters.html)        |
| `DescribeCacheEngineVersions`  | ✅ Supported |                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheEngineVersions.html)  |
| `DescribeCacheParameterGroups` | ✅ Supported | List all or filter by name                                                                                        | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheParameterGroups.html) |
| `DescribeCacheParameters`      | ✅ Supported | Returns curated static parameters for the group's family; supports Source filter and MaxRecords/Marker pagination | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheParameters.html)      |
| `DescribeCacheSubnetGroups`    | ✅ Supported | List all or filter by name                                                                                        | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheSubnetGroups.html)    |
| `DescribeReplicationGroups`    | ✅ Supported | List all or filter by ReplicationGroupId                                                                          | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeReplicationGroups.html)    |
| `ListTagsForResource`          | ✅ Supported | Returns all tags for an ARN                                                                                       | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_ListTagsForResource.html)          |
| `ModifyCacheCluster`           | ✅ Supported | Metadata-only; updates nodeType, engineVersion, numNodes, parameterGroup; modifying→available                     | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_ModifyCacheCluster.html)           |
| `ModifyReplicationGroup`       | ✅ Supported | Metadata-only; updates description, nodeType, failover, multiAZ; modifying→available                              | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_ModifyReplicationGroup.html)       |
| `RebootCacheCluster`           | ✅ Supported |                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_RebootCacheCluster.html)           |
| `RemoveTagsFromResource`       | ✅ Supported | Removes specific tag keys                                                                                         | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_RemoveTagsFromResource.html)       |

<!-- END overcast:capabilities -->

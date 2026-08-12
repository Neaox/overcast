---
title: "ElastiCache — Managed In-Memory Cache"
description: "ElastiCache uses the AWS Query protocol (form-encoded POST, XML responses). Operations are identified by the Action parameter with API version 2015-02-02."
section: "Service Reference"
tags:
  - cache
  - docs
  - elasticache
  - managed
  - memory
  - services
---

# ElastiCache — Managed In-Memory Cache

> AWS docs: https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/Welcome.html

ElastiCache uses the AWS Query protocol (form-encoded POST, XML responses). Operations are
identified by the `Action` parameter with API version `2015-02-02`.

When Docker is available, `CreateCacheCluster`, `CreateReplicationGroup`, and
`CreateServerlessCache` start real containers with automatic port allocation from
`ELASTICACHE_PORT_BASE` (default 63790).
A TCP health check polls until the port is reachable before transitioning to "available".
When Docker is unavailable, operations are metadata-only and status transitions immediately.

One Overcast does not manage another's cache. A cache cluster ID, replication
group ID and serverless cache name are all names you choose, so two Overcasts
sharing a Docker daemon can each hold one called `sessions`, and the container
labels that name — startup reconciliation and the Docker event stream match on
the creating instance's identity as well, rather than marking a cache stopped
that the other one is still serving. The container name is derived from the
resource name and Docker requires it to be unique per daemon, so the second
Overcast to start a cache of that name fails it with a reason saying so instead
of quietly sharing the first one's. Give them different names, or a Docker
daemon each.

Supported engines: **redis** (`redis:6`, `redis:7`), **valkey** (`valkey/valkey:7`, `valkey/valkey:8`),
**memcached** (`memcached:1.5`, `memcached:1.6`).

> [!NOTE]
> Replication groups start a single primary container only — no multi-node replication is
> wired up between replicas.

## VPC placement

A cache cluster created with a `CacheSubnetGroupName` lands on that subnet
group's VPC network and nothing else, so a Lambda or ECS task outside the VPC
cannot reach it — as on AWS, where ElastiCache is never publicly accessible and
has no `PubliclyAccessible` escape hatch. Put the caller in the same VPC.

Two gaps to know about:

- **Replication groups ignore `CacheSubnetGroupName`.** It is discarded before
  it reaches the record, so a replication group always lands on the default
  plane and is reachable from everywhere. That is more permissive than AWS, not
  less, so nothing breaks — but do not rely on it to isolate anything.
- CloudFormation does not forward `CacheSubnetGroupName` either.

See [Networking § Lambda, ECS and VPCs](../networking.md) for the full picture
and for what a refused connection looks like.

---

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported | ❌ Unsupported |
| -------- | ------------ | -------------- |
| General  | 22           | 2              |

---

## Endpoints

### General

| Operation                      | Status         | Notes                                                                                                                             | AWS Docs                                                                                                        |
| ------------------------------ | -------------- | --------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `AddTagsToResource`            | ✅ Supported   | ARN-scoped tag storage                                                                                                            | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_AddTagsToResource.html)            |
| `CreateCacheCluster`           | ✅ Supported   | Docker-backed (redis/valkey/memcached); async creating→available via TCP health check; port auto-alloc                            | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateCacheCluster.html)           |
| `CreateCacheParameterGroup`    | ✅ Supported   | Stores name, family, description, and ARN                                                                                         | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateCacheParameterGroup.html)    |
| `CreateCacheSubnetGroup`       | ✅ Supported   | Stores name, description, and subnet IDs                                                                                          | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateCacheSubnetGroup.html)       |
| `CreateReplicationGroup`       | ✅ Supported   | Docker-backed (single primary node); async creating→available via TCP health check                                                | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateReplicationGroup.html)       |
| `CreateServerlessCache`        | ✅ Supported   | Docker-backed (redis/valkey/memcached); async creating→available via TCP health check; CloudFormation ServerlessCache supported   | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_CreateServerlessCache.html)        |
| `DeleteCacheCluster`           | ✅ Supported   | Sets status to "deleting"; stops and removes Docker container asynchronously                                                      | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteCacheCluster.html)           |
| `DeleteCacheParameterGroup`    | ✅ Supported   | Removes stored parameter group                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteCacheParameterGroup.html)    |
| `DeleteCacheSubnetGroup`       | ✅ Supported   | Removes stored subnet group                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteCacheSubnetGroup.html)       |
| `DeleteReplicationGroup`       | ✅ Supported   | Sets status to "deleting"; stops and removes Docker container asynchronously                                                      | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteReplicationGroup.html)       |
| `DeleteServerlessCache`        | ✅ Supported   | Sets status to "deleting"; stops and removes Docker container asynchronously                                                      | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DeleteServerlessCache.html)        |
| `DescribeCacheClusters`        | ✅ Supported   | List all or filter by CacheClusterId                                                                                              | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheClusters.html)        |
| `DescribeCacheEngineVersions`  | ❌ Unsupported | stub; returns 501                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheEngineVersions.html)  |
| `DescribeCacheParameterGroups` | ✅ Supported   | List all or filter by name                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheParameterGroups.html) |
| `DescribeCacheParameters`      | ✅ Supported   | Returns curated static parameters for the group's family; supports Source filter and MaxRecords/Marker pagination                 | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheParameters.html)      |
| `DescribeCacheSubnetGroups`    | ✅ Supported   | List all or filter by name                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeCacheSubnetGroups.html)    |
| `DescribeReplicationGroups`    | ✅ Supported   | List all or filter by ReplicationGroupId                                                                                          | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeReplicationGroups.html)    |
| `DescribeServerlessCaches`     | ✅ Supported   | List all or filter by ServerlessCacheName                                                                                         | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_DescribeServerlessCaches.html)     |
| `ListTagsForResource`          | ✅ Supported   | Returns all tags for an ARN                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_ListTagsForResource.html)          |
| `ModifyCacheCluster`           | ✅ Supported   | Metadata-only; updates nodeType, engineVersion, numNodes, parameterGroup; modifying→available                                     | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_ModifyCacheCluster.html)           |
| `ModifyReplicationGroup`       | ✅ Supported   | Metadata-only; updates description, nodeType, failover, multiAZ; modifying→available                                              | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_ModifyReplicationGroup.html)       |
| `ModifyServerlessCache`        | ✅ Supported   | Metadata-only; updates description, engine/version, usage limits, security groups, snapshots, and user group; modifying→available | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_ModifyServerlessCache.html)        |
| `RebootCacheCluster`           | ❌ Unsupported | stub; returns 501                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_RebootCacheCluster.html)           |
| `RemoveTagsFromResource`       | ✅ Supported   | Removes specific tag keys                                                                                                         | [docs](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/API_RemoveTagsFromResource.html)       |

<!-- END overcast:capabilities -->

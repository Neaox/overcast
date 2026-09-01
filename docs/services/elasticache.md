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

ElastiCache uses the AWS Query protocol (form-encoded POST, XML responses). Operations are
identified by the `Action` parameter with API version `2015-02-02`.

When Docker is available, `CreateCacheCluster`, `CreateReplicationGroup`, and
`CreateServerlessCache` start real containers with automatic port allocation from
`ELASTICACHE_PORT_BASE` (default 63790).
A readiness check polls the engine's own protocol before transitioning to "available" —
`PING` for Redis and Valkey, `version` for Memcached. A dial is not enough: Docker's published
port accepts connections as soon as the port proxy is up, before the engine has bound anything.
An engine that never answers is not left claiming progress. A cache cluster settles in
`incompatible-network` and a replication group or serverless cache in `create-failed`, which are
the terminal statuses AWS documents for each of those three shapes, with the reason recorded on
the record.
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

A cache cluster or replication group created with a `CacheSubnetGroupName`
lands on that subnet group's VPC network and nothing else, so a Lambda or ECS
task outside the VPC cannot reach it — as on AWS, where ElastiCache is never
publicly accessible and has no `PubliclyAccessible` escape hatch. Put the caller
in the same VPC.

`CreateCacheCluster`, `CreateReplicationGroup` and the CloudFormation resources
for both accept the field. A serverless cache carries subnet IDs directly
instead, and resolves its VPC from the first one that names it.

Create either without a subnet group and it stays on the default plane, which is
where AWS puts a cache that names no subnet group too — its default VPC. That is
not "reachable from everywhere": a caller that named a VPC of its own has given
up the default plane, so it still cannot reach the cache. Put both in the same
VPC, or leave both out of one.

`CacheSubnetGroupName` is a create-only parameter: AWS does not return it on the
`ReplicationGroup` shape, so neither does Overcast.

See [Networking § Lambda, ECS and VPCs](../networking.md) for the full picture
and for what a refused connection looks like.

## CloudFormation

`AWS::ElastiCache::CacheCluster` names its cluster with `ClusterName` — the
resource has no `CacheClusterId` property, whatever the API calls the parameter.
Leave it out and CloudFormation generates the name from the stack, the logical
ID and a random suffix, lowercased and capped at the 50 characters ElastiCache
allows in a cluster ID.

A cache cluster's endpoint reaches a template through `Fn::GetAtt`, under the
attribute pair its engine has — `RedisEndpoint.Address`/`.Port` for Redis and
Valkey, `ConfigurationEndpoint.Address`/`.Port` for Memcached, as AWS populates
them. The pair the engine does not have resolves to the empty string, so a
template that reads the wrong one for its engine gets the same nothing here that
it would get on AWS, rather than a value that works only locally.

A replication group's `PrimaryEndPoint` and `ConfigurationEndPoint` both carry
the endpoint. AWS populates the first only for a cluster-mode-disabled group and
the second only for a cluster-mode-enabled one; Overcast starts a single primary
and models neither node groups nor replicas, so it cannot yet tell the two
apart.

These are data-plane hostnames — the VPC placement above decides who can
resolve one.

---

<!-- BEGIN overcast:capabilities -->

## Operations

22 of 24 listed operations are implemented.
Per-operation status, notes and AWS API links: [ElastiCache operations](elasticache/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/AmazonElastiCache/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)

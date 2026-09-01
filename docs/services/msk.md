---
title: "MSK — Managed Streaming for Kafka"
description: "MSK uses the REST JSON protocol. The v1 endpoints are under /v1/ and the v2 cluster API under /api/v2/."
section: "Service Reference"
tags:
  - docs
  - kafka
  - managed
  - msk
  - services
  - streaming
---

# MSK — Managed Streaming for Kafka

MSK uses the REST JSON protocol and no other. The v1 endpoints are under `/v1/`; the v2 cluster
API — `CreateClusterV2`, `DescribeClusterV2`, `ListClustersV2` — is under `/api/v2/clusters`, which
is where AWS binds it. Both share one cluster store, so a cluster created through either API is
visible through the other.

When Docker is available, `CreateCluster` starts a real [Redpanda](https://redpanda.com/) container
(`docker.redpanda.com/redpandadata/redpanda`) with automatic port allocation from `MSK_PORT_BASE`
(default 49092). A TCP health check polls port 9092 until the broker is reachable before
transitioning the cluster to "ACTIVE". When Docker is unavailable, operations are metadata-only
and status transitions immediately.

`GetBootstrapBrokers` returns the allocated broker endpoint once the container is running.

---

<!-- BEGIN overcast:capabilities -->

## Operations

17 of 30 listed operations are implemented.
Per-operation status, notes and AWS API links: [MSK operations](msk/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/msk/latest/developerguide/what-is-msk.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)

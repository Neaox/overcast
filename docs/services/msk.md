---
title: "MSK — Managed Streaming for Kafka"
description: "MSK uses the REST JSON protocol. All endpoints are under /v1/."
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

> AWS docs: https://docs.aws.amazon.com/msk/latest/developerguide/what-is-msk.html

MSK uses the REST JSON protocol. All endpoints are under `/v1/`.

When Docker is available, `CreateCluster` starts a real [Redpanda](https://redpanda.com/) container
(`docker.redpanda.com/redpandadata/redpanda`) with automatic port allocation from `MSK_PORT_BASE`
(default 49092). A TCP health check polls port 9092 until the broker is reachable before
transitioning the cluster to "ACTIVE". When Docker is unavailable, operations are metadata-only
and status transitions immediately.

`GetBootstrapBrokers` returns the allocated broker endpoint once the container is running.

---

<!-- BEGIN overcast:capabilities -->

## Summary

| Category       | ✅ Supported | ❌ Unsupported |
| -------------- | ------------ | -------------- |
| Clusters       | 7            | 13             |
| Configurations | 5            |                |
| Kafka versions | 1            |                |
| Tagging        | 3            |                |

---

## Endpoints

### Clusters

| Operation                      | Status         | Notes                                                                                                    | AWS Docs                                                                                            |
| ------------------------------ | -------------- | -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `CreateCluster`                | ✅ Supported   | Docker-backed (Redpanda); async CREATING→ACTIVE via TCP health check; port auto-alloc from MSK_PORT_BASE | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_CreateCluster.html)                |
| `DescribeCluster`              | ✅ Supported   | Look up cluster by ARN                                                                                   | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DescribeCluster.html)              |
| `ListClusters`                 | ✅ Supported   | List all clusters; optional `clusterNameFilter` query param                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListClusters.html)                 |
| `DeleteCluster`                | ✅ Supported   | Sets state to "DELETING"; stops and removes Docker container asynchronously                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DeleteCluster.html)                |
| `GetBootstrapBrokers`          | ✅ Supported   | Returns `bootstrapBrokerString` with allocated host:port when Docker container is running                | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_GetBootstrapBrokers.html)          |
| `CreateClusterV2`              | ✅ Supported   | PROVISIONED: same Docker/Redpanda lifecycle as v1; SERVERLESS: metadata-only, immediately ACTIVE         | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_CreateClusterV2.html)              |
| `DescribeClusterV2`            | ✅ Supported   | Returns v2 shape with `clusterType` and `provisioned`/`serverless` sub-object                            | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DescribeClusterV2.html)            |
| `UpdateBrokerCount`            | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateBrokerCount.html)            |
| `UpdateBrokerStorage`          | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateBrokerStorage.html)          |
| `UpdateBrokerType`             | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateBrokerType.html)             |
| `UpdateMonitoring`             | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateMonitoring.html)             |
| `UpdateSecurity`               | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateSecurity.html)               |
| `RebootBroker`                 | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_RebootBroker.html)                 |
| `BatchAssociateScramSecret`    | ❌ Unsupported | SCRAM authentication - not implemented                                                                   | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_BatchAssociateScramSecret.html)    |
| `BatchDisassociateScramSecret` | ❌ Unsupported | SCRAM authentication - not implemented                                                                   | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_BatchDisassociateScramSecret.html) |
| `ListScramSecrets`             | ❌ Unsupported | SCRAM authentication - not implemented                                                                   | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListScramSecrets.html)             |
| `CreateVpcConnection`          | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_CreateVpcConnection.html)          |
| `DeleteVpcConnection`          | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DeleteVpcConnection.html)          |
| `DescribeVpcConnection`        | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DescribeVpcConnection.html)        |
| `ListVpcConnections`           | ❌ Unsupported | stub; returns 501                                                                                        | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListVpcConnections.html)           |

### Configurations

| Operation                    | Status       | Notes                                                                                        | AWS Docs                                                                                          |
| ---------------------------- | ------------ | -------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `CreateConfiguration`        | ✅ Supported | Stores name, description, kafka versions                                                     | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_CreateConfiguration.html)        |
| `DescribeConfiguration`      | ✅ Supported | Look up configuration by ARN                                                                 | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DescribeConfiguration.html)      |
| `ListConfigurations`         | ✅ Supported | List all configurations                                                                      | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListConfigurations.html)         |
| `DeleteConfiguration`        | ✅ Supported | Removes stored configuration                                                                 | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DeleteConfiguration.html)        |
| `UpdateClusterConfiguration` | ✅ Supported | Validates `currentVersion`; verifies configuration ARN exists; returns `clusterOperationArn` | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateClusterConfiguration.html) |

### Kafka versions

| Operation           | Status       | Notes                                                     | AWS Docs                                                                                 |
| ------------------- | ------------ | --------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `ListKafkaVersions` | ✅ Supported | Returns hardcoded list: 3.6.0, 3.5.1, 3.4.0, 2.8.1, 2.6.0 | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListKafkaVersions.html) |

### Tagging

| Operation             | Status       | Notes                       | AWS Docs                                                                                   |
| --------------------- | ------------ | --------------------------- | ------------------------------------------------------------------------------------------ |
| `TagResource`         | ✅ Supported | ARN-scoped tag storage      | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_TagResource.html)         |
| `ListTagsForResource` | ✅ Supported | Returns all tags for an ARN | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListTagsForResource.html) |
| `UntagResource`       | ✅ Supported | Removes specific tag keys   | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UntagResource.html)       |

<!-- END overcast:capabilities -->

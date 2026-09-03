---
title: "MSK operations"
description: "Every MSK operation Overcast declares — 17 of 30 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - msk
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# MSK operations

17 of 30 listed operations are implemented. Back to [MSK](../msk.md).

## Summary

| Category       | ✅ Supported | ❌ Unsupported |
| -------------- | ------------ | -------------- |
| Clusters       | 8            | 13             |
| Configurations | 5            |                |
| Kafka versions | 1            |                |
| Tagging        | 3            |                |

---

## Endpoints

### Clusters

| Operation                      | Status         | Notes                                                                                                                                                                                                                                                          | AWS Docs                                                                                            |
| ------------------------------ | -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `CreateCluster`                | ✅ Supported   | Docker-backed (Redpanda); async CREATING→ACTIVE once the broker answers `ApiVersions`; a broker that never answers ends in `FAILED` with `stateInfo`; port auto-alloc from MSK_PORT_BASE; a cluster name already in use in the region is a `ConflictException` | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_CreateCluster.html)                |
| `DescribeCluster`              | ✅ Supported   | Look up cluster by ARN                                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DescribeCluster.html)              |
| `ListClusters`                 | ✅ Supported   | List all clusters; optional `clusterNameFilter` query param matches on the name prefix                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListClusters.html)                 |
| `DeleteCluster`                | ✅ Supported   | Sets state to "DELETING"; stops and removes Docker container asynchronously                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DeleteCluster.html)                |
| `GetBootstrapBrokers`          | ✅ Supported   | Returns `bootstrapBrokerString` with allocated host:port when Docker container is running                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_GetBootstrapBrokers.html)          |
| `CreateClusterV2`              | ✅ Supported   | PROVISIONED: same Docker/Redpanda lifecycle as v1; SERVERLESS: metadata-only, immediately ACTIVE; rejects a request naming both or neither; shares one cluster-name namespace with `CreateCluster`, so a name already in use is a `ConflictException`          | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_CreateClusterV2.html)              |
| `DescribeClusterV2`            | ✅ Supported   | Returns v2 shape with `clusterType` and `provisioned`/`serverless` sub-object                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DescribeClusterV2.html)            |
| `ListClustersV2`               | ✅ Supported   | `clusterNameFilter` (prefix), `clusterTypeFilter`, `maxResults` and `nextToken` query params; page size capped at 100                                                                                                                                          | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListClustersV2.html)               |
| `UpdateBrokerCount`            | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateBrokerCount.html)            |
| `UpdateBrokerStorage`          | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateBrokerStorage.html)          |
| `UpdateBrokerType`             | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateBrokerType.html)             |
| `UpdateMonitoring`             | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateMonitoring.html)             |
| `UpdateSecurity`               | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateSecurity.html)               |
| `RebootBroker`                 | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_RebootBroker.html)                 |
| `BatchAssociateScramSecret`    | ❌ Unsupported | SCRAM authentication - not implemented                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_BatchAssociateScramSecret.html)    |
| `BatchDisassociateScramSecret` | ❌ Unsupported | SCRAM authentication - not implemented                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_BatchDisassociateScramSecret.html) |
| `ListScramSecrets`             | ❌ Unsupported | SCRAM authentication - not implemented                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListScramSecrets.html)             |
| `CreateVpcConnection`          | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_CreateVpcConnection.html)          |
| `DeleteVpcConnection`          | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DeleteVpcConnection.html)          |
| `DescribeVpcConnection`        | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DescribeVpcConnection.html)        |
| `ListVpcConnections`           | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListVpcConnections.html)           |

### Configurations

| Operation                    | Status       | Notes                                                                                                                                                                     | AWS Docs                                                                                          |
| ---------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `CreateConfiguration`        | ✅ Supported | Stores name, description, kafka versions; a configuration name already in use in the region is a `ConflictException`                                                      | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_CreateConfiguration.html)        |
| `DescribeConfiguration`      | ✅ Supported | Look up configuration by ARN                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DescribeConfiguration.html)      |
| `ListConfigurations`         | ✅ Supported | List all configurations                                                                                                                                                   | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_ListConfigurations.html)         |
| `DeleteConfiguration`        | ✅ Supported | Removes stored configuration                                                                                                                                              | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_DeleteConfiguration.html)        |
| `UpdateClusterConfiguration` | ✅ Supported | Reads the modeled `configurationInfo` object; validates `currentVersion`; a configuration ARN that does not exist is a `NotFoundException`; returns `clusterOperationArn` | [docs](https://docs.aws.amazon.com/msk/latest/developerguide/API_UpdateClusterConfiguration.html) |

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

## Related

- [MSK](../msk.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->

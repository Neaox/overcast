---
title: "MSK — Managed Streaming for Kafka"
description: "MSK clusters backed by a real Redpanda broker per cluster, promoted to ACTIVE only once it answers Kafka's ApiVersions. Broker scaling, SCRAM and VPC connections are not implemented."
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

A provisioned cluster starts a real [Redpanda](https://redpanda.com/) broker you
can produce to and consume from; without Docker it is metadata only.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

ARN=$(aws kafka create-cluster --cluster-name events \
  --kafka-version 3.6.0 --number-of-broker-nodes 1 \
  --broker-node-group-info '{"InstanceType":"kafka.m5.large","ClientSubnets":[]}' \
  --query ClusterArn --output text)

aws kafka describe-cluster --cluster-arn "$ARN"   # wait for ACTIVE
aws kafka get-bootstrap-brokers --cluster-arn "$ARN"
# → { "BootstrapBrokerString": "127.0.0.1:49092" }
```

## What works

| Area | Behaviour |
| --- | --- |
| Real broker | `CreateCluster` starts a Redpanda container, ports allocated from `MSK_PORT_BASE` (default 49092) |
| Honest readiness | `CREATING` → `ACTIVE` only once the broker answers a Kafka `ApiVersions` request — a published port that merely accepts a TCP connection is not enough |
| Failure is terminal | A broker that never answers within 120 seconds settles the cluster in `FAILED`, with the reason in `stateInfo`, so `aws kafka wait cluster-active` stops instead of spinning |
| Bootstrap | `GetBootstrapBrokers` answers per caller: `{cluster}.{region}.kafka.{base}:9092` for a sibling container, the published host port for the host, so one stack output works from both sides |
| v1 and v2 | `/v1/` and `/api/v2/clusters` share one cluster store, so a cluster created through either is visible through both |
| Serverless | `CreateClusterV2` accepts a `serverless` cluster as metadata, immediately `ACTIVE` |
| VPC placement | A cluster whose `ClientSubnets` name a VPC has its broker placed on that VPC's network, so only callers in it can reach the broker |
| Configurations | Create, describe, list, delete, and `UpdateClusterConfiguration` with `currentVersion` validation |
| Tags | `TagResource`, `ListTagsForResource`, `UntagResource` on any MSK ARN |

## Differences from AWS

| Area                            | Overcast                                                                                                                                                                                                                                    |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| One broker, always              | `NumberOfBrokerNodes` is recorded, not honoured; there is a single Redpanda node whatever the request asks for                                                                                                                              |
| Redpanda, not Kafka             | Wire-compatible with the Kafka protocol, but broker internals, JMX metrics and Kafka-specific admin behaviour differ                                                                                                                        |
| `ListKafkaVersions` is fixed    | It reports 3.6.0, 3.5.1, 3.4.0, 2.8.1 and 2.6.0 regardless of what the container runs                                                                                                                                                       |
| No broker changes               | `UpdateBrokerCount`, `UpdateBrokerStorage`, `UpdateBrokerType`, `UpdateMonitoring`, `UpdateSecurity` and `RebootBroker` return `501`                                                                                                        |
| No SCRAM                        | The secret-association operations return `501`; the broker is reachable without authentication                                                                                                                                              |
| No VPC connections              | `CreateVpcConnection` and its siblings return `501`                                                                                                                                                                                         |
| Encryption and auth are dropped | `encryptionInfo`, `clientAuthentication`, `loggingInfo`, `openMonitoring` and a broker group's `storageInfo` are accepted and discarded — a cluster keeps only `instanceType`, `clientSubnets`, `securityGroups` and `brokerAZDistribution` |

## Gotchas

> [!IMPORTANT]
> A cluster name already in use in the region is a `ConflictException`, and
> `CreateCluster` and `CreateClusterV2` share that namespace.

Broker containers are scoped to the instance that created them.

> [!NOTE]
> Two Overcasts on one Docker daemon do not disturb each other's brokers:
> containers carry the identity of the instance that created them, and the
> startup sweep and Docker event stream both match on it.

<!-- BEGIN overcast:capabilities -->

## Operations

17 of 30 listed operations are implemented.
Per-operation status, notes and AWS API links: [MSK operations](msk/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Kinesis Data Streams](./kinesis.md)
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [Lambda, ECS and VPCs](../networking/vpcs.md)
- [AWS API reference](https://docs.aws.amazon.com/msk/latest/developerguide/what-is-msk.html)

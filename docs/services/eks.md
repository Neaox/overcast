---
title: "EKS — Amazon Elastic Kubernetes Service"
description: "EKS supports two modes:"
section: "Service Reference"
tags:
  - amazon
  - docs
  - eks
  - elastic
  - kubernetes
  - service
  - services
---

# EKS — Amazon Elastic Kubernetes Service

EKS supports two modes:

- `mock` (default): metadata-only controller API.
- `live` (opt-in via `OVERCAST_EKS_MODE=live`): launches a k3s control-plane container on `CreateCluster`.

## Behavior notes

- In `mock` mode, cluster status is immediately `ACTIVE` and endpoint/CA fields are synthetic placeholders.
- In `live` mode, cluster status starts `CREATING` and transitions to `ACTIVE` after k3s `/readyz` responds. The k3s image is pulled first, so the first cluster on a machine that has never run one waits on that download.
- In `live` mode, a control plane that cannot be started reaches `FAILED` instead of staying `CREATING`, and `DescribeCluster` reports why under `cluster.health.issues` — the Docker error verbatim for an image that cannot be pulled or a container that cannot be created or started.
- In `live` mode, `DescribeCluster` endpoint uses `https://<OVERCAST_HOSTNAME-or-localhost>:<mapped-port>`.
- In `live` mode, `ListClusters` filters out legacy mock-record clusters (`*.mock.eks.local`) to avoid mixed-mode leakage.
- In `live` mode, cluster-scoped read/update APIs for update/insight/config flows reject legacy mock-record clusters with `501` to keep behavior mode-consistent.
- In `live` mode, `UpdateClusterConfig` follows the same mixed-mode rule and rejects legacy mock-record clusters with `501`.
- In `live` mode, nodegroup CRUD/update/list endpoints also reject legacy mock-record clusters with `501` for the same mixed-mode safety boundary.
- In `live` mode, access-entry and access-policy association endpoints also reject legacy mock-record clusters with `501`.
- In `live` mode, identity-provider-config and pod-identity-association endpoints also reject legacy mock-record clusters with `501`.
- In `live` mode, fargate-profile and cluster-scoped add-on endpoints also reject legacy mock-record clusters with `501`.
- In `live` mode, `DeleteCluster` remains allowed for legacy mock-record clusters so mixed-mode leftovers can be cleaned up.
- `UpdateKubeconfig` is an Overcast extension rather than an AWS API operation: `aws eks update-kubeconfig` is a CLI-side command that calls `DescribeCluster` and writes the file locally. Overcast serves the generated kubeconfig at `POST /_overcast/eks/clusters/{name}/kubeconfig`, which no AWS SDK calls.
- In `live` mode, `UpdateKubeconfig` returns generated kubeconfig once the cluster reaches `ACTIVE` and runtime CA data is available; when CA is missing it attempts an on-demand backfill from the k3s runtime container before returning `503`.
- Nodegroups are metadata-only in both modes and do not start compute.

## Live mode limits and non-goals

- `live` mode is intentionally opt-in and has a much larger resource footprint than the default `mock` mode.
- Startup and idle-memory headline claims for Overcast are measured with `OVERCAST_EKS_MODE=mock`.
- Live-mode EKS launches a k3s control-plane container only; it does not provision real EKS worker capacity.
- Nodegroup, Fargate profile, add-on, access entry/policy, identity provider config, and pod identity association APIs are control-plane metadata surfaces; they do not enforce IAM policy semantics or schedule Kubernetes workloads on managed EKS infrastructure.
- Legacy mock-created EKS records remain blocked by design in live mode (`501`) for read/update/mutation APIs; `DeleteCluster` stays allowed for cleanup.

<!-- BEGIN overcast:capabilities -->

## Operations

All 50 listed operations are implemented.
Per-operation status, notes and AWS API links: [EKS operations](eks/operations.md).

<!-- END overcast:capabilities -->

## Related

- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)

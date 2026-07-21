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
- In `live` mode, cluster status starts `CREATING` and transitions to `ACTIVE` after k3s `/readyz` responds.
- In `live` mode, `DescribeCluster` endpoint uses `https://<OVERCAST_HOSTNAME-or-localhost>:<mapped-port>`.
- In `live` mode, `ListClusters` filters out legacy mock-record clusters (`*.mock.eks.local`) to avoid mixed-mode leakage.
- In `live` mode, cluster-scoped read/update APIs for update/insight/config flows reject legacy mock-record clusters with `501` to keep behavior mode-consistent.
- In `live` mode, `UpdateClusterConfig` follows the same mixed-mode rule and rejects legacy mock-record clusters with `501`.
- In `live` mode, nodegroup CRUD/update/list endpoints also reject legacy mock-record clusters with `501` for the same mixed-mode safety boundary.
- In `live` mode, access-entry and access-policy association endpoints also reject legacy mock-record clusters with `501`.
- In `live` mode, identity-provider-config and pod-identity-association endpoints also reject legacy mock-record clusters with `501`.
- In `live` mode, fargate-profile and cluster-scoped add-on endpoints also reject legacy mock-record clusters with `501`.
- In `live` mode, `DeleteCluster` remains allowed for legacy mock-record clusters so mixed-mode leftovers can be cleaned up.
- In `live` mode, `UpdateKubeconfig` returns generated kubeconfig once the cluster reaches `ACTIVE` and runtime CA data is available; when CA is missing it attempts an on-demand backfill from the k3s runtime container before returning `503`.
- Nodegroups are metadata-only in both modes and do not start compute.

## Live mode limits and non-goals

- `live` mode is intentionally opt-in and has a much larger resource footprint than the default `mock` mode.
- Startup and idle-memory headline claims for Overcast are measured with `OVERCAST_EKS_MODE=mock`.
- Live-mode EKS launches a k3s control-plane container only; it does not provision real EKS worker capacity.
- Nodegroup, Fargate profile, add-on, access entry/policy, identity provider config, and pod identity association APIs are control-plane metadata surfaces; they do not enforce IAM policy semantics or schedule Kubernetes workloads on managed EKS infrastructure.
- Legacy mock-created EKS records remain blocked by design in live mode (`501`) for read/update/mutation APIs; `DeleteCluster` stays allowed for cleanup.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | ✅ Supported |
| ---------- | ------------ |
| Clusters   | 31           |
| Helpers    | 1            |
| Nodegroups | 6            |
| Fargate    | 4            |
| Tags       | 3            |
| Addons     | 7            |

---

## Endpoints

### Clusters

| Operation                            | Status       | Notes                                                                                                                                                              | AWS Docs                                                                                                |
| ------------------------------------ | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------- |
| `CreateCluster`                      | ✅ Supported | Stores cluster metadata including roleArn, version, resourcesVpcConfig, kubernetesNetworkConfig, and encryptionConfig; describe returns inline tags                | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_CreateCluster.html)                      |
| `DescribeCluster`                    | ✅ Supported |                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeCluster.html)                    |
| `DescribeClusterVersions`            | ✅ Supported | Returns synthetic supported Kubernetes version catalog                                                                                                             | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeClusterVersions.html)            |
| `ListClusters`                       | ✅ Supported |                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListClusters.html)                       |
| `CreateAccessEntry`                  | ✅ Supported | Stores cluster principal access entry metadata and persists inline tags                                                                                            | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_CreateAccessEntry.html)                  |
| `DescribeAccessEntry`                | ✅ Supported | Returns stored access entry metadata for a cluster principal ARN with inline tags                                                                                  | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeAccessEntry.html)                |
| `UpdateAccessEntry`                  | ✅ Supported | Updates stored access entry username/groups for a cluster principal ARN                                                                                            | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateAccessEntry.html)                  |
| `DeleteAccessEntry`                  | ✅ Supported | Deletes stored access entry metadata for a cluster principal ARN                                                                                                   | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DeleteAccessEntry.html)                  |
| `ListAccessEntries`                  | ✅ Supported | Returns stored principal ARNs for cluster access entries                                                                                                           | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListAccessEntries.html)                  |
| `AssociateAccessPolicy`              | ✅ Supported | Associates a policy ARN with a stored access entry principal                                                                                                       | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_AssociateAccessPolicy.html)              |
| `ListAccessPolicies`                 | ✅ Supported | Returns synthetic managed EKS access policy catalog                                                                                                                | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListAccessPolicies.html)                 |
| `DescribeAccessPolicy`               | ✅ Supported | Returns synthetic managed EKS access policy details by name                                                                                                        | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeAccessPolicy.html)               |
| `ListAssociatedAccessPolicies`       | ✅ Supported | Lists associated policy ARNs and access scopes for a stored access entry principal                                                                                 | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListAssociatedAccessPolicies.html)       |
| `DisassociateAccessPolicy`           | ✅ Supported | Disassociates a policy ARN from a stored access entry principal                                                                                                    | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DisassociateAccessPolicy.html)           |
| `ListIdentityProviderConfigs`        | ✅ Supported | Returns stored identity provider config summaries                                                                                                                  | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListIdentityProviderConfigs.html)        |
| `DescribeIdentityProviderConfig`     | ✅ Supported | Returns stored identity provider config details by type/name with inline tags                                                                                      | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeIdentityProviderConfig.html)     |
| `UpdateIdentityProviderConfig`       | ✅ Supported | Updates stored identity provider config fields and records an update entry                                                                                         | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateIdentityProviderConfig.html)       |
| `AssociateIdentityProviderConfig`    | ✅ Supported | Stores OIDC identity provider metadata, persists inline tags, and records an update entry                                                                          | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_AssociateIdentityProviderConfig.html)    |
| `DisassociateIdentityProviderConfig` | ✅ Supported | Removes stored identity provider metadata, clears inline tags, and records an update entry                                                                         | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DisassociateIdentityProviderConfig.html) |
| `CreatePodIdentityAssociation`       | ✅ Supported | Creates and stores pod identity association metadata for a cluster service account, persists inline tags, and rejects duplicate namespace/service-account bindings | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_CreatePodIdentityAssociation.html)       |
| `ListPodIdentityAssociations`        | ✅ Supported | Returns stored pod identity associations for a cluster                                                                                                             | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListPodIdentityAssociations.html)        |
| `DescribePodIdentityAssociation`     | ✅ Supported | Returns stored pod identity association details by association ID with inline tags                                                                                 | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribePodIdentityAssociation.html)     |
| `UpdatePodIdentityAssociation`       | ✅ Supported | Updates stored pod identity association role ARN by association ID                                                                                                 | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdatePodIdentityAssociation.html)       |
| `DeletePodIdentityAssociation`       | ✅ Supported | Deletes stored pod identity association metadata by association ID                                                                                                 | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DeletePodIdentityAssociation.html)       |
| `ListUpdates`                        | ✅ Supported | Lists recorded update IDs for a cluster                                                                                                                            | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListUpdates.html)                        |
| `ListInsights`                       | ✅ Supported | Returns synthetic health/readiness insight summaries for a cluster                                                                                                 | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListInsights.html)                       |
| `DescribeInsight`                    | ✅ Supported | Returns synthetic health/readiness insight detail by insight ID                                                                                                    | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeInsight.html)                    |
| `UpdateClusterConfig`                | ✅ Supported | Updates stored cluster logging, resourcesVpcConfig, and kubernetesNetworkConfig; records an update entry                                                           | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateClusterConfig.html)                |
| `UpdateClusterVersion`               | ✅ Supported | Updates stored cluster version metadata                                                                                                                            | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateClusterVersion.html)               |
| `DescribeUpdate`                     | ✅ Supported | Returns previously recorded cluster/nodegroup update status by update ID                                                                                           | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeUpdate.html)                     |
| `DeleteCluster`                      | ✅ Supported | Deletes cluster metadata and nodegroups                                                                                                                            | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DeleteCluster.html)                      |

### Helpers

| Operation          | Status       | Notes                                                                                                              | AWS Docs                                                                              |
| ------------------ | ------------ | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------- |
| `UpdateKubeconfig` | ✅ Supported | Returns generated kubeconfig YAML for mock clusters and ready live clusters (503 until live endpoint/CA are ready) | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateKubeconfig.html) |

### Nodegroups

| Operation                | Status       | Notes                                                                                                                                                                    | AWS Docs                                                                                    |
| ------------------------ | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------- |
| `CreateNodegroup`        | ✅ Supported | Stores full nodegroup metadata including instanceTypes, amiType, capacityType, diskSize, taints, labels, scalingConfig, updateConfig, launchTemplate, and releaseVersion | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_CreateNodegroup.html)        |
| `UpdateNodegroupVersion` | ✅ Supported | Updates stored nodegroup version metadata                                                                                                                                | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateNodegroupVersion.html) |
| `UpdateNodegroupConfig`  | ✅ Supported | Updates stored nodegroup labels, taints, scalingConfig, and updateConfig; records an update entry                                                                        | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateNodegroupConfig.html)  |
| `DescribeNodegroup`      | ✅ Supported |                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeNodegroup.html)      |
| `ListNodegroups`         | ✅ Supported |                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListNodegroups.html)         |
| `DeleteNodegroup`        | ✅ Supported | Deletes nodegroup metadata                                                                                                                                               | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DeleteNodegroup.html)        |

### Fargate

| Operation                | Status       | Notes                                                                                 | AWS Docs                                                                                    |
| ------------------------ | ------------ | ------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `ListFargateProfiles`    | ✅ Supported | Lists stored profiles; always includes synthetic "default" profile                    | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListFargateProfiles.html)    |
| `DescribeFargateProfile` | ✅ Supported | Returns stored or synthetic default profile                                           | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeFargateProfile.html) |
| `CreateFargateProfile`   | ✅ Supported | Stores Fargate profile metadata including podExecutionRoleArn, subnets, and selectors | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_CreateFargateProfile.html)   |
| `DeleteFargateProfile`   | ✅ Supported | Removes stored Fargate profile metadata                                               | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DeleteFargateProfile.html)   |

### Tags

| Operation             | Status       | Notes                                    | AWS Docs                                                                                 |
| --------------------- | ------------ | ---------------------------------------- | ---------------------------------------------------------------------------------------- |
| `ListTagsForResource` | ✅ Supported | Returns tags for any EKS resource ARN    | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListTagsForResource.html) |
| `TagResource`         | ✅ Supported | Adds tags to an EKS resource by ARN      | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tags from an EKS resource by ARN | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UntagResource.html)       |

### Addons

| Operation                    | Status       | Notes                                                                                                                       | AWS Docs                                                                                        |
| ---------------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| `CreateAddon`                | ✅ Supported | Stores add-on metadata including addonVersion, configurationValues, and serviceAccountRoleArn; describe returns inline tags | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_CreateAddon.html)                |
| `DescribeAddon`              | ✅ Supported |                                                                                                                             | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeAddon.html)              |
| `ListAddons`                 | ✅ Supported |                                                                                                                             | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListAddons.html)                 |
| `UpdateAddon`                | ✅ Supported | Updates stored add-on version/configuration and records an update entry                                                     | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_UpdateAddon.html)                |
| `DeleteAddon`                | ✅ Supported | Removes add-on metadata                                                                                                     | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DeleteAddon.html)                |
| `DescribeAddonConfiguration` | ✅ Supported | Returns synthetic configuration schema for core add-ons                                                                     | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeAddonConfiguration.html) |
| `DescribeAddonVersions`      | ✅ Supported | Returns synthetic version catalog for vpc-cni, coredns, kube-proxy, aws-ebs-csi-driver; empty for unknown add-ons           | [docs](https://docs.aws.amazon.com/eks/latest/APIReference/API_DescribeAddonVersions.html)      |

<!-- END overcast:capabilities -->

---
title: "EFS — Amazon Elastic File System"
description: "Control-plane emulation of EFS: file systems, mount targets, access points, policies, lifecycle and backup configuration, and tagging."
section: "Service Reference"
tags:
  - amazon
  - docs
  - efs
  - elastic
  - file
  - service
  - services
  - storage
---

# EFS — Amazon Elastic File System

Control-plane emulation of EFS: file systems, mount targets, access points,
file-system policies, lifecycle and backup configuration, tagging (current and
legacy APIs), and account preferences. The REST-JSON API is served under the
real AWS `/2015-02-01/` path prefix, so unmodified AWS SDKs and the AWS CLI
work as-is.

## Behavior notes

- **Metadata only — no NFS data plane.** File systems are not mountable;
  mount targets exist as metadata with deterministic synthesized network
  fields (availability zone, IP address, ENI ID derived from the subnet ID).
- Resources follow the real lifecycle (`creating` → `available` → `deleting`).
  Transitions complete inline with a real clock, so a resource is usable as
  soon as its create call returns; under a mock clock the intermediate states
  are observable.
- `CreateFileSystem` is idempotent on `CreationToken` and enforces the
  performance/throughput-mode pairing rules (`provisioned` requires
  `ProvisionedThroughputInMibps`; `maxIO` is incompatible with `elastic`
  throughput and One Zone file systems).
- `DeleteFileSystem` returns `FileSystemInUse` while mount targets exist, and
  removes the file system's access points, tags, and policy with it.
- Mount targets are limited to one per availability zone/subnet per file
  system (`MountTargetConflict`) and at most 5 security groups; security
  groups are stored, not validated against EC2.
- The file-system policy is stored and echoed verbatim (JSON-validated), but
  not enforced on requests.
- `PutBackupPolicy` stores `ENABLED`/`DISABLED` directly without the
  transitional `ENABLING`/`DISABLING` states, and there is no AWS Backup
  integration.
- Replication configuration APIs are not implemented and return `501`.
- Generated resource IDs are always long-form (`fs-`/`fsmt-`/`fsap-` +
  17 hex chars) regardless of the account preference.

## CloudFormation

`AWS::EFS::FileSystem`, `AWS::EFS::MountTarget`, and `AWS::EFS::AccessPoint`
are fully supported, including `FileSystemPolicy`, `LifecyclePolicies`,
`BackupPolicy`, `FileSystemProtection`, and tag updates. `Ref` returns the
resource ID; `GetAtt` supports `Arn`/`FileSystemId` (file system),
`Id`/`IpAddress` (mount target), and `AccessPointId`/`Arn` (access point).
Property changes that replace on real AWS (encryption, performance mode,
subnet, POSIX identity, …) trigger replacement here too.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category      | ✅ Supported | ❌ Unsupported |
| ------------- | ------------ | -------------- |
| File systems  | 5            |                |
| Mount targets | 5            |                |
| Access points | 3            |                |
| Lifecycle     | 2            |                |
| Backup        | 2            |                |
| Policy        | 3            |                |
| Tags          | 6            |                |
| Account       | 2            |                |
| Replication   |              | 3              |

---

## Endpoints

### File systems

| Operation                    | Status       | Notes                                                                                                                                                       | AWS Docs                                                                              |
| ---------------------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `CreateFileSystem`           | ✅ Supported | Creation-token idempotency, performance/throughput mode validation, One Zone metadata, optional Backup flag and inline tags; lifecycle creating → available | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_CreateFileSystem.html)           |
| `DescribeFileSystems`        | ✅ Supported | Filters by FileSystemId or CreationToken; Marker/MaxItems pagination                                                                                        | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeFileSystems.html)        |
| `UpdateFileSystem`           | ✅ Supported | Updates ThroughputMode and ProvisionedThroughputInMibps with pairing validation                                                                             | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_UpdateFileSystem.html)           |
| `DeleteFileSystem`           | ✅ Supported | Rejects deletion while mount targets exist (FileSystemInUse); cascades access points, tags, and policy                                                      | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DeleteFileSystem.html)           |
| `UpdateFileSystemProtection` | ✅ Supported | Stores ReplicationOverwriteProtection                                                                                                                       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_UpdateFileSystemProtection.html) |

### Mount targets

| Operation                           | Status       | Notes                                                                                                                                      | AWS Docs                                                                                     |
| ----------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| `CreateMountTarget`                 | ✅ Supported | Metadata only — no NFS data plane. One mount target per AZ/subnet enforced; AZ and IP are synthesized deterministically from the subnet ID | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_CreateMountTarget.html)                 |
| `DescribeMountTargets`              | ✅ Supported | Lookup by FileSystemId, MountTargetId, or AccessPointId; Marker/MaxItems pagination                                                        | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeMountTargets.html)              |
| `DeleteMountTarget`                 | ✅ Supported | Lifecycle deleting → removed                                                                                                               | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DeleteMountTarget.html)                 |
| `DescribeMountTargetSecurityGroups` | ✅ Supported |                                                                                                                                            | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeMountTargetSecurityGroups.html) |
| `ModifyMountTargetSecurityGroups`   | ✅ Supported | Enforces the 5-security-group limit; groups are stored, not validated against EC2                                                          | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_ModifyMountTargetSecurityGroups.html)   |

### Access points

| Operation              | Status       | Notes                                                                                                          | AWS Docs                                                                        |
| ---------------------- | ------------ | -------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| `CreateAccessPoint`    | ✅ Supported | Client-token idempotency conflict, PosixUser/RootDirectory stored, inline tags; lifecycle creating → available | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_CreateAccessPoint.html)    |
| `DescribeAccessPoints` | ✅ Supported | Filters by AccessPointId or FileSystemId (mutually exclusive); NextToken/MaxResults pagination                 | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeAccessPoints.html) |
| `DeleteAccessPoint`    | ✅ Supported |                                                                                                                | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DeleteAccessPoint.html)    |

### Lifecycle

| Operation                        | Status       | Notes                                                                                                          | AWS Docs                                                                                  |
| -------------------------------- | ------------ | -------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `PutLifecycleConfiguration`      | ✅ Supported | Validates transition enums and the one-transition-per-policy rule; storage-class transitions are metadata only | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_PutLifecycleConfiguration.html)      |
| `DescribeLifecycleConfiguration` | ✅ Supported |                                                                                                                | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeLifecycleConfiguration.html) |

### Backup

| Operation              | Status       | Notes                                                                                                   | AWS Docs                                                                        |
| ---------------------- | ------------ | ------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| `PutBackupPolicy`      | ✅ Supported | Stores ENABLED/DISABLED directly (no ENABLING/DISABLING transitional states); no AWS Backup integration | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_PutBackupPolicy.html)      |
| `DescribeBackupPolicy` | ✅ Supported |                                                                                                         | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeBackupPolicy.html) |

### Policy

| Operation                  | Status       | Notes                                                                 | AWS Docs                                                                            |
| -------------------------- | ------------ | --------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| `PutFileSystemPolicy`      | ✅ Supported | Stores the policy document (JSON-validated); not enforced on requests | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_PutFileSystemPolicy.html)      |
| `DescribeFileSystemPolicy` | ✅ Supported |                                                                       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeFileSystemPolicy.html) |
| `DeleteFileSystemPolicy`   | ✅ Supported |                                                                       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DeleteFileSystemPolicy.html)   |

### Tags

| Operation             | Status       | Notes                                                   | AWS Docs                                                                       |
| --------------------- | ------------ | ------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `TagResource`         | ✅ Supported | Accepts file-system and access-point IDs or ARNs        | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported |                                                         | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported |                                                         | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_ListTagsForResource.html) |
| `CreateTags`          | ✅ Supported | Legacy alias of TagResource (file systems only)         | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_CreateTags.html)          |
| `DeleteTags`          | ✅ Supported | Legacy alias of UntagResource (file systems only)       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DeleteTags.html)          |
| `DescribeTags`        | ✅ Supported | Legacy alias of ListTagsForResource (file systems only) | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeTags.html)        |

### Account

| Operation                    | Status       | Notes                                                     | AWS Docs                                                                              |
| ---------------------------- | ------------ | --------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `DescribeAccountPreferences` | ✅ Supported | Defaults to LONG_ID                                       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeAccountPreferences.html) |
| `PutAccountPreferences`      | ✅ Supported | Stores the preference; generated IDs are always long-form | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_PutAccountPreferences.html)      |

### Replication

| Operation                           | Status         | Notes | AWS Docs                                                                                     |
| ----------------------------------- | -------------- | ----- | -------------------------------------------------------------------------------------------- |
| `CreateReplicationConfiguration`    | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_CreateReplicationConfiguration.html)    |
| `DeleteReplicationConfiguration`    | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DeleteReplicationConfiguration.html)    |
| `DescribeReplicationConfigurations` | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeReplicationConfigurations.html) |

<!-- END overcast:capabilities -->

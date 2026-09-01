---
title: "EFS operations"
description: "Every EFS operation Overcast declares — 28 of 31 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - efs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# EFS operations

28 of 31 listed operations are implemented. Back to [EFS](../efs.md).

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

| Operation                           | Status       | Notes                                                                                                                                                                                   | AWS Docs                                                                                     |
| ----------------------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `CreateMountTarget`                 | ✅ Supported | Serves a real NFSv4 export with OVERCAST_EFS_NFS=true, metadata only otherwise. One mount target per AZ/subnet enforced; AZ and IP are synthesized deterministically from the subnet ID | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_CreateMountTarget.html)                 |
| `DescribeMountTargets`              | ✅ Supported | Lookup by FileSystemId, MountTargetId, or AccessPointId; Marker/MaxItems pagination                                                                                                     | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeMountTargets.html)              |
| `DeleteMountTarget`                 | ✅ Supported | Lifecycle deleting → removed                                                                                                                                                            | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DeleteMountTarget.html)                 |
| `DescribeMountTargetSecurityGroups` | ✅ Supported |                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_DescribeMountTargetSecurityGroups.html) |
| `ModifyMountTargetSecurityGroups`   | ✅ Supported | Enforces the 5-security-group limit; groups are stored, not validated against EC2                                                                                                       | [docs](https://docs.aws.amazon.com/efs/latest/ug/API_ModifyMountTargetSecurityGroups.html)   |

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

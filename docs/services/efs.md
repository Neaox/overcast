---
title: "EFS — Amazon Elastic File System"
description: "Quick start, the Docker volume behind each file system, access points and mount targets, the live and mock modes, and the network fields and policies that are synthetic."
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

Every file system is backed by a real Docker volume, so a Lambda function and an
ECS task mounting it share the same bytes — no NFS hop required.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

FS=$(aws efs create-file-system --creation-token demo \
  --query FileSystemId --output text)
aws efs create-access-point --file-system-id "$FS" \
  --root-directory 'Path=/data,CreationInfo={OwnerUid=1000,OwnerGid=1000,Permissions=0755}'
aws efs describe-file-systems --file-system-id "$FS"

docker volume ls --filter name=overcast-efs-"$FS"
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

## What works

| Area | Behaviour |
| --- | --- |
| Real storage | `CreateFileSystem` creates the Docker volume `overcast-efs-<FileSystemId>`; `DeleteFileSystem` removes it |
| Shared bytes | The volume is mounted into Lambda containers (`FileSystemConfigs`) and ECS task containers (`efsVolumeConfiguration`), scoped to the access point's root directory by Docker volume subpaths (Docker Engine 26+) |
| Access points | Root directories with `CreationInfo` are created in the volume with the declared ownership and permissions before the first mount; without it, a missing directory fails the mount, as on AWS |
| Lifecycle | Resources move through `creating` → `available` → `deleting` and are usable as soon as the create call returns |
| Validation | `CreateFileSystem` is idempotent on `CreationToken` and enforces AWS's performance/throughput pairing rules; `DeleteFileSystem` answers `FileSystemInUse` while mount targets exist |
| Mount targets | One per availability zone/subnet per file system (`MountTargetConflict`), up to 5 security groups |
| Config surface | File-system policies, lifecycle and backup policies, account preferences, and both the current and legacy tag APIs |
| CloudFormation | `AWS::EFS::FileSystem`, `AWS::EFS::MountTarget` and `AWS::EFS::AccessPoint`, including policies and the property changes that force replacement on AWS |
| Optional NFS | `OVERCAST_EFS_NFS=true` gives every mount target a mountable NFSv4 export — see [EFS examples](./efs/examples.md) |
| Bindings | AWS's own `/2015-02-01/` REST-JSON paths, so SDKs and `aws efs …` work unmodified |

## Differences from AWS

| Area                            | Overcast                                                                                                                                         |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| Synthetic network fields        | `DescribeMountTargets.IpAddress`, availability zone and ENI id are derived from the subnet id; the address is never what an export is reached on |
| No data plane without Docker    | In `mock` mode — and in `live` mode while no Docker daemon is reachable — mount targets are metadata and there is no storage behind them         |
| Policies are not enforced       | A file-system policy is JSON-validated, stored and echoed, never applied to a request                                                            |
| Security groups are not checked | They are stored, not validated against EC2 or enforced                                                                                           |
| Backup policy has no states     | `PutBackupPolicy` stores `ENABLED`/`DISABLED` with no `ENABLING`/`DISABLING`, and there is no AWS Backup integration                             |
| No replication                  | The replication configuration operations answer `501`                                                                                            |
| Long ids always                 | Resource ids are always long-form (`fs-`/`fsmt-`/`fsap-` plus 17 hex characters), whatever the account preference says                           |

## Modes

`OVERCAST_EFS_MODE` picks between them; `live` is the default.

| Mode | Behaviour |
| --- | --- |
| `live` | A Docker volume per file system. Without a reachable Docker daemon it creates nothing and behaves exactly like `mock` |
| `mock` | Metadata-only control plane; EFS touches Docker for nothing |

Set `mock` to keep EFS away from Docker even where a daemon is running.

## Gotchas

> [!WARNING]
> On startup, volumes are reconciled against persisted file systems, scoped to
> volumes carrying this instance's identity — so two Overcasts sharing a Docker
> daemon cannot delete each other's data. With the default `memory` state
> backend that identity is minted afresh on every start, so a restart sweeps
> nothing and orphans wait for
> `docker volume prune --filter label=overcast.managed=true`.

<!-- BEGIN overcast:capabilities -->

## Operations

28 of 31 listed operations are implemented.
Per-operation status, notes and AWS API links: [EFS operations](efs/operations.md).

<!-- END overcast:capabilities -->

## Related

- [EFS examples](./efs/examples.md) — mounting a file system over NFS
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [Lambda, ECS and VPCs](../networking/vpcs.md)
- [Storage and persistence](../storage.md)

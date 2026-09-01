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

EFS supports two modes:

- `live` (default): each file system is backed by a named Docker volume
  (`overcast-efs-<FileSystemId>`), created on `CreateFileSystem` and removed on
  `DeleteFileSystem`. On startup, volumes are reconciled against persisted file
  systems: missing volumes are recreated, and volumes this instance created
  whose file system is gone are removed. The sweep is scoped to volumes
  carrying this instance's identity, so two Overcasts sharing a Docker daemon
  cannot delete each other's file data; with the default `memory` state backend
  that identity is minted afresh on every start, so a restart sweeps nothing at
  all and orphans wait for `docker volume prune --filter
  label=overcast.managed=true`. `EFS_DOCKER_SOCKET` overrides the Docker socket (defaults to the
  Lambda socket). Volume operations are best-effort: the control plane keeps
  working when Docker is unavailable, and reconciliation heals the gap when it
  returns.
- `mock` (opt out with `OVERCAST_EFS_MODE=mock`): metadata-only control plane,
  and EFS touches Docker for nothing.

Live mode is the default because it asks nothing of a machine that cannot
provide it. It creates a volume only for a file system someone created, and
without a reachable Docker daemon it creates nothing at all and behaves exactly
like `mock` — so the setting to reach for is `mock`, only if you want EFS kept
away from Docker deliberately.

Within `live` mode, `OVERCAST_EFS_NFS=true` additionally opts into the NFS
data plane: each mount target runs an NFS-Ganesha container exporting its file
system's volume. See [Mounting over NFS](#mounting-over-nfs).

## Behavior notes

- **Data sharing works without NFS.** In `live` mode the backing volume is
  mounted into Lambda containers (`FileSystemConfigs`) and ECS task containers
  (`efsVolumeConfiguration`), scoped to the access point's root directory (or
  the ECS `rootDirectory`) via Docker volume subpaths — Docker Engine 26+
  required for subpath mounts. Because both sides mount the same named volume,
  a Lambda function and an ECS task genuinely share bytes with no NFS hop.
  Access points with `CreationInfo` have their root directory created in the
  volume with the declared ownership and permissions before the first mount;
  without `CreationInfo`, a missing directory makes the mount fail, as on
  AWS.
- In `mock` mode — and in `live` mode while no Docker daemon is reachable —
  there is no data plane at all: mount targets are metadata with deterministic
  synthesized network fields (availability zone, IP address, ENI ID derived
  from the subnet ID).
- `DescribeMountTargets.IpAddress` is always synthetic, including with NFS
  exports on — the export is reached through its published host port or the
  export network, never through that address.
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

## Mounting over NFS

`OVERCAST_EFS_NFS=true` (live mode only) gives every mount target a real,
mountable NFSv4 export. `CreateMountTarget` starts one NFS-Ganesha container
named `overcast-efs-nfs-<MountTargetId>` that exports the file system's volume
and publishes container port 2049 on a free host port at or above
`EFS_NFS_PORT_BASE`; `DeleteMountTarget` removes it. Ganesha runs entirely in
userspace — no `--privileged` and no kernel modules — so the export works on
Linux, macOS and Windows Docker hosts alike. The container is granted exactly
one Linux capability, `CAP_DAC_READ_SEARCH`: Ganesha's VFS backend resolves NFS
file handles with `open_by_handle_at()`, which the kernel gates on it. Without
it the export accepts one mount and then serves nothing.

It is opt-in because most testing does not need it: Lambda and ECS already
share bytes through the volume, and an export costs a container and a port per
mount target.

The mount target stays `creating` until the export answers an NFSv4 call, then
becomes `available` — so a successful `DescribeMountTargets` means the export
is genuinely serving. An export that never answers settles the mount target in
`error` instead, with a warning naming the container whose logs say why. The
resource is never stranded in `creating`, and it never reports `available`
without a data plane behind it. (With `OVERCAST_EFS_NFS` off there is no export
to fail: mount targets go straight to `available`, as before.)

Where to mount from:

| Client | Address |
| --- | --- |
| The Docker host | `localhost:<published port>` (`DescribeMountTargets` does not report it — read it from `docker ps`) |
| A sibling container | The mount target's DNS name on the shared data plane (`OVERCAST_NETWORK`), or the export container's address there |

Pseudo-paths follow the file system's access points: `/` is the volume root,
and `/<AccessPointId>` is that access point's root directory, squashed onto
its `PosixUser` when it declares one. Two consequences worth knowing:

- **An empty directory named after each access point appears in the file
  system root.** Ganesha grafts a pseudo-path onto a name that already exists
  in the exported volume, so the export container creates one anchor directory
  per access point before it starts. Mounting `/<AccessPointId>` shows the
  access point's root directory, never the anchor — the anchor is only visible
  to something listing the volume root.
- **Exports are fixed when the mount target starts.** Ganesha reloads exports
  only on restart, and churning a live NFS server on every `CreateAccessPoint`
  would break clients holding open files. An access point created after the
  mount target therefore has no pseudo-path: delete and recreate the mount
  target to pick it up. Its data is reachable in the meantime at the
  equivalent path under the root export (an access point rooted at `/app/data`
  is `/app/data` below `/`), just without the `PosixUser` squash.

Mounting the export requires `CAP_SYS_ADMIN` on the *client*, which is the
client's business — the server side needs only the one capability above.

| Variable | Default | Purpose |
| --- | --- | --- |
| `OVERCAST_EFS_NFS` | `false` | Opt into exports (needs live mode, which is the default) |
| `EFS_NFS_PORT_BASE` | `22049` | First host port considered for publishing 2049 |
| `EFS_NFS_IMAGE` | digest-pinned NFS-Ganesha | Override the export image |
| `OVERCAST_NETWORK` | `overcast` | Docker network the export containers join, shared with every other container Overcast starts |

## CloudFormation

`AWS::EFS::FileSystem`, `AWS::EFS::MountTarget`, and `AWS::EFS::AccessPoint`
are fully supported, including `FileSystemPolicy`, `LifecyclePolicies`,
`BackupPolicy`, `FileSystemProtection`, and tag updates. `Ref` returns the
resource ID; `GetAtt` supports `Arn`/`FileSystemId` (file system),
`Id`/`IpAddress` (mount target), and `AccessPointId`/`Arn` (access point).
Property changes that replace on real AWS (encryption, performance mode,
subnet, POSIX identity, …) trigger replacement here too.

<!-- BEGIN overcast:capabilities -->

## Operations

28 of 31 listed operations are implemented.
Per-operation status, notes and AWS API links: [EFS operations](efs/operations.md).

<!-- END overcast:capabilities -->

## Related

- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)

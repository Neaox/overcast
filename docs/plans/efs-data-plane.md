# EFS data plane — Docker-volume-backed live mode

Status: **complete**. Phase 1 — the full control plane
(`internal/services/efs`, CloudFormation handlers, docs, tests) — merged in
#421. Step 1 (named volumes) in #424, step 2 (Lambda `FileSystemConfigs`) in
#425, step 3 (ECS `efsVolumeConfiguration`) in #426, review fixes in #427,
access-point/task root directories via Docker volume subpaths in #429, and
step 4 (NFS export per mount target) in this change. The open questions are
resolved below under [Decisions](#decisions).

Findings that shaped the implementation (recorded when step 1 landed):

- `internal/docker` had no volume APIs; `CreateVolume`/`RemoveVolume`/
  `ListVolumes` were added with the standard managed labels so
  reconciliation and sweeps can find EFS volumes.
- Lambda has **no `FileSystemConfigs` modeling at all** (step 2 adds the
  wire fields as well as the mount), and ECS has no
  `efsVolumeConfiguration`; both have a single container-creation site
  (`lambda/container_runtime.go`, `ecs/handler_tasks.go`) where a named
  volume can be appended to `HostConfig.Binds` as `volume-name:/path`.
- Docker named volumes are directly usable in `Binds`; no bind-mount host
  paths are involved, so the same volume is shared by any container that
  names it.

## Goal

Make an emulated EFS file system hold real, shared file data that emulated
compute can read and write — so a Lambda function writing to
`/mnt/shared` and an ECS task reading the same access point actually share
bytes, and (opt-in) host/container clients can mount the file system over NFS.

## Non-goals

- Performance emulation (burst credits, provisioned throughput effects).
- Storage-class transitions actually moving data (IA/Archive stay metadata).
- Multi-AZ semantics; one backing volume per file system regardless of mount
  target count.
- Replication (stays `501` until there is a second region to replicate into).

## Design

Mirrors the EKS live-mode pattern: `OVERCAST_EFS_MODE=mock` (default,
today's behaviour) vs `OVERCAST_EFS_MODE=live`.

### 1. Named Docker volume per file system (foundation) — ✅ implemented

- `CreateFileSystem` (live mode, Docker available) creates a named volume
  `overcast-efs-<FileSystemId>`; the `DeleteFileSystem` deletion callback
  removes it (`internal/services/efs/live_volumes.go`).
- `SetDocker(*docker.Client)` + `dockerReady` (EKS pattern), wired in the
  router's Docker-probe block behind `OVERCAST_EFS_MODE=live`; the setter
  kicks off asynchronous volume reconciliation (recreate missing volumes
  across all regions, remove orphaned managed volumes).
- Docker unavailable ⇒ `CreateFileSystem` still succeeds metadata-only and
  logs a warning (same degradation the ECS/RDS services use), so live mode
  never breaks pure-control-plane CI.
- `EFS_DOCKER_SOCKET` (default: Lambda socket) selects the daemon.

### 2. Compute integration (the high-value step) — Lambda ✅ / ECS ✅ implemented

- **Lambda** (implemented): `FileSystemConfigs [{Arn, LocalMountPath}]` is
  modeled on CreateFunction / UpdateFunctionConfiguration /
  GetFunctionConfiguration with AWS's validation rules (one config max,
  access-point ARN, `/mnt/<name>` path) — this closed a control-plane gap
  that existed in every mode. The container runtime resolves each config
  through the `EFSVolumeResolver` interface (implemented by the EFS service,
  wired in the router) and binds `overcast-efs-<fsID>:<LocalMountPath>`;
  unresolvable configs (mock mode, Docker down, unknown access point) are
  skipped with a warning so the function still runs.
- **ECS** (implemented): task definitions model `volumes[]` (with
  `efsVolumeConfiguration`) and container `mountPoints[]`, validated at
  registration (mount points must reference a declared volume →
  `ClientException`). At task start each container's mount points resolve
  through ECS's own `EFSVolumeResolver` interface (by file system ID, in the
  request region) and bind `overcast-efs-<fsID>:<containerPath>[:ro]`.
  Non-EFS volumes are stored/echoed but not mounted.
- Because both sides mount the *same* named volume, cross-service data
  sharing works without any NFS hop. This is the piece that unlocks real
  application testing (shared caches, model files, upload scratch space).

### 3. NFS export per mount target (opt-in) — ✅ implemented

Off by default and gated on `OVERCAST_EFS_NFS=true` **and** live mode: Lambda
and ECS already share bytes through the named volume, so the export exists for
clients that genuinely speak NFS, and it costs a container plus a published
port per mount target. Implementation in `internal/services/efs/live_nfs.go`.

- `CreateMountTarget` starts one NFS-Ganesha container per mount target
  (`overcast-efs-nfs-<MountTargetId>`) exporting the file system's volume at
  `/export`, with container port 2049 published on the first free host port at
  or above `EFS_NFS_PORT_BASE` (default 22049). `DeleteMountTarget` stops and
  removes it and releases the port.
- **Image**: `registry.k8s.io/sig-storage/nfs-provisioner`, pinned by digest
  (`config.DefaultEFSNFSImage`, overridable with `EFS_NFS_IMAGE`). Overcast
  uses it purely as a carrier for `ganesha.nfsd` and its VFS FSAL: the
  entrypoint is replaced with a script that writes Overcast's own
  `ganesha.conf` and execs the daemon, so nothing depends on the image's
  provisioner behaviour. Multi-arch (amd64, arm64, ppc64le, s390x).
- **Unprivileged**: the export is NFSv4-only — no rpcbind, NLM, RQUOTA or UDP
  — so `ganesha.nfsd` binds 2049 and serves without `--privileged`, added
  capabilities, or kernel modules, on Linux, macOS and Windows Docker hosts.
- **Readiness** is an NFSv4 NULL call (program 100003, version 4, procedure 0)
  over TCP, retried on a bounded budget. A bare TCP dial is not a readiness
  signal: Docker's published port accepts connections as soon as the proxy is
  up, well before the daemon serves. The mount target stays `creating` until
  the export answers, then goes `available`; if the retries run out it goes
  `available` anyway with a warning, so a wedged export cannot strand the
  resource.
- **Reachability**: exports join `EFS_NETWORK` (default `overcast_efs`) as
  well as publishing a host port, so a containerized Overcast and sibling
  clients reach them by container IP while host clients use
  `localhost:<mapped-port>`. `DescribeMountTargets.IpAddress` stays synthetic.
- Access-point root directories map to per-export pseudo-paths:
  `/` is the volume root, `/<AccessPointId>` is that access point's root
  directory, with `Squash = All_Squash` plus `Anonymous_Uid`/`Anonymous_Gid`
  taken from its `PosixUser` (and `No_Root_Squash` without one).
- **Cleanup discipline** follows the container-lifecycle patterns from RDS
  (#412), ElastiCache (#459) and MSK/Lambda (#461): the start runs off the
  request path, so a delete can land mid-start when the record still carries
  no container ID — the start goroutine re-reads the record and, finding it
  gone or `deleting`, tears down the container it alone knows about.
  Reconciliation on `SetDocker` adopts running exports (container ID and
  published port) for surviving mount targets, restarts missing ones, and
  sweeps orphaned exports, stale one-shot root-directory helpers, and port
  reservations with no mount target behind them.

## Ordering and effort

1. Volumes + reconciliation + docs/tests — small, no new images. ✅
2. Lambda `FileSystemConfigs` mounting — medium; touches
   `internal/services/lambda` container start path and needs an integration
   test gated on Docker availability. ✅
3. ECS volume mounting — medium, same shape as (2). ✅
4. NFS-Ganesha mount targets — larger; needs image selection/pinning,
   readiness probing, port publishing, and cleanup discipline (follow the
   RDS container lifecycle patterns). ✅

## Known limitations (accepted)

- ~~Access-point root directories not honored~~ — **implemented**: mounts now
  use Docker's `Mounts` API with `VolumeOptions.Subpath` (Engine API v1.45).
  Access points with `CreationInfo` have their root directory materialized in
  the volume (mkdir/chown/chmod via a one-shot busybox helper container,
  deduplicated per process) before the first subpath mount; without
  `CreationInfo` — or for a plain ECS `rootDirectory` — a missing directory
  makes the mount fail, matching AWS. Requires Docker Engine 26+ (API v1.45)
  for subpath mounts.
- **Creation-token idempotency has a check-then-write race** under concurrent
  identical `CreateFileSystem` calls — the shared `state.Store` has no
  transactions, and every service carries the same class of race. Accepted.
- **Volume removal while a container still mounts the volume** fails with 409;
  the delete path retries a few times (30 s apart) and startup reconciliation
  is the backstop, so an orphan can outlive a session only if the emulator
  never restarts.
- **NFS exports are fixed when the mount target starts.** An access point
  created after its file system's mount target has no pseudo-path on that
  export until the mount target is recreated: Ganesha reloads exports only on
  restart, and bouncing a live NFS server on every `CreateAccessPoint` would
  drop clients holding open files. Volume-mount consumers (Lambda, ECS) are
  unaffected — they resolve access points per container start.
- **NFS clients need their own mount privileges.** The export is unprivileged;
  mounting it is not. A container that mounts it needs `CAP_SYS_ADMIN`, which
  is the client's business and outside what Overcast can arrange.

## Decisions

The three questions this plan opened are settled. All three landed where the
plan leaned, and each is recorded here rather than left as a question so the
next reader does not reopen it.

### `Backup`/restore does not snapshot the volume — **no**

`PutBackupPolicy`/`DescribeBackupPolicy` stay metadata: the policy is stored
and echoed, and nothing copies the volume. Restoring a snapshot is an AWS
Backup operation, and AWS Backup is not an emulated service — a backup policy
that produced no recovery point anyone could restore from would be a more
confusing half-truth than an honest metadata field. Users who want a copy of a
file system's contents have Docker's own volume tooling, which operates on
`overcast-efs-<FileSystemId>` directly.

### One Zone file systems behave the same — **no difference**

A One Zone file system gets one named volume, exactly like a Regional one. The
only One Zone behaviour Overcast models is control-plane: `AvailabilityZoneName`
is stored and echoed, mount targets are pinned to the file system's own zone,
and `maxIO` is rejected. There is no second AZ to be in, so durability and
placement have nothing to differ about, and inventing a distinction would make
One Zone look meaningfully different from Regional when it is not.

### uid/gid is applied at creation, not enforced per request — **no enforcement**

An access point's `CreationInfo` sets ownership and permissions on its root
directory when that directory is materialized (`live_subdir.go`), and its
`PosixUser` becomes `Squash`/`Anonymous_Uid`/`Anonymous_Gid` on the NFS export
so NFS clients are squashed onto it. Beyond that, identity is not enforced
per request on the volume-mount path: Lambda and ECS containers mount the
volume directly and read and write as whatever user the container runs as.

Enforcing per-request identity there would mean interposing on file I/O
between the container and the volume, which needs either a FUSE layer or a
privileged mount helper — a large amount of machinery for a fidelity gain that
almost no test depends on, in a tool that is explicitly not a security
boundary. Applications testing against Overcast see the ownership their access
point declares; they do not see `EACCES` for using the wrong uid.

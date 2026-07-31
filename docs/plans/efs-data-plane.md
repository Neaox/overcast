# EFS data plane — Docker-volume-backed live mode

Status: **step 1 implemented** (volumes as the storage primitive); steps 2–4
planned. Phase 1 — the full control plane (`internal/services/efs`,
CloudFormation handlers, docs, tests) — merged in #421.

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
bytes, and (opt-in) host/сontainer clients can mount the file system over NFS.

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

### 3. NFS export per mount target (opt-in, later)

- `CreateMountTarget` (live mode) starts one NFS-Ganesha container
  (userspace NFS — no privileged mode or kernel modules) exporting the
  volume, port 2049 published on a dynamic host port; `DeleteMountTarget`
  stops it.
- `DescribeMountTargets.IpAddress` stays synthetic; a new behaviour note
  documents mounting via `localhost:<mapped-port>` (host) or the container
  network (siblings).
- Access-point root directories map to per-export pseudo-paths with
  `Squash`/`Anonymous_Uid` from `PosixUser`.

## Ordering and effort

1. Volumes + reconciliation + docs/tests — small, no new images.
2. Lambda `FileSystemConfigs` mounting — medium; touches
   `internal/services/lambda` container start path and needs an integration
   test gated on Docker availability.
3. ECS volume mounting — medium, same shape as (2).
4. NFS-Ganesha mount targets — larger; needs image selection/pinning,
   readiness probing, port publishing, and cleanup discipline (follow the
   RDS container lifecycle patterns).

## Known limitations (accepted or in flight)

- **Access-point root directories are not yet honored by mounts** — Lambda/ECS
  containers currently see the volume root regardless of the access point's
  `RootDirectory` (or ECS `rootDirectory`). Docker's `Mounts` API
  (`VolumeOptions.Subpath`, Engine API v1.45) is the implementation path;
  being addressed as its own change.
- **Creation-token idempotency has a check-then-write race** under concurrent
  identical `CreateFileSystem` calls — the shared `state.Store` has no
  transactions, and every service carries the same class of race. Accepted.
- **Volume removal while a container still mounts the volume** fails with 409;
  the delete path retries a few times (30 s apart) and startup reconciliation
  is the backstop, so an orphan can outlive a session only if the emulator
  never restarts.

## Open questions

- Whether `Backup`/restore should snapshot the volume (probably not; AWS
  Backup is out of scope).
- Whether One Zone file systems should behave differently (no — one volume
  either way).
- uid/gid enforcement fidelity for access points on shared volumes (initial
  version: apply `CreationInfo` ownership/permissions on first mount, do not
  enforce per-request identity).

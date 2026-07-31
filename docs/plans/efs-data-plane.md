# EFS data plane — Docker-volume-backed live mode

Status: **planned** (phase 2 of EFS support; not started).
Phase 1 — the full control plane (`internal/services/efs`, CloudFormation
handlers, docs, tests) — is merged; this document scopes what "more than
metadata" means for EFS and how it should be built.

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

### 1. Named Docker volume per file system (foundation)

- `CreateFileSystem` (live mode, Docker available) creates a named volume
  `overcast-efs-<FileSystemId>`; `DeleteFileSystem` removes it.
- Service gains `SetDocker(*docker.Client)` + `dockerReady` (EKS pattern) and
  implements `router.ContainerReconciler`-style startup reconciliation for
  volumes (`serviceutil.ScanRegions` over persisted file systems: recreate
  missing volumes, mark orphans).
- Docker unavailable ⇒ `CreateFileSystem` still succeeds metadata-only and
  logs a warning (same degradation the ECS/RDS services use), so live mode
  never breaks pure-control-plane CI.

### 2. Compute integration (the high-value step)

- **Lambda**: functions created with `FileSystemConfigs` (already accepted by
  the Lambda control plane as metadata) get the file system's named volume
  mounted at `LocalMountPath` when their runtime container starts. Root
  directory + POSIX identity come from the referenced access point ARN.
- **ECS**: task definitions with an `efsVolumeConfiguration` mount the same
  named volume into task containers.
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

## Open questions

- Whether `Backup`/restore should snapshot the volume (probably not; AWS
  Backup is out of scope).
- Whether One Zone file systems should behave differently (no — one volume
  either way).
- uid/gid enforcement fidelity for access points on shared volumes (initial
  version: apply `CreationInfo` ownership/permissions on first mount, do not
  enforce per-request identity).

---
section: Added
area: efs
---

- [efs] live-mode mounts now honor root directories: Lambda `FileSystemConfigs` mounts are scoped to the access point's `RootDirectory`, and ECS `efsVolumeConfiguration` mounts to the access point's root or the declared `rootDirectory`, via Docker volume subpath mounts (Docker Engine 26+). Access points with `CreationInfo` have the directory created in the volume with the declared ownership and permissions before the first mount; without `CreationInfo` a missing directory fails the mount, matching AWS
- [ecs] `RegisterTaskDefinition` now rejects `efsVolumeConfiguration` combining an `accessPointId` with a `rootDirectory` other than `/`, matching AWS

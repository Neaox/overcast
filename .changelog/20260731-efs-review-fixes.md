---
section: Fixed
area: efs
---

- [efs] `DescribeFileSystems` now rejects requests carrying both `FileSystemId` and `CreationToken` with `BadRequest`, matching AWS
- [efs] `DescribeFileSystems` amortizes one mount-target scan across the whole page instead of scanning per file system
- [efs] deleting a file system whose volume is still held by a running container now retries the volume removal (3 attempts, 30 s apart) before deferring to startup reconciliation

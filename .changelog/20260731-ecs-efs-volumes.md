---
section: Added
area: ecs
---

- [ecs] task definitions now model `volumes` (including `efsVolumeConfiguration`) and container `mountPoints`, with AWS's undefined-volume validation; when EFS live mode is active, task containers mount the file system's backing Docker volume at each mount point (honoring `readOnly`), sharing real file data with Lambda functions that mount the same file system

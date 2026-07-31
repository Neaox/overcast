---
section: Added
area: lambda
---

- [lambda] `FileSystemConfigs` is now modeled on CreateFunction, UpdateFunctionConfiguration, and GetFunctionConfiguration (one config max, EFS access-point ARN, `/mnt/<name>` mount path — matching AWS validation); when EFS live mode is active, the function's containers mount the backing Docker volume at `LocalMountPath`, so invocations share real file data with each other and with other services mounting the same file system

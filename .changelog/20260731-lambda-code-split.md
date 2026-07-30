---
section: Changed
area: lambda
---

- [lambda] deployment packages are stored separately from function records, so invoke-path reads no longer base64-decode the whole zip on every invocation (and S3 code-sync events no longer decode every function's package); existing records migrate automatically on their next write

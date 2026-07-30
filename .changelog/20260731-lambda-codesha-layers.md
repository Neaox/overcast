---
section: Fixed
area: lambda
---

- [lambda] `CodeSha256` in function and version responses is now the base64-encoded SHA-256 AWS returns, not hex — tooling that compares its locally computed hash (CDK change detection among them) no longer sees permanent code drift
- [lambda] layer version responses now populate `Content.CodeSha256` (base64, as on AWS); layers published before this release omit it
- [lambda] layer archives are stored separately from layer records, so listing layers no longer base64-decodes every layer's full zip per call

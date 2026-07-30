---
section: Changed
area: lambda
---

- [lambda] the invoke path no longer SHA-256s the whole deployment package on every invocation (and every configuration read) — the hash is computed once when code is written and stored on the function record, cutting per-invoke CPU for large packages

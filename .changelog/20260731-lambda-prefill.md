---
section: Changed
area: lambda
---

- [lambda] cold-start artifacts (code and layer tars) are pre-built in the background once a deploy settles, so even the first cold start of a new code version skips the package fetch and conversion; the artifact cache is reported on the `/_lambda/instances` debug endpoint

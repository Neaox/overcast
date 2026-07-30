---
section: Changed
area: lambda
---

- [lambda] cold starts provision the container filesystem with a single archive (code, layers, TLS trust root, bootstrap) instead of up to four sequential Docker copy round trips — measured cold-start p50 ~355 ms → ~300 ms for cached-image hello-world functions

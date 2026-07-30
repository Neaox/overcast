---
section: Changed
area: lambda
---

- [lambda] REPORT lines' `Max Memory Used` now reports the execution environment's running peak across warm invocations — matching AWS — and is sampled concurrently with handler execution, so writing the REPORT line no longer holds up the invoke response waiting on Docker stats

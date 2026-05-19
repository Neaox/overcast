---
name: Bug report
about: An endpoint returns wrong behaviour, wrong format, or an unexpected error
title: 'Bug: <service> <operation> <short symptom>'
labels: bug, area/emulation, status/needs-triage
assignees: ''
---

## Service and operation

<!-- e.g. S3 / PutObject, SQS / ReceiveMessage -->

**Service:**
**Operation:**
**Status in support matrix:** <!-- ✅ Supported / ⚠️ Partial / check docs/services/<service>.md -->

---

## What happened

<!-- Describe what went wrong. Include the error code and message if there was one. -->

## What was expected

<!-- What should have happened? Reference AWS docs if helpful. -->

## Reproduction

<!-- Minimal reproduction case. CLI commands or a short code snippet are ideal. -->

```bash
# AWS CLI example:
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test

aws <command>
```

## Environment

| | |
|--|--|
| Overcast version | <!-- `overcast --version` or Docker image tag --> |
| Run mode | Docker / binary / `make run` |
| `OVERCAST_STATE` | memory / sqlite |
| OS | |
| AWS SDK / CLI version | |

## Debug output

<!-- Run with OVERCAST_LOG_LEVEL=debug and paste the relevant log lines -->

<details>
<summary>Debug log</summary>

```
paste here
```
</details>

---

<!--
Priority labels (add one):
  priority/p1 - Blocks a core use case (affects a supported endpoint)
  priority/p2 - Affects a common workflow but has a workaround
  priority/p3 - Minor issue or edge case

Effort labels (added by maintainers after triage):
  effort/small  - <2h
  effort/medium - 2-8h
  effort/large  - >8h, likely needs design discussion
-->

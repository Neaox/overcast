---
name: Web UI issue
about: Report or request work for the Overcast web UI, dashboard, frontend routing, or BFF-backed UI behavior
title: 'Web UI: <page/component> <short symptom or change>'
labels: area/web-ui, status/needs-triage
assignees: ''
---

## Scope

<!-- Identify where the issue appears. -->

**Page/route:**
**Component/feature:**
**Related service:** <!-- Optional, e.g. Lambda, SQS, S3 -->
**Area:** <!-- web UI / BFF / both -->

## What happened

<!-- Describe the bug, missing behavior, or requested change. -->

## What was expected

<!-- Describe the expected UI behavior. -->

## Reproduction

<!-- Include exact steps, seed data, commands, or browser actions. -->

1.
2.
3.

## Environment

| | |
|--|--|
| Overcast version | <!-- `overcast --version` or Docker image tag --> |
| Run mode | Docker / binary / `make run` |
| Browser | |
| Viewport/device | Desktop / mobile / width if relevant |
| OS | |

## Evidence

<!-- Add screenshots, console errors, network responses, or logs when useful. -->

<details>
<summary>Logs or screenshots</summary>

```text
paste here
```
</details>

## Completion Criteria

- [ ] UI behavior is fixed or implemented
- [ ] Mobile and desktop behavior are checked when layout is affected
- [ ] BFF/API behavior is covered separately if the root cause is backend behavior
- [ ] Relevant tests or manual verification steps are recorded

<!-- Suggested labels: area/bff if the issue is in Go BFF routes, service/<name> when tied to a service, bug, enhancement, priority/p2, effort/small -->

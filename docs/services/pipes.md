---
title: "EventBridge Pipes — endpoint support"
description: "Notes"
section: "Service Reference"
tags:
  - docs
  - endpoint
  - eventbridge
  - pipes
  - services
  - support
---

# EventBridge Pipes — endpoint support

> AWS docs: [EventBridge Pipes API Reference](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/Welcome.html)

EventBridge Pipes uses REST-JSON under the `/v1/pipes` path prefix.
Overcast supports full pipe CRUD and DynamoDB Streams → SQS delivery.

---

## Notes

- **DynamoDB Streams → SQS only.** Pipes subscribe to DynamoDB stream events via the internal
  event bus and deliver to SQS targets. Other source/target combinations are not yet supported.
- **Async state machine.** Pipe state transitions (CREATING→RUNNING, STOPPING→STOPPED, etc.)
  happen asynchronously with a short delay.
- **Start/stop.** Setting `DesiredState` to `STOPPED` or `RUNNING` on update triggers the
  appropriate state transition.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| Pipes    | 5            |

---

## Endpoints

### Pipes

| Operation      | Status       | Notes                                               | AWS Docs                                                                                     |
| -------------- | ------------ | --------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `CreatePipe`   | ✅ Supported | Creates with async state machine (CREATING→RUNNING) | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_CreatePipe.html)   |
| `DescribePipe` | ✅ Supported | Returns pipe details and current state              | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_DescribePipe.html) |
| `UpdatePipe`   | ✅ Supported | Updates DesiredState and Description                | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_UpdatePipe.html)   |
| `DeletePipe`   | ✅ Supported | Async deletion (DELETING→removed)                   | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_DeletePipe.html)   |
| `ListPipes`    | ✅ Supported | Lists all pipes                                     | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_ListPipes.html)    |

<!-- END overcast:capabilities -->

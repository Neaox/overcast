---
title: "Step Functions operations"
description: "Every Step Functions operation Overcast declares — 15 of 15 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - services
  - stepfunctions
---

<!-- BEGIN overcast:capabilities -->

# Step Functions operations

All 15 listed operations are implemented. Back to [Step Functions](../stepfunctions.md).

## Summary

| Category       | ✅ Supported |
| -------------- | ------------ |
| State machines | 6            |
| Executions     | 6            |
| Tags           | 3            |

---

## Endpoints

### State machines

| Operation                          | Status       | Notes                                                                                 | AWS Docs                                                                                                         |
| ---------------------------------- | ------------ | ------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `CreateStateMachine`               | ✅ Supported | Validates the ASL; idempotent — returns existing if name+def match                    | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_CreateStateMachine.html)               |
| `DescribeStateMachine`             | ✅ Supported |                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DescribeStateMachine.html)             |
| `ListStateMachines`                | ✅ Supported |                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_ListStateMachines.html)                |
| `DeleteStateMachine`               | ✅ Supported |                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DeleteStateMachine.html)               |
| `UpdateStateMachine`               | ✅ Supported | Definition/roleArn/loggingConfiguration/tracingConfiguration; no versioning (publish) | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_UpdateStateMachine.html)               |
| `DescribeStateMachineForExecution` | ✅ Supported |                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DescribeStateMachineForExecution.html) |

### Executions

| Operation             | Status       | Notes                                                                                                                                                                                                                          | AWS Docs                                                                                            |
| --------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `StartExecution`      | ✅ Supported | Interprets the ASL; returns while the execution is RUNNING, as AWS does; a standard workflow's RUNNING and terminal status transitions each emit a Step Functions Execution Status Change event to the default EventBridge bus | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_StartExecution.html)      |
| `StartSyncExecution`  | ✅ Supported | EXPRESS only — same interpreter, run to completion before returning; EXPRESS executions do not emit EventBridge events, matching AWS                                                                                           | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_StartSyncExecution.html)  |
| `DescribeExecution`   | ✅ Supported | Real status, output, error and cause                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DescribeExecution.html)   |
| `ListExecutions`      | ✅ Supported | statusFilter (validated against the ExecutionStatus enum) and maxResults honoured; no pagination token                                                                                                                         | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_ListExecutions.html)      |
| `GetExecutionHistory` | ✅ Supported | Real state-transition events in AWS's vocabulary; readable while RUNNING                                                                                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_GetExecutionHistory.html) |
| `StopExecution`       | ✅ Supported | Interrupts a running execution; it reaches ABORTED asynchronously; a standard workflow's ABORTED transition emits a Step Functions Execution Status Change event to the default EventBridge bus                                | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_StopExecution.html)       |

### Tags

| Operation             | Status       | Notes | AWS Docs                                                                                            |
| --------------------- | ------------ | ----- | --------------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported |       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->

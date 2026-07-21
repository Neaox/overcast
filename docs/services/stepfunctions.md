---
title: "Step Functions — endpoint support"
description: "Step Functions accepts AWS JSON 1.0 via X-Amz-Target: AWSStepFunctions.\u003coperation\u003e. It also accepts Smithy RPC v2 CBOR at /service/StepFunctions/operation/\u003coperation\u003e with..."
section: "Service Reference"
tags:
  - docs
  - endpoint
  - functions
  - services
  - step
  - stepfunctions
  - support
---

# Step Functions — endpoint support

> AWS docs: [Step Functions API Reference](https://docs.aws.amazon.com/step-functions/latest/apireference/Welcome.html)

Step Functions accepts AWS JSON 1.0 via `X-Amz-Target:
AWSStepFunctions.<operation>`. It also accepts Smithy RPC v2 CBOR at
`/service/StepFunctions/operation/<operation>` with `Smithy-Protocol:
rpc-v2-cbor` and `Content-Type: application/cbor`. Overcast implements state
machine CRUD and basic execution tracking — executions are recorded but **not
actually executed** (they immediately succeed).

> [!WARNING]
> **Emulation tier: Inert** — state machines are stored and can be managed, but
> executions are **no-op**: they complete immediately without running any states,
> invoking any Lambdas, or producing any side effects.

---

## Notes

- **No execution engine.** `StartExecution` creates an execution record that immediately transitions to `SUCCEEDED`. The state machine definition (ASL) is stored but not interpreted.
- **Idempotent creation.** `CreateStateMachine` returns the existing state machine if the name, definition, role ARN, and type all match.
- **CDK compatible.** Sufficient for CDK deployments that create state machines and trigger executions.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category       | ✅ Supported |
| -------------- | ------------ |
| State machines | 4            |
| Executions     | 1            |

---

## Endpoints

### State machines

| Operation              | Status       | Notes                                           | AWS Docs                                                                                             |
| ---------------------- | ------------ | ----------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `CreateStateMachine`   | ✅ Supported | Idempotent — returns existing if name+def match | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_CreateStateMachine.html)   |
| `DescribeStateMachine` | ✅ Supported |                                                 | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DescribeStateMachine.html) |
| `ListStateMachines`    | ✅ Supported |                                                 | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_ListStateMachines.html)    |
| `DeleteStateMachine`   | ✅ Supported |                                                 | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DeleteStateMachine.html)   |

### Executions

| Operation        | Status       | Notes                                          | AWS Docs                                                                                       |
| ---------------- | ------------ | ---------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `StartExecution` | ✅ Supported | Records execution; immediately marks SUCCEEDED | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_StartExecution.html) |

<!-- END overcast:capabilities -->

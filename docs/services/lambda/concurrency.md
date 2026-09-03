---
title: "Lambda concurrency"
description: "How many containers Overcast will run for a function, what happens to an invocation that cannot get one, and how reserved and provisioned concurrency behave."
section: "Service Reference"
tags:
  - concurrency
  - docs
  - lambda
  - services
---

# Lambda concurrency

How many containers [Lambda](../lambda.md) will run at once, and what happens to
an invocation that cannot get one.

## The limits

These size the pool to your Docker host. They are not AWS's account quota, which
Overcast does not emulate.

| Limit | Environment variable | Default | When reached |
| --- | --- | --- | --- |
| Containers across all functions | `LAMBDA_MAX_INSTANCES` | auto (25 fallback) | Reclaim an idle container → queue → throttle |
| Containers for one function | `LAMBDA_MAX_INSTANCES_PER_FUNCTION` | auto (10 fallback) | Queue → throttle |
| Aggregate container memory (Σ `MemorySize`) | `LAMBDA_MAX_MEMORY_MB` | auto (unlimited fallback) | Reclaim an idle container → queue → throttle |
| Idle containers kept per function | `LAMBDA_MAX_WARM_INSTANCES` | 10 | Surplus destroyed on release |
| Concurrent container starts | `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | auto (4 fallback) | Starts queue behind a semaphore |
| `ReservedConcurrentExecutions` | per function, AWS API | unset | Throttle immediately |

Derivations and every other `LAMBDA_*` variable are in
[Configuration](../../configuration.md).

An invocation that cannot get a container first reclaims the least-recently-used
idle one — never a provisioned one — and then waits. If it is still waiting when
the function's timeout expires it is throttled, with a 429
`TooManyRequestsException` and `Reason: ConcurrentInvocationLimitExceeded`.

## The memory budget

`LAMBDA_MAX_MEMORY_MB` counts bytes, not containers: a new container is admitted
only while `Σ MemorySize` of live containers plus its own stays inside the
budget. Each container is hard-capped at its function's `MemorySize` with swap
disabled, so the sum is a real bound.

Above ~90% of the budget the pool changes behaviour: a container whose invocation
has just finished is destroyed rather than kept warm, so queued work regains
budget without waiting for the 15-minute idle sweep. Provisioned environments are
always kept and a running invocation is never interrupted. Entering and leaving
that regime is logged once each — expect cold starts while it lasts.

## Reserved concurrency

`ReservedConcurrentExecutions` is enforced with AWS's semantics rather than the
pool's: no queueing, an immediate 429 with
`Reason: ReservedFunctionConcurrentInvocationLimitExceeded`. Setting it to 0
disables the function, the same idiom that works on AWS.

Asynchronous invocations are never throttled back to the caller — they were
already answered 202 — and are retried internally. Event source mappings behave
the same way: a throttled batch is left in flight, so the messages return on the
visibility timeout. [Event delivery and retries](./async.md) covers the retry
budget and where an exhausted event ends up.

## Provisioned concurrency

Environments are created in the background (`Status: IN_PROGRESS`, then `READY`),
held open regardless of the idle sweep, replenished when one is lost, and rebuilt
against the new configuration after a code or config update. Containers report
`AWS_LAMBDA_INITIALIZATION_TYPE=provisioned-concurrency`, which is what Powertools
and similar libraries read to classify a cold start.

Provisioned concurrency is a **floor, not a ceiling**: when all reserved
environments are busy, further invocations spill over into on-demand capacity with
a cold start rather than being throttled, matching AWS, where only reserved
concurrency caps a function. A reservation is never reclaimed for another
function. Without Docker nothing can be allocated, so the configuration is stored
and reported `Status: FAILED` with a `StatusReason` rather than claiming `READY`.

## Related

- [Lambda](../lambda.md) — quick start and what works
- [Lambda limitations](./limitations.md) — the divergence table
- [Lambda execution environments](./execution-environments.md) — when a warm container is retired
- [Lambda event delivery and retries](./async.md) — what happens to a throttled event
- [Lambda troubleshooting](./troubleshooting.md) — throttles, layer errors, extension endpoints
- [Configuration reference](../../configuration.md) — every `LAMBDA_*` environment variable

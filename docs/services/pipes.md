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
Overcast supports full pipe CRUD, three source types, a Lambda enrichment step, and seven
target types.

---

## Supported wiring

| Leg | Supported | Refused at `CreatePipe`/`UpdatePipe` |
| --- | --- | --- |
| **Source** | DynamoDB Streams, Kinesis streams, SQS queues | everything else, including Amazon MQ and Kafka |
| **Enrichment** | a Lambda function (optional) | API destinations, API Gateway, Step Functions |
| **Target** | Lambda, SQS, SNS, Step Functions, Kinesis, Firehose, EventBridge event bus | ECS tasks, Batch, CloudWatch Logs, Redshift Data, SageMaker, Timestream, API destinations |

A combination Overcast cannot run is rejected with a `ValidationException` naming the offending
field, so a pipe is never stored in a state where it would silently do nothing.

## Notes

- **Batches.** A source produces a JSON array of records. A target with a batch API (SQS, SNS,
  Kinesis, Firehose, EventBridge bus) receives one entry per record; Lambda and Step Functions
  receive the whole array in a single request, as they do on AWS. `BatchSize` applies to the
  polled sources; a DynamoDB Streams source is driven one record at a time by the internal event
  bus, so it always runs a batch of one.
- **Enrichment.** The Lambda enrichment is invoked synchronously and its return value *replaces*
  the batch. An empty response (`""`, `{}`, `[]`, `null`) drops the batch without invoking the
  target; `[{}]` invokes the target with an empty payload.
- **Input transformation.** `EnrichmentParameters.InputTemplate` and
  `TargetParameters.InputTemplate` are applied to each record individually, over the JSON-path
  subset AWS accepts plus the `aws.pipes.*` reserved variables.
- **Polling.** Kinesis and SQS sources are polled once per second on the emulator's clock. A
  failed execution leaves SQS messages to become visible again and does not advance the Kinesis
  shard cursor, so the batch is retried.
- **DynamoDB Streams retries and dead-lettering.** A stream record arrives over the internal
  event bus and is gone once the subscriber returns — there is no cursor to leave alone and no
  message to let reappear — so a failed delivery is retried in place up to
  `SourceParameters.DynamoDBStreamParameters.MaximumRetryAttempts` (capped at 5 retries; unset
  means AWS's "retry until the record expires", which the cap stands in for, and an explicit `0`
  means one attempt). Once the retries are exhausted the batch is sent to the source's
  `DeadLetterConfig` SQS queue or SNS topic if one is configured, and otherwise reported as
  `failed` on the delivery feed with a logged warning. Overcast dead-letters the source records
  themselves rather than AWS's shard/sequence-range failure envelope, because a DynamoDB-sourced
  pipe here is bus-driven and keeps no shard bookkeeping — the records are what makes the batch
  replayable.
- **Filtering is not emulated.** `SourceParameters.FilterCriteria` is rejected rather than stored
  and ignored — filter inside a Lambda enrichment instead.
- **`UpdatePipe` is `PUT /v1/pipes/{name}`**, as AWS routes it, and requires `RoleArn`.
- **Async state machine.** Pipe state transitions (CREATING→RUNNING, UPDATING→RUNNING,
  STOPPING→STOPPED, …) happen asynchronously with a short delay.
- **Start/stop.** Setting `DesiredState` to `STOPPED` or `RUNNING` on update triggers the
  appropriate state transition.
- **Partial batch responses.** A **Lambda** target's response is read for a
  `batchItemFailures` report, and the records it names are the only ones redelivered: an SQS source
  deletes the rest and leaves the reported messages to become visible again, a Kinesis source moves
  its shard cursor to just before the earliest reported record, and a DynamoDB Streams source
  retries from that record onwards. A report Overcast cannot honour — invalid JSON, an empty or
  missing `itemIdentifier`, an identifier naming a record that was not in the batch — retries the
  whole batch and is logged with the reason. A pipe has no `FunctionResponseTypes` to opt in with,
  so a response that never mentions `batchItemFailures` is left alone rather than second-guessed;
  that is what keeps a target returning a non-JSON payload succeeding as it always did. **Two
  places still do not report:** a *Step Functions* target, whose asynchronous `StartExecution` has
  no response to read, and a Lambda **enrichment**, whose return value AWS defines as *replacing*
  the batch rather than reporting on it. AWS offers no partial-batch reporting for any other target
  type.
- **Stored but not acted on.** `LogConfiguration`, `KmsKeyIdentifier` and `ParallelizationFactor`
  have no effect, and a *Kinesis* source's
  `MaximumRetryAttempts` and `DeadLetterConfig` do not either — a Kinesis batch is retried by not
  advancing the shard cursor, so it never runs out of attempts and never reaches a dead-letter
  queue. `RoleArn` is required, as AWS requires it, but is not evaluated — Overcast is not a
  security boundary.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| Pipes    | 5            |
| Tags     | 3            |

---

## Endpoints

### Pipes

| Operation      | Status       | Notes                                                                                      | AWS Docs                                                                                     |
| -------------- | ------------ | ------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| `CreatePipe`   | ✅ Supported | Validates source/enrichment/target wiring up front; async state machine (CREATING→RUNNING) | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_CreatePipe.html)   |
| `DescribePipe` | ✅ Supported | Returns the full pipe configuration and current state                                      | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_DescribePipe.html) |
| `UpdatePipe`   | ✅ Supported | Updates DesiredState, description, role and parameter blocks (UPDATING→previous state)     | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_UpdatePipe.html)   |
| `DeletePipe`   | ✅ Supported | Async deletion (DELETING→removed)                                                          | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_DeletePipe.html)   |
| `ListPipes`    | ✅ Supported | Lists all pipes as PipeSummary                                                             | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_ListPipes.html)    |

### Tags

| Operation             | Status       | Notes                               | AWS Docs                                                                                            |
| --------------------- | ------------ | ----------------------------------- | --------------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Adds/merges tags by pipe ARN        | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tags by key from a pipe ARN | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns tags for a pipe ARN         | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->

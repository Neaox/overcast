---
title: "Pipes — Amazon EventBridge Pipes"
description: "Quick start, the source, enrichment and target combinations accepted, batching, transformation and partial batch failures, and the fields dropped on decode."
section: "Service Reference"
tags:
  - docs
  - eventbridge
  - pipes
  - services
---

# Pipes — Amazon EventBridge Pipes

Point-to-point wiring from a source, through an optional Lambda enrichment, to a
target — polled and delivered for real.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

SRC=$(aws sqs create-queue --queue-name inbox --query QueueUrl --output text)
SRC_ARN=$(aws sqs get-queue-attributes --queue-url "$SRC" \
  --attribute-names QueueArn --query Attributes.QueueArn --output text)
DST=$(aws sns create-topic --name fanout --query TopicArn --output text)

aws pipes create-pipe --name inbox-to-topic \
  --role-arn arn:aws:iam::000000000000:role/pipes \
  --source "$SRC_ARN" --target "$DST"

aws sqs send-message --queue-url "$SRC" --message-body '{"id":1}'
aws pipes describe-pipe --name inbox-to-topic
```

## What works

| Leg | Supported | Refused at `CreatePipe`/`UpdatePipe` |
| --- | --- | --- |
| **Source** | DynamoDB Streams, Kinesis streams, SQS queues | Everything else, including Amazon MQ and Kafka |
| **Enrichment** | A Lambda function (optional) | API destinations, API Gateway, Step Functions |
| **Target** | Lambda, SQS, SNS, Step Functions, Kinesis, Firehose, EventBridge event bus | ECS tasks, Batch, CloudWatch Logs, Redshift Data, SageMaker, Timestream, API destinations |

A combination Overcast cannot run is rejected with a `ValidationException` naming
the offending field, so a pipe is never stored in a state where it would silently
do nothing.

| Area | Behaviour |
| --- | --- |
| Batching | A source produces a JSON array of records. A target with a batch API receives one entry per record; Lambda and Step Functions receive the whole array in one request, as on AWS. `BatchSize` applies to the polled sources. |
| Enrichment | Invoked synchronously; its return value *replaces* the batch. An empty response (`""`, `{}`, `[]`, `null`) drops the batch without invoking the target; `[{}]` invokes the target with an empty payload. |
| Input transformation | `EnrichmentParameters.InputTemplate` and `TargetParameters.InputTemplate` are applied per record, over the JSON-path subset AWS accepts plus the `aws.pipes.*` reserved variables. |
| Polling | Kinesis and SQS sources are polled once per second on the emulator's clock. |
| Partial batch responses | A **Lambda target's** response is read for `batchItemFailures`, and only the records it names are redelivered. |
| Lifecycle | State transitions (`CREATING`→`RUNNING`, `STOPPING`→`STOPPED`, …) happen asynchronously with a short delay; setting `DesiredState` on update triggers one. |

## Differences from AWS

| Area                                   | On AWS                                  | Overcast                                                                                                                             |
| -------------------------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `FilterCriteria`                       | Server-side filtering                   | Rejected outright rather than stored and ignored — filter inside a Lambda enrichment instead                                         |
| Stream retries                         | Retry until expiry                      | Capped at 5 retries (an explicit `0` means one attempt); unset means the cap, standing in for AWS's "retry until the record expires" |
| Dead-letter payload                    | A shard/sequence-range failure envelope | The source records themselves, so the batch is replayable without a second read                                                      |
| DynamoDB Streams batching              | `BatchSize` applies                     | Bus-driven one record at a time, so always a batch of one                                                                            |
| SQS dead-lettering                     | Same                                    | Left to the queue's own `RedrivePolicy`, as on AWS — `DeadLetterConfig` does not apply to an SQS source                              |
| `ParallelizationFactor`                | Honoured                                | Stored and never read                                                                                                                |
| `LogConfiguration`, `KmsKeyIdentifier` | Stored and honoured                     | Not modelled — discarded on decode and absent from `DescribePipe`                                                                    |
| `RoleArn`                              | Enforced                                | Required, as AWS requires it, but never evaluated                                                                                    |

Once a stream source's retries are exhausted the batch goes to the source's
`DeadLetterConfig` SQS queue or SNS topic if one is configured, and is otherwise
reported failed with a logged warning while the cursor moves on rather than
blocking forever. A `DeadLetterConfig` naming anything but an SQS queue or SNS
topic is refused at create and update time.

## Gotchas

> [!WARNING]
> `LogConfiguration` and `KmsKeyIdentifier` are dropped, not stored inert. A
> `DescribePipe` assertion that round-trips either field will fail.

Only a Lambda **target** reports partial batch failures. A Step Functions
target's asynchronous `StartExecution` has no response to read, and a Lambda
**enrichment**'s return value AWS defines as *replacing* the batch rather than
reporting on it. AWS offers no partial-batch reporting for any other target
type either.

<!-- BEGIN overcast:capabilities -->

## Operations

All 8 listed operations are implemented.
Per-operation status, notes and AWS API links: [Pipes operations](pipes/operations.md).

<!-- END overcast:capabilities -->

## Related

- [EventBridge](./eventbridge.md) — the same targets, driven by event patterns
- [Scheduler](./scheduler.md) — the same targets, driven by a clock
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/Welcome.html)

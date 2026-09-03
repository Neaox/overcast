---
title: "Lambda event delivery and retries"
description: "What happens to an event a Lambda function did not answer: async retry attempts, destinations and DLQs, event age, and partial batch responses from event source mappings."
section: "Service Reference"
tags:
  - docs
  - lambda
  - retries
  - services
---

# Lambda event delivery and retries

What happens to an event [Lambda](../lambda.md) accepted but the function did not
successfully handle — a retry, a destination, a dead-letter queue, or a message
left in flight.

## Asynchronous invocation

`PutFunctionEventInvokeConfig` and its family are implemented, so
`MaximumRetryAttempts` (0–2) and `MaximumEventAgeInSeconds` (60–21600) apply per
function, version or alias. A function with no configuration gets AWS's defaults:
two retries, waiting one minute then two. On-success and on-failure
**destinations** receive AWS's invocation record — the envelope naming the
request, the condition, the attempt count and the function's response — and a
`DeadLetterConfig` receives the event itself, so a function configured with both
gets both.

This holds however the event was raised: an S3 notification, an EventBridge or
Scheduler target and an SNS `lambda` subscription all enter the same async path.

| Case | Overcast | On AWS |
| --- | --- | --- |
| `MaximumEventAgeInSeconds` | Measured from acceptance, covering the waits between attempts and handler time; an expired event is discarded **before** the next attempt | Checked after the attempt |
| An aged-out record's `condition` | `RetriesExhausted` | Undocumented — the console describes the destination as firing on all attempts failing *or* the age being exceeded |
| Throttled by the function's own reserved concurrency | Dead-lettered without retries, once the invocation has waited out its concurrency back-off | Dead-lettered without retries |
| Throttled by *exhausted* concurrency | Dead-lettered | Returned to the queue for up to six hours |
| Retry budget for a throttled async invocation | Much shorter than AWS's | Six hours |
| S3 as an on-failure destination | `501` rather than accepted and never written | Supported |
| S3 as an on-success destination | `InvalidParameterValueException` | `InvalidParameterValueException` |

At AWS's retry waits, the sixty-second minimum event age is reachable in the
ordinary case: an event whose first attempt fails has already expired when the
one-minute wait ends, so it never runs again.

## Partial batch responses

An event source mapping created with
`FunctionResponseTypes: ["ReportBatchItemFailures"]` is honoured, not just stored.
The poller reads the response and acts on the records it names:

```json
{ "batchItemFailures": [{ "itemIdentifier": "<message id or sequence number>" }] }
```

- **SQS.** Only the messages the function did *not* report are deleted. A reported
  message stays in flight and becomes visible again when its visibility timeout
  expires, which is how AWS redelivers it. The queue's own `RedrivePolicy` then
  counts the receive and dead-letters on schedule.
- **DynamoDB Streams.** The batch is retried from the earliest record the function
  named, and everything before it is treated as done. Records *after* it are
  redelivered even if the function said they succeeded, because a stream is
  ordered and AWS checkpoints at the lowest reported sequence number.

AWS's edge cases are reproduced:

| Response | Treated as |
| --- | --- |
| Empty or absent `batchItemFailures`, or no response at all | The whole batch succeeded — turning the flag on cannot change a handler that does not use it |
| Invalid JSON | Complete batch failure; nothing is acknowledged |
| An entry that is not an object | Complete batch failure |
| A missing, empty or non-string `itemIdentifier` | Complete batch failure |
| An identifier naming a record that was not in the batch | Complete batch failure |

Each failure is logged with its reason. Member names are matched
case-insensitively, so a handler written against a strongly typed SDK that
serialises `BatchItemFailures` is honoured rather than read as malformed.
`FunctionResponseTypes` itself is validated against AWS's one-member enum.

## Related

- [Lambda](../lambda.md) — quick start and what works
- [Lambda limitations](./limitations.md) — the divergence table
- [Lambda concurrency](./concurrency.md) — what throttles an invocation in the first place
- [Lambda troubleshooting](./troubleshooting.md) — throttles, layer errors, extension endpoints
- [SQS](../sqs.md) — visibility timeouts and redrive policies

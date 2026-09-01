---
title: "CloudWatch Logs"
description: "CloudWatch Logs accepts the AWS JSON 1.1 API over the shared root endpoint with X-Amz-Target: Logs_20140328.\u003cOperationName\u003e. It also accepts Smithy RPC v2 CBOR at..."
section: "Service Reference"
tags:
  - cloudwatch
  - docs
  - logs
  - services
---

# CloudWatch Logs

CloudWatch Logs accepts the AWS JSON 1.1 API over the shared root endpoint
with `X-Amz-Target: Logs_20140328.<OperationName>`. It also accepts Smithy
RPC v2 CBOR at `/service/Logs_20140328/operation/<OperationName>` with
`Smithy-Protocol: rpc-v2-cbor` and `Content-Type: application/cbor`.

Log group names are typically in the form `/aws/lambda/<function-name>` or
`/custom/<app-name>`. Log stream names can be any valid string.

Tagging behavior:

- `CreateLogGroup` accepts `tags` and applies them as part of creating the group, so a
  rejected request creates nothing. `TagLogGroup` merges into the existing set.
- Tag maps are validated against AWS's documented constraints before anything is written:
  at most 50 tags per log group, keys 1–128 characters, values 0–256 characters (an empty
  value is legal, an empty key is not), and no key may begin with the reserved `aws:`
  prefix. Violations return `InvalidParameterException` and leave the log group's existing
  tags untouched. `AWS::Logs::LogGroup` passes its `Tags` through to the service and does
  not re-validate them.

Storage and retention behavior:

- In the SQLite-backed storage modes, log events live in a dedicated indexed table
  (`logs_events`), so appends and time-range reads stay fast regardless of stream size;
  pre-existing blob-format events are converted automatically by a one-time migration on
  first startup after upgrade.
- `RetentionInDays` (set via `PutRetentionPolicy`) is **enforced**: a periodic background
  sweep deletes events older than the group's retention window in every storage mode. Groups
  with no retention policy keep events indefinitely.
- `retentionInDays` must be one of the values AWS documents — 1, 3, 5, 7, 14, 30, 60, 90, 120,
  150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653. Anything else is
  rejected with `InvalidParameterException` before the log group is touched, over AWS JSON and
  RPC v2 CBOR alike. `AWS::Logs::LogGroup` inherits that check from the service instead of
  duplicating it, so a template carrying an unsupported `RetentionInDays` fails the resource
  and rolls the stack back.
- The same sweep also removes a log stream's metadata (its `DescribeLogStreams` entry) once
  its last event has aged out of the retention window and the stream has no events left
  anywhere — matching real CloudWatch Logs, which eventually deletes empty log streams rather
  than leaving a stale entry behind forever. A stream is only removed once it has no persisted
  events, no buffered (not-yet-flushed) events, and a non-zero last-event timestamp — streams
  that have never received an event are never removed, regardless of age.
- Incoming events are briefly write-buffered per stream (~50 ms debounce, flushed early on
  bursts) to coalesce writes; buffers are flushed synchronously on graceful shutdown.

---

<!-- BEGIN overcast:capabilities -->

## Operations

18 of 22 listed operations are implemented.
Per-operation status, notes and AWS API links: [CloudWatch Logs operations](cloudwatch-logs/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)

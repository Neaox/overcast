---
title: "CloudWatch Logs — Amazon CloudWatch Logs"
description: "Quick start, filter patterns and retention enforcement, the input rules applied before anything is written, and the four operations that return 501."
section: "Service Reference"
tags:
  - cloudwatch
  - docs
  - logs
  - services
---

# CloudWatch Logs — Amazon CloudWatch Logs

Log groups, streams and events are stored and queryable, and retention is
enforced for real. Lambda writes its own output here, so `aws logs tail` works
the way it does on AWS.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
aws logs create-log-group --log-group-name /custom/demo
aws logs create-log-stream --log-group-name /custom/demo --log-stream-name run-1
aws logs put-log-events --log-group-name /custom/demo --log-stream-name run-1 \
  --log-events "timestamp=$(($(date +%s) * 1000)),message=hello"

aws logs filter-log-events --log-group-name /custom/demo
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

A Lambda function gets `/aws/lambda/<function-name>` created for it at
`CreateFunction`, and a stream per invocation. Its stdout and stderr land there.

## What works

| Area | Behaviour |
| --- | --- |
| Groups, streams, events | Full CRUD, plus `GetLogEvents` and `FilterLogEvents` |
| Paging | `DescribeLogGroups` and `DescribeLogStreams` honour `limit` (default and maximum 50) and page with `nextToken` |
| Ingestion window | An out-of-window log event is discarded and reported in `rejectedLogEventsInfo`, and the call still succeeds |
| Filter patterns | Plain text, JSON (`{ $.field = value }`), and space-delimited (`[col, ...]`) patterns |
| Retention | `PutRetentionPolicy` is enforced by a background sweep every 5 minutes, in every storage mode. A group with no policy keeps events indefinitely |
| Stream cleanup | The same sweep removes a stream once its last event has aged out and nothing is left buffered. A stream that never received an event is never removed |
| Tagging | `CreateLogGroup` applies `tags` atomically — a rejected request creates nothing. `TagLogGroup` merges |
| Live tail | `StartLiveTail` over the JSON protocol, which the console's tail view uses |
| Storage | In SQLite-backed modes, events live in a dedicated indexed table, so appends and time-range reads stay fast regardless of stream size |
| Protocols | AWS JSON 1.1 (`X-Amz-Target: Logs_20140328.<Operation>`) and Smithy RPC v2 CBOR |

## Validation

Three input rules are enforced before anything is written, so a bad request
never half-applies.

| Input | Rule |
| --- | --- |
| `retentionInDays` | One of 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653 |
| Tags | At most 50 per group, keys 1–128 characters, values 0–256, no key beginning `aws:` |
| `logEvents` | Ascending by `timestamp`. An unordered batch is refused whole |

All three return `InvalidParameterException` and leave existing state untouched.
`AWS::Logs::LogGroup` inherits them from the service, so a template carrying an
unsupported `RetentionInDays` fails the resource and rolls the stack back.

An event's timestamp is the exception to that rule. One more than two hours
ahead of now, older than 14 days, or older than the log group's retention
period is discarded rather than refused: `PutLogEvents` still answers `200` and
names the discarded range in `rejectedLogEventsInfo`, which is the only signal
a client gets that the data never landed.

## Differences from AWS

| Area                      | On AWS                             | Overcast                                                                                                                                                                           |
| ------------------------- | ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Logs Insights             | `StartQuery` and `GetQueryResults` | Both return `501`, so `GetLogRecord` resolves only an `eventId` handed out by `FilterLogEvents`                                                                                    |
| Subscription filters      | Fan out to Lambda or Kinesis       | `PutSubscriptionFilter` returns `501`; there is no fan-out                                                                                                                         |
| Metric filters            | Log events become metrics          | `PutMetricFilter` returns `501`; they never do                                                                                                                                     |
| `StartLiveTail` over CBOR | Supported                          | Returns `501`; only the JSON protocol serves it                                                                                                                                    |
| Write timing              | No documented write buffer         | Events are buffered per stream for about 50 ms — flushed early on a burst, and synchronously on graceful shutdown — so a read immediately after a write may not see the last event |

## Gotchas

> [!WARNING]
> `StartLiveTail` carries a `streaming-` host prefix. The AWS SDK for Go v2
> prepends it to whatever host you configure — `streaming-localhost:4566`,
> which resolves nowhere — so a `BaseEndpoint` alone is not enough.

Pin the endpoint as immutable, which is the one resolver switch that suppresses
the prefix. Only the legacy resolver has it:

```go
client := cloudwatchlogs.NewFromConfig(cfg, func(o *cloudwatchlogs.Options) {
    o.EndpointResolver = cloudwatchlogs.EndpointResolverFromURL(
        "http://localhost:4566",
        func(e *aws.Endpoint) { e.HostnameImmutable = true },
    )
})
```

Every other operation works from `o.BaseEndpoint` as usual — see
[Using AWS SDKs and CLI](../sdk-cli.md#go-aws-sdk-v2). The JavaScript SDK,
which the console's tail view uses, needs nothing extra.

<!-- BEGIN overcast:capabilities -->

## Operations

19 of 23 listed operations are implemented.
Per-operation status, notes and AWS API links: [CloudWatch Logs operations](cloudwatch-logs/operations.md).

<!-- END overcast:capabilities -->

## Related

- [CloudWatch](./cloudwatch.md) — metrics and alarms
- [Lambda](./lambda.md) — function logging configuration
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/Welcome.html)

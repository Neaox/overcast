---
title: "CloudWatch — Amazon CloudWatch"
description: "Quick start, what the alarm evaluator reads and fires, the one-hour metric window, and the alarm shapes that are created but never evaluated."
section: "Service Reference"
tags:
  - alarms
  - amazon
  - cloudwatch
  - docs
  - metrics
  - services
---

# CloudWatch — Amazon CloudWatch

Alarms are evaluated automatically against published metrics, move through
`OK`/`ALARM`/`INSUFFICIENT_DATA`, and fire their actions. Metric datapoints are
kept for about an hour.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
aws cloudwatch put-metric-data --namespace demo --metric-data 'MetricName=Errors,Value=5'
aws cloudwatch put-metric-alarm --alarm-name errors --namespace demo \
  --metric-name Errors --statistic Sum --period 60 --evaluation-periods 1 \
  --threshold 1 --comparison-operator GreaterThanThreshold

aws cloudwatch describe-alarms --alarm-names errors \
  --query 'MetricAlarms[0].[StateValue,StateReason]'
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

The alarm reacts within about one period: evaluation runs over **closed**
periods, so a datapoint published into the period still accumulating is not yet
a datapoint to evaluate.

## What works

| Area | Behaviour |
| --- | --- |
| Metrics | `PutMetricData`, `ListMetrics`, `GetMetricStatistics` and `GetMetricData` |
| Alarm evaluation | A background loop evaluates each alarm over the last `EvaluationPeriods` closed periods, epoch-aligned the way real CloudWatch aligns them |
| What it reads | Namespace, metric name and dimension set; `Average`, `Sum`, `SampleCount`, `Minimum`, `Maximum`; the four threshold comparisons; `Period`, `EvaluationPeriods` and `DatapointsToAlarm` including the "M out of N" rule; `TreatMissingData`; `Unit` |
| On transition | AWS's `StateReason` sentence and `StateReasonData` document, a `StateUpdate` item in `DescribeAlarmHistory`, a `CloudWatch Alarm State Change` event on the default EventBridge bus, and the state's actions. Actions fire on a transition only, exactly as on AWS |
| Actions | An SNS topic ARN is delivered through the emulator's own `Publish`, carrying real CloudWatch's notification body. An Auto Scaling policy ARN executes the policy |
| Controls | `ActionsEnabled`, `EnableAlarmActions`, `DisableAlarmActions` and `SetAlarmState` |
| Tagging | Alarms only — `PutMetricAlarm` `Tags` at creation, then `TagResource` / `UntagResource` / `ListTagsForResource` |
| Protocols | Query, AWS JSON, and Smithy RPC v2 CBOR, from one implementation per operation |

## Differences from AWS

| Area                                                                   | On AWS                                | Overcast                                                                 |
| ---------------------------------------------------------------------- | ------------------------------------- | ------------------------------------------------------------------------ |
| Metric retention                                                       | 15 months at declining resolution     | About one hour, in every storage backend                                 |
| Alarm history                                                          | 14 days                               | The most recent 100 items per alarm                                      |
| Metric math, anomaly detection, percentiles                            | Evaluated                             | The alarm is **created and says so**, but never evaluated                |
| `PutCompositeAlarm`, `PutAnomalyDetector`, PromQL `EvaluationCriteria` | Full API                              | `501 NotImplemented`                                                     |
| Dashboards, metric streams, Contributor Insights                       | Full API                              | Not emulated; a tagging call naming one gets `ResourceNotFoundException` |
| `SetAlarmState`                                                        | Reverts at the next evaluation        | Held for one full evaluation range                                       |
| Unqualified datapoints                                                 | Ignored by an alarm that names a unit | A datapoint published without a unit feeds an alarm that names one       |

The full list, with the defaults `PutMetricAlarm` applies and the ones it
refuses to invent, is in [CloudWatch limitations](./cloudwatch/limitations.md).

## Gotchas

> [!WARNING]
> An alarm whose `Period × EvaluationPeriods` reaches back further than the
> one-hour metric window sees the missing periods and resolves them through
> `TreatMissingData`. Long-window alarms are not a local test.

An alarm the evaluator cannot decide at all announces itself instead.

> [!TIP]
> An alarm Overcast cannot evaluate never sits silently armed. It stays
> `INSUFFICIENT_DATA` with a `StateReason` saying so, the `PutMetricAlarm`
> response carries an `x-overcast-emulation-limitation` header naming what is
> not emulated, and a CloudFormation-created one says the same in
> `ResourceStatusReason` as the deploy goes past.

<!-- BEGIN overcast:capabilities -->

## Operations

15 of 17 listed operations are implemented.
Per-operation status, notes and AWS API links: [CloudWatch operations](cloudwatch/operations.md).

<!-- END overcast:capabilities -->

## Related

- [CloudWatch limitations](./cloudwatch/limitations.md) — evaluation rules, defaults and tagging
- [CloudWatch Logs](./cloudwatch-logs.md) — log groups, streams and retention
- [Auto Scaling](./autoscaling.md) — alarms that drive scaling policies
- [All service pages](./README.md)
- [AWS API reference](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/)

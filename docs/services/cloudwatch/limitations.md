---
title: "CloudWatch limitations"
description: "Which alarm configurations Overcast evaluates, which it accepts without evaluating, which it refuses, and the defaults PutMetricAlarm applies."
section: "Service Reference"
tags:
  - cloudwatch
  - docs
  - limitations
  - services
---

# CloudWatch limitations

What the alarm evaluator does and does not decide, and the input rules around
it. The working set is on [CloudWatch](../cloudwatch.md).

## Created, but not evaluated

An alarm whose configuration the evaluator cannot decide is created and says
so, rather than being refused.

| Configuration | Why |
| --- | --- |
| `Metrics` — metric math and multi-metric alarms | No expression evaluator |
| `ThresholdMetricId` — anomaly detection | No model to compare against |
| `ExtendedStatistic` — `p99`, `tm99`, … | Percentiles are not computed |
| `LessThanLowerOrGreaterThanUpperThreshold`, `LessThanLowerThreshold`, `GreaterThanUpperThreshold` | Anomaly-band operators, with no band |

Such an alarm is never left silent. It declares itself in three places:

- `StateValue` stays `INSUFFICIENT_DATA`, and `StateReason` says the state is
  not computed.
- The `PutMetricAlarm` response carries `x-overcast-emulation-limitation`,
  naming what is not emulated. Ordinary alarms carry no such header.
- `ResourceStatusReason` on the CloudFormation event, when the alarm came from
  a template, so it appears as the deploy goes past.

## Refused

| Configuration | Response |
| --- | --- |
| `EvaluationCriteria` — PromQL alarms | `501 NotImplemented` from `PutMetricAlarm` |
| Any modelled CloudWatch operation Overcast does not implement — `PutCompositeAlarm`, `PutAnomalyDetector`, `PutDashboard` and the rest of the dashboard, metric-stream and Contributor Insights calls | `501 NotImplemented` |
| An action ARN with no sink — EC2 instance actions, Systems Manager OpsItems | The transition still happens and is still published; the undelivered action is logged and recorded as an `Action` history item saying it was **NOT executed** |

Values AWS itself rejects still get AWS's `400 ValidationError`, not a `501`:
an unknown `Statistic` or `ComparisonOperator`, an invalid `TreatMissingData`,
a `Period` that is not 10, 20, 30 or a multiple of 60, or `DatapointsToAlarm`
greater than `EvaluationPeriods`. A metric-math alarm that *also* names a
top-level `Namespace`/`MetricName` is one AWS rejects, and so does Overcast.

## Defaults

`PutMetricAlarm` marks almost everything `Required: No`, which is not the same
as having a default. Three parameters AWS documents a default for, and Overcast
applies the same one:

| Parameter | Default when omitted |
| --- | --- |
| `ActionsEnabled` | `true` |
| `DatapointsToAlarm` | `EvaluationPeriods` — "N out of N" |
| `TreatMissingData` | `missing` |

Five more are optional only because a PromQL alarm carries them inside
`EvaluationCriteria`. For an alarm on a metric they are required, and omitting
one gets a `400 ValidationError` rather than a substituted value:
`Statistic` (or `ExtendedStatistic`), `ComparisonOperator`, `Period`,
`EvaluationPeriods` and `Threshold`.

`AlarmName` is required by `PutMetricAlarm` and optional on
`AWS::CloudWatch::Alarm`: CloudFormation generates
`{StackName}-{LogicalID}-{RANDOM}` when a template leaves it out, which is what
CDK relies on.

## Deliberate divergences

| Area | On AWS | Overcast |
| --- | --- | --- |
| `SetAlarmState` | Reverts at the next evaluation, which can be almost immediately | Protected for one full evaluation range (`Period × EvaluationPeriods`), so a forced state reaches its actions |
| Look-back | May reach further back to fill a range short of datapoints | Exactly the configured range; gaps resolve through `TreatMissingData` |
| Alarm history | Bounded by age — 14 days | Bounded by count — 100 items per alarm |
| A datapoint published with no unit | Filed under `None`, so an alarm naming a unit never sees it and sits in `INSUFFICIENT_DATA` | Counts towards an alarm that names a unit |
| `EvaluationWindow` | Default sliding window | Accepted and ignored; the period-aligned window is always used |

A datapoint that *does* name a unit is still held to it.

## Tagging

AWS tags four CloudWatch resource types — alarms, dashboards, metric streams and
Contributor Insights rules. Overcast emulates alarms only, so the alarm is the
whole taggable surface.

| Resource | Tag on create | Tag after create |
| --- | --- | --- |
| Alarm (`arn:aws:cloudwatch:<region>:<account>:alarm:<name>`) | `PutMetricAlarm` `Tags` | `TagResource` / `UntagResource` / `ListTagsForResource` |
| Dashboard, metric stream, Contributor Insights rule | Not emulated | Not emulated — `ResourceNotFoundException` |

- **Tags apply at creation only.** `PutMetricAlarm` ignores `Tags` when the
  call updates an existing alarm, as on AWS.
- **Tags are deleted with the alarm.** An alarm recreated under the same name
  starts untagged.
- **An unknown resource is an error, not an empty tag set.** All three
  operations return `404 ResourceNotFoundException` for an ARN whose alarm does
  not exist, and `400 InvalidParameterValue` for a `ResourceARN` that is not a
  CloudWatch ARN, including an empty one.
- **Tag sets are validated on both entry points.** At most 50 tags, keys 1–128
  characters not starting `aws:`, values at most 256 characters. A rejected set
  is not written, and a create carrying an invalid one fails outright rather
  than leaving an untagged alarm behind.

The Query protocol's flattened member list ends at the first missing `Key`, so
an empty tag key can only be expressed — and only be rejected — over the JSON
protocol.

## Related

- [CloudWatch](../cloudwatch.md) — quick start and what works
- [CloudWatch operations](./operations.md) — per-operation status

---
title: "CloudWatch — Amazon CloudWatch"
description: "Amazon CloudWatch (monitoring and alarms) answers both the Query protocol — form-encoded POST requests with Action and Version=2010-08-01 — and the JSON protocol the AWS CLI and SDKs send."
section: "Service Reference"
tags:
  - amazon
  - cloudwatch
  - docs
  - services
---

# CloudWatch — Amazon CloudWatch

> AWS docs: https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/

Amazon CloudWatch (monitoring and alarms) answers both the Query protocol — form-encoded
POST requests with `Action` and `Version=2010-08-01` parameters — and the JSON protocol
the AWS CLI and the AWS SDKs send.

---

## Notes

- Query protocol: `POST / HTTP/1.1` with `Action=<Operation>&Version=2010-08-01` in the form body.
- JSON protocol: `POST / HTTP/1.1` with `Content-Type: application/x-amz-json-1.0` and
  `X-Amz-Target: GraniteServiceVersion20100801.<Operation>`. This is what the AWS CLI and the
  SDKs send, so every supported operation answers on both protocols — with one exception below.
- **`GetMetricData` is Query-only.** It has no JSON handler yet, so an SDK or CLI call to it
  gets `400 UnknownOperationException` rather than datapoints.
- Unrecognized Query operations return an XML `501 Not Implemented` error response;
  unrecognized JSON targets return `400 UnknownOperationException`, as on AWS.
- PutMetricData appears in both Alarms and Metrics categories as it supports both use cases.
- **Metric datapoint retention diverges from real AWS:** datapoints are retained for ~1 hour
  (all storage backends), enforced by read-time filtering plus a periodic background sweep —
  real CloudWatch retains metric data for up to 15 months at declining resolution. Overcast
  only bounds local growth; it is not suitable for historical metric analysis. An alarm whose
  `Period × EvaluationPeriods` reaches back further than that window sees the missing periods
  and resolves them through `TreatMissingData`.

## Alarm evaluation

Alarms are evaluated automatically. A single background loop, driven by the injected clock,
evaluates each alarm over the last `EvaluationPeriods` **closed** periods, aligned to the epoch
the way real CloudWatch aligns them — so a datapoint published into the period still
accumulating is not yet a datapoint to evaluate, and an alarm reacts to a breach within about
one period.

`StateValue` moves between `OK`, `ALARM` and `INSUFFICIENT_DATA` with AWS's `StateReason`
sentence and `StateReasonData` JSON document. Every transition writes a `StateUpdate` item to
`DescribeAlarmHistory`, publishes the `CloudWatch Alarm State Change` event to the default
EventBridge bus (source `aws.cloudwatch`), and fires the actions configured for the state it
moved into. Actions fire on a **transition only** — a re-evaluation landing on the same state
fires nothing, exactly as on AWS.

### What is evaluated

| Configuration | Behaviour |
| --- | --- |
| `Namespace` + `MetricName` + `Dimensions` | Evaluated. An alarm sees only its own dimension set |
| `Statistic` — `Average`, `Sum`, `SampleCount`, `Minimum`, `Maximum` | Evaluated |
| `Threshold` with `GreaterThanThreshold`, `GreaterThanOrEqualToThreshold`, `LessThanThreshold`, `LessThanOrEqualToThreshold` | Evaluated |
| `Period`, `EvaluationPeriods`, `DatapointsToAlarm` | Evaluated, including the "M out of N" rule |
| `TreatMissingData` — `missing`, `ignore`, `breaching`, `notBreaching` | Evaluated |
| `AlarmActions` / `OKActions` / `InsufficientDataActions` naming an SNS topic | Delivered through the emulator's own SNS `Publish`, carrying real CloudWatch's notification body |
| `ActionsEnabled`, `EnableAlarmActions`, `DisableAlarmActions` | Honoured |
| `Unit` | Selects which datapoints the alarm sees — a metric published under several units evaluates separately per unit |
| `Tags` | Applied when the alarm is created. Ignored on a `PutMetricAlarm` that updates an existing alarm, as on AWS — use `TagResource`/`UntagResource` |
| `SetAlarmState` | Forces the state and fires that state's actions |

### Optional parameters and their defaults

`PutMetricAlarm` marks almost everything `Required: No`, but that is not the
same as "has a default". Three parameters AWS documents a default for, and
Overcast applies the same one:

| Parameter | Default when omitted |
| --- | --- |
| `ActionsEnabled` | `true` |
| `DatapointsToAlarm` | `EvaluationPeriods` — "N out of N" |
| `TreatMissingData` | `missing` |

Five more are optional only because a PromQL alarm carries them inside
`EvaluationCriteria` instead. For an alarm on a metric they are required, and a
request that omits one gets a `400 ValidationError` rather than a substituted
value — `Statistic` (or `ExtendedStatistic`), `ComparisonOperator`, `Period`,
`EvaluationPeriods` and `Threshold`. Overcast used to fill these in with
`Average` / `GreaterThanThreshold` / 60s / 1 period / `0.0`, which is not a
default so much as a different alarm from the one the caller half-described.
`Threshold: 0` is a value, not an omission.

`AlarmName` is required by `PutMetricAlarm` and optional on
`AWS::CloudWatch::Alarm` — CloudFormation generates
`{StackName}-{LogicalID}-{RANDOM}` when a template leaves it out, which is what
CDK relies on.

### What is refused

Refused loudly rather than accepted and left un-evaluated — an alarm that looks armed but is
never watched is the failure mode the fidelity-risk veto exists to prevent.

| Configuration | Response |
| --- | --- |
| `Metrics` (metric math / multi-metric alarms) | `501 NotImplemented` from `PutMetricAlarm` |
| `EvaluationCriteria` (PromQL alarms) | `501 NotImplemented` from `PutMetricAlarm` |
| `ThresholdMetricId` (anomaly detection) | `501 NotImplemented` from `PutMetricAlarm` |
| `ExtendedStatistic` (`p99`, `tm99`, …) | `501 NotImplemented` from `PutMetricAlarm` |
| `LessThanLowerOrGreaterThanUpperThreshold`, `LessThanLowerThreshold`, `GreaterThanUpperThreshold` | `501 NotImplemented` from `PutMetricAlarm` — anomaly-band operators |
| `PutCompositeAlarm`, `PutAnomalyDetector` | `501 NotImplemented` |
| An action ARN with no sink — EC2 instance actions, Systems Manager OpsItems | The transition still happens and is still published; the undelivered action is logged and recorded as an `Action` history item saying it was **NOT executed** |

Values AWS itself rejects — an unknown `Statistic`, an unknown `ComparisonOperator`, an invalid
`TreatMissingData`, a `Period` that is not 10, 20, 30 or a multiple of 60, or
`DatapointsToAlarm` greater than `EvaluationPeriods` — get AWS's `400 ValidationError`, not a
`501`. The two claims are different: one says the request is wrong, the other says Overcast is
incomplete.

### Deliberate divergences

- **`SetAlarmState` is held longer than on AWS.** Real CloudWatch reverts a forced state at the
  next evaluation, which can be almost immediately. Overcast protects it for one full evaluation
  range (`Period × EvaluationPeriods`) so a forced state is actually observable in
  `DescribeAlarms` and actually reaches its actions.
- **No look-back beyond the evaluation range.** Real CloudWatch may reach further back in time to
  fill an evaluation range short of real datapoints. Overcast evaluates exactly the configured
  range and resolves the gaps through `TreatMissingData`.
- **Alarm history is bounded by count, not age.** The most recent 100 items per alarm are kept;
  real CloudWatch keeps 14 days.
- **A datapoint published without a unit feeds an alarm that names one.** AWS files an
  unqualified datapoint under `None`, so on AWS an alarm on `Count` never sees it and sits in
  `INSUFFICIENT_DATA` — the trap the `PutMetricAlarm` docs warn about when they recommend
  omitting `Unit`. Locally published metrics routinely omit the unit while the CDK construct
  that created the alarm supplied one, so Overcast lets the unqualified datapoint count. A
  datapoint that *does* name a unit is still held to it.
- **`EvaluationWindow` is accepted and ignored.** Overcast always evaluates the period-aligned
  window described above, rather than AWS's default sliding window.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported | ❌ Unsupported |
| -------- | ------------ | -------------- |
| General  | 15           | 2              |

---

## Endpoints

### General

| Operation                 | Status         | Notes                                                                                                                                                                                                                                                                                                                                             | AWS Docs                                                                                                  |
| ------------------------- | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `DeleteAlarms`            | ✅ Supported   | Deletes one or more alarms by name, along with their history                                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DeleteAlarms.html)            |
| `DescribeAlarmHistory`    | ✅ Supported   | Returns StateUpdate, ConfigurationUpdate and Action items; filters by alarm name, item type, date range, MaxRecords and ScanBy. The most recent 100 items per alarm are retained                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DescribeAlarmHistory.html)    |
| `DescribeAlarms`          | ✅ Supported   | Lists alarms, supports filtering; reports the evaluator's live StateValue, StateReason and StateReasonData                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DescribeAlarms.html)          |
| `DescribeAlarmsForMetric` | ✅ Supported   | Lists alarms for a specific metric                                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DescribeAlarmsForMetric.html) |
| `DisableAlarmActions`     | ✅ Supported   | Clears ActionsEnabled so transitions stop firing actions                                                                                                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DisableAlarmActions.html)     |
| `EnableAlarmActions`      | ✅ Supported   | Sets ActionsEnabled so transitions fire actions again                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_EnableAlarmActions.html)      |
| `GetMetricData`           | ✅ Supported   | Returns query-based metric values over time ranges                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricData.html)           |
| `GetMetricStatistics`     | ✅ Supported   | Returns aggregated datapoints by period                                                                                                                                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricStatistics.html)     |
| `ListMetrics`             | ✅ Supported   | Lists available metrics                                                                                                                                                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_ListMetrics.html)             |
| `ListTagsForResource`     | ✅ Supported   | Lists tags for an alarm                                                                                                                                                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_ListTagsForResource.html)     |
| `PutMetricAlarm`          | ✅ Supported   | Creates or updates a single-metric alarm, which is then evaluated automatically (Threshold, ComparisonOperator, Period, EvaluationPeriods, DatapointsToAlarm, Dimensions, TreatMissingData). Metric-math/multi-metric alarms, anomaly detection (ThresholdMetricId) and extended statistics are refused with 501 rather than created un-evaluated | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_PutMetricAlarm.html)          |
| `PutMetricData`           | ✅ Supported   | Publishes metric data points                                                                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_PutMetricData.html)           |
| `SetAlarmState`           | ✅ Supported   | Forces an alarm's state and fires that state's actions; the forced state is held against the evaluator for one evaluation range                                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_SetAlarmState.html)           |
| `PutAnomalyDetector`      | ❌ Unsupported | stub; returns 501 — there is no anomaly-detection model behind the emulator                                                                                                                                                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_PutAnomalyDetector.html)      |
| `PutCompositeAlarm`       | ❌ Unsupported | stub; returns 501 — composite alarm rules are not evaluated                                                                                                                                                                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_PutCompositeAlarm.html)       |
| `TagResource`             | ✅ Supported   | Adds or updates tags on an alarm                                                                                                                                                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_TagResource.html)             |
| `UntagResource`           | ✅ Supported   | Removes tags from an alarm                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_UntagResource.html)           |

<!-- END overcast:capabilities -->

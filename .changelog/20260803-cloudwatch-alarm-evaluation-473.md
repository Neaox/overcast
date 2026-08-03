~! [cloudwatch] alarms now evaluate their own metrics: epoch-aligned periods, `DatapointsToAlarm` M-of-N, `Dimensions`, and all four `TreatMissingData` modes
  migration: an alarm that previously only moved when you called `SetAlarmState` now changes state on its own and fires its `AlarmActions`/`OKActions`/`InsufficientDataActions`. Call `DisableAlarmActions` on alarms whose actions you do not want fired locally
+ [cloudwatch] alarm state transitions publish the `CloudWatch Alarm State Change` event to EventBridge and notify SNS alarm actions
+ [cloudwatch] `DescribeAlarmHistory`, `EnableAlarmActions` and `DisableAlarmActions`, plus `StateReasonData`, `Dimensions`, `DatapointsToAlarm` and the action lists on `DescribeAlarms`
+! [cloudwatch] `PutMetricAlarm` refuses alarm shapes it cannot evaluate — metric math, anomaly detection, extended statistics — with a `501` instead of creating an alarm that never fires
  migration: replace a metric-math, anomaly-detection or percentile alarm with a single-metric alarm using Average, Sum, SampleCount, Minimum or Maximum
+ [web] a live alarms view on the CloudWatch page showing state, reason, what is being evaluated, and recent transitions
* [cloudwatch] Query-protocol errors use AWS's `ErrorResponse` envelope, so SDKs read the error code instead of a generic failure

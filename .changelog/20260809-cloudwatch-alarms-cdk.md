* [cloudformation/cloudwatch] an `AWS::CloudWatch::Alarm` the template does not name deploys
  CloudFormation mints `{Stack}-{Logical}-{RANDOM}` for an omitted `AlarmName`, which is what
  CDK's `Alarm` construct and every `metric.createAlarm()` helper rely on. The empty name
  reached `PutMetricAlarm` and came back as AWS's own `Value null at 'alarmName'`.
* [cloudformation] `AWS::Events::Rule`, `AWS::StepFunctions::StateMachine`, `AWS::ApiGateway::RestApi`, `AWS::Scheduler::ScheduleGroup`, `AWS::ECR::Repository` and `AWS::IAM::User` are named by CloudFormation when the template omits their name
  Each carries the logical ID, so two unnamed resources of one type in a stack are two
  resources. The rule and the schedule group used to share one empty name and quietly
  become a single resource; the rest failed the stack.
* [cloudformation/cloudwatch] every `AWS::CloudWatch::Alarm` property reaches the alarm
  `DatapointsToAlarm` and `TreatMissingData` were dropped, so an "M out of N" alarm was
  evaluated as "N out of N" and `notBreaching` reverted to `missing`. A property the
  template omits is now left out of the request rather than sent empty.
+ [cloudwatch] `Tags` on `PutMetricAlarm`, applied at creation as on AWS
+ [cloudwatch] `Unit` selects which datapoints an alarm evaluates, so a metric published under
  several units evaluates separately per unit
+ [cloudwatch] PromQL alarms (`EvaluationCriteria`) join metric math and anomaly detection in
  returning 501 rather than being created un-evaluated
~! [cloudwatch] `PutMetricAlarm` requires `Statistic`, `ComparisonOperator`, `Period`, `EvaluationPeriods` and `Threshold` for an alarm on a metric
  migration: pass the parameter explicitly. These are `Required: No` only because a PromQL
  alarm supplies them inside `EvaluationCriteria`; an alarm on a metric that omitted one used
  to be created with `Average` / `GreaterThanThreshold` / 60s / 1 period / `0.0` substituted,
  which arms an alarm nobody configured.

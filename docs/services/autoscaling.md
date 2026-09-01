---
title: "Auto Scaling — AWS Auto Scaling"
description: "Auto Scaling groups really converge: a reconciler launches and terminates EC2 instances to match desired capacity, runs the lifecycle state machine, and executes scaling policies driven by CloudWatch alarms."
section: "Service Reference"
tags:
  - auto
  - autoscaling
  - aws
  - docs
  - scaling
  - services
---

# Auto Scaling — AWS Auto Scaling

Auto Scaling groups really converge. A single background reconciler launches and
terminates EC2 instances through the emulator's own EC2 service until each
group's owned instance set matches its `DesiredCapacity`, advances the lifecycle
state machine, honours lifecycle hooks, replaces unhealthy instances, and
records a scaling activity for everything it does.

## What works
Group, launch-configuration, scaling-policy, lifecycle-hook and tag CRUD, plus
`SetDesiredCapacity`, `ExecutePolicy`, `TerminateInstanceInAutoScalingGroup`,
`CompleteLifecycleAction`, `RecordLifecycleActionHeartbeat`,
`SetInstanceHealth`, `SetInstanceProtection`, `DescribeAutoScalingInstances` and
`DescribeScalingActivities`.

## Reconciliation

A group backed by a launch configuration converges within about a second of any
change:

- `CreateAutoScalingGroup`, `UpdateAutoScalingGroup`, `SetDesiredCapacity`,
  `ExecutePolicy` and an alarm-driven policy all move `DesiredCapacity`; the
  reconciler then calls EC2 `RunInstances` / `TerminateInstances` to close the
  gap, clamped to the group's `MinSize` and `MaxSize`.
- Instances move `Pending` → `InService` and `Terminating` → gone. With a
  matching lifecycle hook they park in `Pending:Wait` / `Terminating:Wait`
  first.
- `DescribeAutoScalingGroups` and `DescribeAutoScalingInstances` report the real
  instance set, with `LifecycleState`, `HealthStatus`, `AvailabilityZone`,
  `InstanceType` and `ProtectedFromScaleIn`.
- Instances are placed round-robin across `AvailabilityZones` and, when set,
  across `VPCZoneIdentifier`'s subnets. Tags marked `PropagateAtLaunch` are
  applied to each launched instance, alongside `aws:autoscaling:groupName`.
- Every launch and termination writes a `DescribeScalingActivities` entry with
  AWS's `StatusCode`, `Progress`, `Description` and `Cause` wording. Activities
  are capped at the 200 most recent per group.
- A launch EC2 refuses (a missing AMI, for instance) is recorded as a `Failed`
  activity carrying EC2's own error, never silently dropped.

## Scaling policies

`SimpleScaling` and `StepScaling` execute for real. `PutScalingPolicy` accepts
`ChangeInCapacity`, `ExactCapacity` and `PercentChangeInCapacity`, and honours
`MinAdjustmentMagnitude` and the group's cooldown.

Attaching a policy ARN to a CloudWatch alarm's `AlarmActions` works end to end:
when the alarm transitions, Auto Scaling executes the policy. For a step-scaling
policy, the breach is measured as the alarm's most recent datapoint minus its
threshold, which is the number AWS compares against `MetricIntervalLowerBound` /
`MetricIntervalUpperBound`.

## Lifecycle hooks

`PutLifecycleHook` really pauses the transition it watches. The instance parks
in `Pending:Wait` or `Terminating:Wait`, an `EC2 Instance-launch Lifecycle
Action` (or `…-terminate…`) event is published to the default EventBridge bus
with the `LifecycleActionToken`, and the instance stays there until
`CompleteLifecycleAction` is called or `HeartbeatTimeout` elapses and the hook's
`DefaultResult` is applied. `RecordLifecycleActionHeartbeat` extends the window.

## Not implemented — refused, not ignored

A configuration the reconciler cannot converge is refused at the configuring
operation rather than stored and quietly ignored:

| Configuration | Operation | Result |
| --- | --- | --- |
| `LaunchTemplate` | `CreateAutoScalingGroup` / `UpdateAutoScalingGroup` | `501` — Overcast's EC2 has no `CreateLaunchTemplate` to resolve `ImageId`/`InstanceType` from. Use a launch configuration. Tracked in [#518](https://github.com/overcast-sh/overcast/issues/518). |
| `MixedInstancesPolicy` | `CreateAutoScalingGroup` / `UpdateAutoScalingGroup` | `501` — there is no instance-type fleet or spot allocation to distribute over. |
| `InstanceId` (create from a running instance) | `CreateAutoScalingGroup` | `501` — launch parameters cannot be derived from a running instance. |
| No launch source at all | `CreateAutoScalingGroup` | `400 ValidationError`, as on AWS. |
| `PolicyType=TargetTrackingScaling` | `PutScalingPolicy` | `501` — there is no controller tracking a target metric value. |
| `PolicyType=PredictiveScaling` | `PutScalingPolicy` | `501` — there is no forecasting model over historical metrics. |
| Warm pools, instance refresh, scheduled actions, notification configurations | — | Not registered; a protocol-correct `501`. |

## Differences from AWS
- **Termination policy.** Real Auto Scaling's `Default` policy balances across
  Availability Zones and then prefers the oldest launch configuration; Overcast
  implements `OldestInstance` (skipping instances protected from scale-in),
  which gives the same answer for a single-AZ or uniform group. A
  `TerminationPolicies` value is stored and echoed but does not change the
  choice.
- **`HealthCheckType=ELB`** is accepted and echoed, but load-balancer target
  health is not a health source — only `SetInstanceHealth` marks an instance
  unhealthy. `HealthCheckGracePeriod` is stored and echoed.
- **One hook per transition.** AWS runs every hook watching a transition;
  Overcast parks the instance on the first matching hook by name.
- **State is not partitioned by region.** Auto Scaling groups are global to the
  emulator, and the reconciler launches into the configured default region.
- Uses the Query protocol (form-encoded POST) with API version `2011-01-01`.

<!-- BEGIN overcast:capabilities -->

## Operations

All 25 listed operations are implemented.
Per-operation status, notes and AWS API links: [Auto Scaling operations](autoscaling/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/autoscaling/ec2/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)

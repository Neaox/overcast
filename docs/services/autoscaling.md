---
title: "Auto Scaling — AWS Auto Scaling"
description: "Auto Scaling groups really converge: a reconciler launches and terminates EC2 instances until the group matches its desired capacity, runs the lifecycle state machine, honours lifecycle hooks, and executes simple and step scaling policies driven by CloudWatch alarms."
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

> AWS docs: https://docs.aws.amazon.com/autoscaling/ec2/APIReference/Welcome.html

Auto Scaling groups really converge. A single background reconciler launches and
terminates EC2 instances through the emulator's own EC2 service until each
group's owned instance set matches its `DesiredCapacity`, advances the lifecycle
state machine, honours lifecycle hooks, replaces unhealthy instances, and
records a scaling activity for everything it does.

## Summary

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

Per [the fidelity-risk rule](../plans/full-emulation-priority.md), a
configuration the reconciler cannot converge is refused at the configuring
operation rather than stored and quietly ignored:

| Configuration | Operation | Result |
| --- | --- | --- |
| `LaunchTemplate` | `CreateAutoScalingGroup` / `UpdateAutoScalingGroup` | `501` — Overcast's EC2 has no `CreateLaunchTemplate` to resolve `ImageId`/`InstanceType` from. Use a launch configuration. Tracked in [#518](https://github.com/Neaox/overcast/issues/518). |
| `MixedInstancesPolicy` | `CreateAutoScalingGroup` / `UpdateAutoScalingGroup` | `501` — there is no instance-type fleet or spot allocation to distribute over. |
| `InstanceId` (create from a running instance) | `CreateAutoScalingGroup` | `501` — launch parameters cannot be derived from a running instance. |
| No launch source at all | `CreateAutoScalingGroup` | `400 ValidationError`, as on AWS. |
| `PolicyType=TargetTrackingScaling` | `PutScalingPolicy` | `501` — there is no controller tracking a target metric value. |
| `PolicyType=PredictiveScaling` | `PutScalingPolicy` | `501` — there is no forecasting model over historical metrics. |
| Warm pools, instance refresh, scheduled actions, notification configurations | — | Not registered; a protocol-correct `501`. |

## Known divergences

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

## Summary

| Category   | ✅ Supported |
| ---------- | ------------ |
| Operations | 25           |

---

## Endpoints

### Operations

| Operation                             | Status       | Notes                                                                                                                                                                    | AWS Docs |
| ------------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------- |
| `CreateAutoScalingGroup`              | ✅ Supported | Reconciles: launches EC2 instances to DesiredCapacity. LaunchTemplate, MixedInstancesPolicy and InstanceId are refused with 501 — Overcast's EC2 has no launch templates |          |
| `UpdateAutoScalingGroup`              | ✅ Supported | Min/max/desired changes re-converge the group; desired outside min/max is AWS's ValidationError                                                                          |          |
| `DescribeAutoScalingGroups`           | ✅ Supported | Reports the real converged instance set with LifecycleState, HealthStatus, AvailabilityZone and ProtectedFromScaleIn                                                     |          |
| `DeleteAutoScalingGroup`              | ✅ Supported | ResourceInUse while instances remain unless ForceDelete; ForceDelete terminates them                                                                                     |          |
| `SetDesiredCapacity`                  | ✅ Supported | Launches or terminates instances to converge; out-of-range values return AWS's ValidationError                                                                           |          |
| `TerminateInstanceInAutoScalingGroup` | ✅ Supported | Terminates the EC2 instance and records the scaling activity; honours ShouldDecrementDesiredCapacity                                                                     |          |
| `CreateLaunchConfiguration`           | ✅ Supported | ImageId/InstanceType/SecurityGroups drive RunInstances at launch time                                                                                                    |          |
| `DescribeLaunchConfigurations`        | ✅ Supported |                                                                                                                                                                          |          |
| `DeleteLaunchConfiguration`           | ✅ Supported | ResourceInUse while a group still references it                                                                                                                          |          |
| `PutScalingPolicy`                    | ✅ Supported | SimpleScaling and StepScaling execute for real; TargetTrackingScaling and PredictiveScaling are refused with 501                                                         |          |
| `DescribePolicies`                    | ✅ Supported |                                                                                                                                                                          |          |
| `DeletePolicy`                        | ✅ Supported |                                                                                                                                                                          |          |
| `ExecutePolicy`                       | ✅ Supported | Applies the policy's adjustment to DesiredCapacity, honouring cooldown and MinAdjustmentMagnitude                                                                        |          |
| `PutLifecycleHook`                    | ✅ Supported | Really pauses launch/terminate in Pending:Wait / Terminating:Wait and emits the EventBridge lifecycle-action event                                                       |          |
| `DescribeLifecycleHooks`              | ✅ Supported |                                                                                                                                                                          |          |
| `DeleteLifecycleHook`                 | ✅ Supported | Releases any instance parked on the hook                                                                                                                                 |          |
| `CompleteLifecycleAction`             | ✅ Supported | CONTINUE moves the instance on; ABANDON terminates it                                                                                                                    |          |
| `RecordLifecycleActionHeartbeat`      | ✅ Supported | Extends the hook's heartbeat window                                                                                                                                      |          |
| `CreateOrUpdateTags`                  | ✅ Supported | PropagateAtLaunch tags are applied to launched EC2 instances                                                                                                             |          |
| `DeleteTags`                          | ✅ Supported |                                                                                                                                                                          |          |
| `DescribeTags`                        | ✅ Supported | Filters: auto-scaling-group, key, propagate-at-launch, value; an unimplemented filter name is refused, not ignored                                                       |          |
| `DescribeAutoScalingInstances`        | ✅ Supported | Reports the real instance set the reconciler owns                                                                                                                        |          |
| `DescribeScalingActivities`           | ✅ Supported | One activity per launch and termination, with AWS's StatusCode and Cause wording                                                                                         |          |
| `SetInstanceHealth`                   | ✅ Supported | Unhealthy instances are terminated and replaced by the reconciler                                                                                                        |          |
| `SetInstanceProtection`               | ✅ Supported | Protected instances are excluded from scale-in                                                                                                                           |          |

<!-- END overcast:capabilities -->

---
title: "Auto Scaling — AWS Auto Scaling"
description: "Quick start, how a group converges on desired capacity, which scaling policies and lifecycle hooks run for real, and the configurations refused with 501."
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

Auto Scaling groups converge: a reconciler launches and terminates EC2
instances until each group matches its `DesiredCapacity`, running the lifecycle
state machine and recording an activity for everything it does.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
aws autoscaling create-launch-configuration --launch-configuration-name web \
  --image-id ami-12345678 --instance-type t3.micro
aws autoscaling create-auto-scaling-group --auto-scaling-group-name web \
  --launch-configuration-name web --availability-zones us-east-1a \
  --min-size 1 --max-size 3 --desired-capacity 2

aws autoscaling describe-auto-scaling-groups --auto-scaling-group-name web \
  --query 'AutoScalingGroups[0].Instances[].[InstanceId,LifecycleState]'
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

The reconciler is woken by every change, so the instances are usually there by
the time the next call lands. A one-second tick bounds the transitions nothing
pokes — a lifecycle heartbeat expiring, a cooldown ending.

## What works

| Area | Behaviour |
| --- | --- |
| Convergence | `CreateAutoScalingGroup`, `UpdateAutoScalingGroup`, `SetDesiredCapacity`, `ExecutePolicy` and alarm-driven policies all move `DesiredCapacity`; the reconciler then calls EC2 `RunInstances` / `TerminateInstances` to close the gap, clamped to `MinSize` and `MaxSize` |
| Launch source | Either a launch configuration or an EC2 `LaunchTemplate`, by ID or name and at a version including `$Latest` and `$Default`. The template is resolved through EC2 when the group is configured, so one that does not exist is a `ValidationError` rather than a group that never converges. `DescribeAutoScalingGroups` reports it, as does each instance launched from it |
| Instance state | `Pending` → `InService` and `Terminating` → gone, reported with `LifecycleState`, `HealthStatus`, `AvailabilityZone`, `InstanceType` and `ProtectedFromScaleIn` |
| Placement | Round-robin across `VPCZoneIdentifier`'s subnets, each instance landing in its own subnet's zone, or across `AvailabilityZones` for a group with no subnets. A group given only subnets takes its zones from them |
| Instance tags | Tags marked `PropagateAtLaunch` are applied to each instance, alongside `aws:autoscaling:groupName` |
| Scaling policies | `SimpleScaling` and `StepScaling` execute, honouring `ChangeInCapacity`, `ExactCapacity`, `PercentChangeInCapacity`, `MinAdjustmentMagnitude` and the group's cooldown |
| CloudWatch alarms | A policy ARN in an alarm's `AlarmActions` works end to end. For a step policy, the breach is the alarm's most recent datapoint minus its threshold — the number AWS compares against the metric interval bounds |
| Lifecycle hooks | The instance really parks in `Pending:Wait` or `Terminating:Wait`, an `EC2 Instance-launch Lifecycle Action` event carrying the `LifecycleActionToken` is published to the default EventBridge bus, and it stays there until `CompleteLifecycleAction` or `HeartbeatTimeout` applies the hook's `DefaultResult`. `RecordLifecycleActionHeartbeat` extends the window |
| Activities | Every launch and termination writes a `DescribeScalingActivities` entry with AWS's `StatusCode`, `Progress`, `Description` and `Cause` wording, capped at the 200 most recent per group. A launch EC2 refuses is recorded as a `Failed` activity carrying EC2's own error, never silently dropped |

## Refused, not ignored

A configuration the reconciler cannot converge is refused at the configuring
operation rather than stored and quietly disregarded.

| Configuration | Operation | Result |
| --- | --- | --- |
| `MixedInstancesPolicy` | `CreateAutoScalingGroup` / `UpdateAutoScalingGroup` | `501` — there is no instance-type fleet or spot allocation to distribute over |
| `InstanceId` | `CreateAutoScalingGroup` | `501` — launch parameters cannot be derived from a running instance |
| No launch source at all | `CreateAutoScalingGroup` | `400 ValidationError`, as on AWS |
| `VPCZoneIdentifier` subnets outside `AvailabilityZones` | `CreateAutoScalingGroup` / `UpdateAutoScalingGroup` | `400 ValidationError`, as on AWS — the two must name the same zones |
| `PolicyType=TargetTrackingScaling` | `PutScalingPolicy` | `501` — nothing tracks a target metric value |
| `PolicyType=PredictiveScaling` | `PutScalingPolicy` | `501` — there is no forecasting model over historical metrics |
| Warm pools, instance refresh, scheduled actions, notification configurations | — | Not registered; a protocol-correct `501` |

## Differences from AWS

| Area                  | On AWS                                                         | Overcast                                                                                                                        |
| --------------------- | -------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| Termination policy    | `Default` — balance across AZs, then the documented tie-breaks | `OldestInstance`, skipping instances protected from scale-in — the same answer `Default` gives for a single-AZ or uniform group |
| `TerminationPolicies` | Chooses which instance goes                                    | Stored and echoed; it does not change the choice                                                                                |
| `HealthCheckType=ELB` | Load-balancer target health marks an instance unhealthy        | Accepted and echoed, as is `HealthCheckGracePeriod`; only `SetInstanceHealth` marks an instance unhealthy                       |
| Lifecycle hooks       | Every hook watching a transition runs                          | The instance parks on the first matching hook by name                                                                           |
| Region                | Groups are regional                                            | Groups are global to the emulator; the reconciler launches into the configured default region                                   |
| Instances             | Real compute                                                   | Metadata — the group converges on records, not on running compute. See [EC2](./ec2.md)                                          |

<!-- BEGIN overcast:capabilities -->

## Operations

All 25 listed operations are implemented.
Per-operation status, notes and AWS API links: [Auto Scaling operations](autoscaling/operations.md).

<!-- END overcast:capabilities -->

## Related

- [EC2 / VPC](./ec2.md) — what an instance record does and does not do
- [CloudWatch](./cloudwatch.md) — the alarms that drive scaling policies
- [All service pages](./README.md)
- [AWS API reference](https://docs.aws.amazon.com/autoscaling/ec2/APIReference/Welcome.html)

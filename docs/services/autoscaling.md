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
| Instance state | `Pending` → `InService` and `Terminating` → gone, reported with `LifecycleState`, `HealthStatus`, `AvailabilityZone`, `InstanceType` and `ProtectedFromScaleIn` |
| Placement | Round-robin across `AvailabilityZones` and, when set, across `VPCZoneIdentifier`'s subnets. Tags marked `PropagateAtLaunch` are applied to each instance, alongside `aws:autoscaling:groupName` |
| Scaling policies | `SimpleScaling` and `StepScaling` execute, honouring `ChangeInCapacity`, `ExactCapacity`, `PercentChangeInCapacity`, `MinAdjustmentMagnitude` and the group's cooldown |
| CloudWatch alarms | A policy ARN in an alarm's `AlarmActions` works end to end. For a step policy, the breach is the alarm's most recent datapoint minus its threshold — the number AWS compares against the metric interval bounds |
| Lifecycle hooks | The instance really parks in `Pending:Wait` or `Terminating:Wait`, an `EC2 Instance-launch Lifecycle Action` event carrying the `LifecycleActionToken` is published to the default EventBridge bus, and it stays there until `CompleteLifecycleAction` or `HeartbeatTimeout` applies the hook's `DefaultResult`. `RecordLifecycleActionHeartbeat` extends the window |
| Activities | Every launch and termination writes a `DescribeScalingActivities` entry with AWS's `StatusCode`, `Progress`, `Description` and `Cause` wording, capped at the 200 most recent per group. A launch EC2 refuses is recorded as a `Failed` activity carrying EC2's own error, never silently dropped |

## Refused, not ignored

A configuration the reconciler cannot converge is refused at the configuring
operation rather than stored and quietly disregarded.

| Configuration | Operation | Result |
| --- | --- | --- |
| `LaunchTemplate` | `CreateAutoScalingGroup` / `UpdateAutoScalingGroup` | `501` — EC2 has no `CreateLaunchTemplate` to resolve `ImageId`/`InstanceType` from. Use a launch configuration |
| `MixedInstancesPolicy` | `CreateAutoScalingGroup` / `UpdateAutoScalingGroup` | `501` — there is no instance-type fleet or spot allocation to distribute over |
| `InstanceId` | `CreateAutoScalingGroup` | `501` — launch parameters cannot be derived from a running instance |
| No launch source at all | `CreateAutoScalingGroup` | `400 ValidationError`, as on AWS |
| `PolicyType=TargetTrackingScaling` | `PutScalingPolicy` | `501` — nothing tracks a target metric value |
| `PolicyType=PredictiveScaling` | `PutScalingPolicy` | `501` — there is no forecasting model over historical metrics |
| Warm pools, instance refresh, scheduled actions, notification configurations | — | Not registered; a protocol-correct `501` |

## Differences from AWS

| Area | Overcast |
| --- | --- |
| Termination policy | `OldestInstance`, skipping instances protected from scale-in — the same answer AWS's `Default` gives for a single-AZ or uniform group. A `TerminationPolicies` value is stored and echoed but does not change the choice |
| `HealthCheckType=ELB` | Accepted and echoed, but load-balancer target health is not a health source. Only `SetInstanceHealth` marks an instance unhealthy. `HealthCheckGracePeriod` is stored and echoed |
| Lifecycle hooks | AWS runs every hook watching a transition; Overcast parks the instance on the first matching hook by name |
| Region | Groups are global to the emulator, and the reconciler launches into the configured default region |
| Instances | EC2 instances are metadata — the group converges on records, not on running compute. See [EC2](./ec2.md) |

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

---
title: "Auto Scaling operations"
description: "Every Auto Scaling operation Overcast declares — 25 of 25 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - autoscaling
  - docs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# Auto Scaling operations

All 25 listed operations are implemented. Back to [Auto Scaling](../autoscaling.md).

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

## Related

- [Auto Scaling](../autoscaling.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->

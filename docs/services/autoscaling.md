# Auto Scaling — AWS Auto Scaling

> AWS docs: https://docs.aws.amazon.com/autoscaling/ec2/APIReference/Welcome.html

Metadata-only implementation of Auto Scaling. All state is stored in-memory and responses use realistic field values, but no actual instance management or scaling is performed.

## Summary

Supports create/describe/delete for Auto Scaling Groups, Launch Configurations, Scaling Policies, Lifecycle Hooks, and Tags, as well as `SetDesiredCapacity`, `TerminateInstanceInAutoScalingGroup`, and `DescribeAutoScalingInstances`.

## Behavior Notes

- No real EC2 instances are launched or managed — all capacity changes are accepted and stored, but no instances are tracked.
- `TerminateInstanceInAutoScalingGroup` is accepted and returns a stub activity record.
- `DescribeAutoScalingInstances` always returns an empty list (no real instances).
- All other Auto Scaling operations are unsupported and return 501 Not Implemented.
- Uses the Query protocol (form-encoded POST) with API version `2011-01-01`.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | 🧊 Inert |
| ---------- | -------- |
| Operations | 19       |

---

## Endpoints

### Operations

| Operation                             | Status   | Notes | AWS Docs |
| ------------------------------------- | -------- | ----- | -------- |
| `CreateAutoScalingGroup`              | 🧊 Inert |       |          |
| `UpdateAutoScalingGroup`              | 🧊 Inert |       |          |
| `DescribeAutoScalingGroups`           | 🧊 Inert |       |          |
| `DeleteAutoScalingGroup`              | 🧊 Inert |       |          |
| `SetDesiredCapacity`                  | 🧊 Inert |       |          |
| `TerminateInstanceInAutoScalingGroup` | 🧊 Inert |       |          |
| `CreateLaunchConfiguration`           | 🧊 Inert |       |          |
| `DescribeLaunchConfigurations`        | 🧊 Inert |       |          |
| `DeleteLaunchConfiguration`           | 🧊 Inert |       |          |
| `PutScalingPolicy`                    | 🧊 Inert |       |          |
| `DescribePolicies`                    | 🧊 Inert |       |          |
| `DeletePolicy`                        | 🧊 Inert |       |          |
| `PutLifecycleHook`                    | 🧊 Inert |       |          |
| `DescribeLifecycleHooks`              | 🧊 Inert |       |          |
| `DeleteLifecycleHook`                 | 🧊 Inert |       |          |
| `CreateOrUpdateTags`                  | 🧊 Inert |       |          |
| `DeleteTags`                          | 🧊 Inert |       |          |
| `DescribeTags`                        | 🧊 Inert |       |          |
| `DescribeAutoScalingInstances`        | 🧊 Inert |       |          |

<!-- END overcast:capabilities -->

//go:build dev

package autoscaling

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateAutoScalingGroup", Status: capabilities.StatusSupported, Notes: "Reconciles: launches EC2 instances to DesiredCapacity. LaunchTemplate, MixedInstancesPolicy and InstanceId are refused with 501 — Overcast's EC2 has no launch templates"},
		capabilities.Capability{Operation: "UpdateAutoScalingGroup", Status: capabilities.StatusSupported, Notes: "Min/max/desired changes re-converge the group; desired outside min/max is AWS's ValidationError"},
		capabilities.Capability{Operation: "DescribeAutoScalingGroups", Status: capabilities.StatusSupported, Notes: "Reports the real converged instance set with LifecycleState, HealthStatus, AvailabilityZone and ProtectedFromScaleIn"},
		capabilities.Capability{Operation: "DeleteAutoScalingGroup", Status: capabilities.StatusSupported, Notes: "ResourceInUse while instances remain unless ForceDelete; ForceDelete terminates them"},
		capabilities.Capability{Operation: "SetDesiredCapacity", Status: capabilities.StatusSupported, Notes: "Launches or terminates instances to converge; out-of-range values return AWS's ValidationError"},
		capabilities.Capability{Operation: "TerminateInstanceInAutoScalingGroup", Status: capabilities.StatusSupported, Notes: "Terminates the EC2 instance and records the scaling activity; honours ShouldDecrementDesiredCapacity"},
		capabilities.Capability{Operation: "CreateLaunchConfiguration", Status: capabilities.StatusSupported, Notes: "ImageId/InstanceType/SecurityGroups drive RunInstances at launch time"},
		capabilities.Capability{Operation: "DescribeLaunchConfigurations", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteLaunchConfiguration", Status: capabilities.StatusSupported, Notes: "ResourceInUse while a group still references it"},
		capabilities.Capability{Operation: "PutScalingPolicy", Status: capabilities.StatusSupported, Notes: "SimpleScaling and StepScaling execute for real; TargetTrackingScaling and PredictiveScaling are refused with 501"},
		capabilities.Capability{Operation: "DescribePolicies", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeletePolicy", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ExecutePolicy", Status: capabilities.StatusSupported, Notes: "Applies the policy's adjustment to DesiredCapacity, honouring cooldown and MinAdjustmentMagnitude"},
		capabilities.Capability{Operation: "PutLifecycleHook", Status: capabilities.StatusSupported, Notes: "Really pauses launch/terminate in Pending:Wait / Terminating:Wait and emits the EventBridge lifecycle-action event"},
		capabilities.Capability{Operation: "DescribeLifecycleHooks", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteLifecycleHook", Status: capabilities.StatusSupported, Notes: "Releases any instance parked on the hook"},
		capabilities.Capability{Operation: "CompleteLifecycleAction", Status: capabilities.StatusSupported, Notes: "CONTINUE moves the instance on; ABANDON terminates it"},
		capabilities.Capability{Operation: "RecordLifecycleActionHeartbeat", Status: capabilities.StatusSupported, Notes: "Extends the hook's heartbeat window"},
		capabilities.Capability{Operation: "CreateOrUpdateTags", Status: capabilities.StatusSupported, Notes: "PropagateAtLaunch tags are applied to launched EC2 instances"},
		capabilities.Capability{Operation: "DeleteTags", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DescribeTags", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DescribeAutoScalingInstances", Status: capabilities.StatusSupported, Notes: "Reports the real instance set the reconciler owns"},
		capabilities.Capability{Operation: "DescribeScalingActivities", Status: capabilities.StatusSupported, Notes: "One activity per launch and termination, with AWS's StatusCode and Cause wording"},
		capabilities.Capability{Operation: "SetInstanceHealth", Status: capabilities.StatusSupported, Notes: "Unhealthy instances are terminated and replaced by the reconciler"},
		capabilities.Capability{Operation: "SetInstanceProtection", Status: capabilities.StatusSupported, Notes: "Protected instances are excluded from scale-in"},
	)
}

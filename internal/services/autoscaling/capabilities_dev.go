//go:build dev

package autoscaling

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateAutoScalingGroup", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "UpdateAutoScalingGroup", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeAutoScalingGroups", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteAutoScalingGroup", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "SetDesiredCapacity", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "TerminateInstanceInAutoScalingGroup", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "CreateLaunchConfiguration", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeLaunchConfigurations", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteLaunchConfiguration", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "PutScalingPolicy", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribePolicies", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeletePolicy", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "PutLifecycleHook", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeLifecycleHooks", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteLifecycleHook", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "CreateOrUpdateTags", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DeleteTags", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeTags", Status: capabilities.StatusInert},
		capabilities.Capability{Operation: "DescribeAutoScalingInstances", Status: capabilities.StatusInert},
	)
}

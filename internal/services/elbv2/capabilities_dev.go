//go:build dev

package elbv2

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateLoadBalancer", Category: "Load Balancers", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DescribeLoadBalancers", Category: "Load Balancers", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteLoadBalancer", Category: "Load Balancers", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "CreateTargetGroup", Category: "Target Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DescribeTargetGroups", Category: "Target Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteTargetGroup", Category: "Target Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "CreateListener", Category: "Listeners", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DescribeListeners", Category: "Listeners", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteListener", Category: "Listeners", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "RegisterTargets", Category: "Targets", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeregisterTargets", Category: "Targets", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DescribeTargetHealth", Category: "Targets", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "CreateRule", Category: "Listener Rules", Status: capabilities.StatusUnsupported},
		capabilities.Capability{Operation: "DescribeRules", Category: "Listener Rules", Status: capabilities.StatusUnsupported},
		capabilities.Capability{Operation: "ModifyLoadBalancerAttributes", Category: "Load Balancers", Status: capabilities.StatusUnsupported},
	)
}

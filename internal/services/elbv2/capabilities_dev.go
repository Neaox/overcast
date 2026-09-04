//go:build dev

package elbv2

import "github.com/overcast-sh/overcast/internal/capabilities"

func init() {
	capabilities.Default.RegisterForService(serviceName,
		capabilities.Capability{Operation: "CreateLoadBalancer", Category: "Load Balancers", Status: capabilities.StatusSupported, Notes: "Threads Type, Scheme, IpAddressType, Subnets, SecurityGroups and Tags"},
		capabilities.Capability{Operation: "DescribeLoadBalancers", Category: "Load Balancers", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteLoadBalancer", Category: "Load Balancers", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "CreateTargetGroup", Category: "Target Groups", Status: capabilities.StatusSupported, Notes: "Threads TargetType, ProtocolVersion, IpAddressType, the HealthCheck* family, Matcher and Tags; health checks are stored and echoed but not evaluated against targets"},
		capabilities.Capability{Operation: "DescribeTargetGroups", Category: "Target Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteTargetGroup", Category: "Target Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "ModifyTargetGroup", Category: "Target Groups", Status: capabilities.StatusSupported, Notes: "Updates the HealthCheck* family and Matcher; TargetType/Protocol/Port/VpcId require replacement"},
		capabilities.Capability{Operation: "ModifyTargetGroupAttributes", Category: "Target Groups", Status: capabilities.StatusSupported, Notes: "Stores and echoes attributes such as deregistration_delay.timeout_seconds; not enforced by the data plane"},
		capabilities.Capability{Operation: "DescribeTargetGroupAttributes", Category: "Target Groups", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "CreateListener", Category: "Listeners", Status: capabilities.StatusSupported, Notes: "Forwards each DefaultActions member's Type, TargetGroupArn, Order, RedirectConfig and FixedResponseConfig; weighted ForwardConfig, Certificates/SslPolicy/AlpnPolicy/MutualAuthentication and the Cognito/OIDC auth actions are not modelled"},
		capabilities.Capability{Operation: "DescribeListeners", Category: "Listeners", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeleteListener", Category: "Listeners", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "RegisterTargets", Category: "Targets", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DeregisterTargets", Category: "Targets", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "DescribeTargetHealth", Category: "Targets", Status: capabilities.StatusSupported, Notes: "Always reports \"healthy\"; the stored HealthCheck* block is not evaluated against targets"},
		capabilities.Capability{Operation: "CreateRule", Category: "Listener Rules", Status: capabilities.StatusUnsupported},
		capabilities.Capability{Operation: "DescribeRules", Category: "Listener Rules", Status: capabilities.StatusUnsupported},
		capabilities.Capability{Operation: "ModifyLoadBalancerAttributes", Category: "Load Balancers", Status: capabilities.StatusSupported, Notes: "Stores and echoes attributes such as idle_timeout.timeout_seconds and deletion_protection.enabled; not enforced by the data plane"},
		capabilities.Capability{Operation: "DescribeLoadBalancerAttributes", Category: "Load Balancers", Status: capabilities.StatusSupported},
		capabilities.Capability{Operation: "AddTags", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Adds tags to load balancers and target groups"},
		capabilities.Capability{Operation: "RemoveTags", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Removes tags from load balancers and target groups"},
		capabilities.Capability{Operation: "DescribeTags", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Describes tags for load balancers and target groups"},
	)
}

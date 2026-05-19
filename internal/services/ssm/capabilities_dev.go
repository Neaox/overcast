//go:build dev

package ssm

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		capabilities.Capability{Service: "ssm", Operation: "AddTagsToResource", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "DeleteParameter", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "DeleteParameters", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "DescribeParameters", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "GetParameter", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "GetParameterHistory", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "GetParameters", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "GetParametersByPath", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "ListTagsForResource", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ssm", Operation: "PutParameter", Category: "General", Status: capabilities.StatusSupported},
	)
}

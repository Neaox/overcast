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

		// Unsupported operations: no dispatch entry, so requests return
		// 501 NotImplemented via the dispatch fallback. DocOnly because they
		// are documented but not dispatched. Only real AWS SSM operations are
		// listed here.
		capabilities.Capability{Service: "ssm", Operation: "LabelParameterVersion", Category: "Parameters", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "ssm", Operation: "UnlabelParameterVersion", Category: "Parameters", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "ssm", Operation: "RemoveTagsFromResource", Category: "Tags", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "ssm", Operation: "GetServiceSetting", Category: "Advanced/misc", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "ssm", Operation: "CreateDocument", Category: "Advanced/misc", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "ssm", Operation: "SendCommand", Category: "Advanced/misc", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "ssm", Operation: "StartAutomationExecution", Category: "Advanced/misc", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
		capabilities.Capability{Service: "ssm", Operation: "RegisterDefaultPatchBaseline", Category: "Advanced/misc", Status: capabilities.StatusUnsupported, Notes: "Returns 501", DocOnly: true},
	)
}

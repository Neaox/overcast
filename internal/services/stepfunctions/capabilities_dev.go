//go:build dev

package stepfunctions

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// State machines
		capabilities.Capability{Service: "stepfunctions", Operation: "CreateStateMachine", Category: "State machines", Status: capabilities.StatusSupported, Notes: "Idempotent — returns existing if name+def match"},
		capabilities.Capability{Service: "stepfunctions", Operation: "DescribeStateMachine", Category: "State machines", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "stepfunctions", Operation: "ListStateMachines", Category: "State machines", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "stepfunctions", Operation: "DeleteStateMachine", Category: "State machines", Status: capabilities.StatusSupported},
		// Executions
		capabilities.Capability{Service: "stepfunctions", Operation: "StartExecution", Category: "Executions", Status: capabilities.StatusSupported, Notes: "Records execution; immediately marks SUCCEEDED"},
	)
}

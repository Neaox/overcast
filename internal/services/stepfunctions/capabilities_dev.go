//go:build dev

package stepfunctions

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// State machines
		capabilities.Capability{Service: "stepfunctions", Operation: "CreateStateMachine", Category: "State machines", Status: capabilities.StatusSupported, Notes: "Validates the ASL; idempotent — returns existing if name+def match"},
		capabilities.Capability{Service: "stepfunctions", Operation: "DescribeStateMachine", Category: "State machines", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "stepfunctions", Operation: "ListStateMachines", Category: "State machines", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "stepfunctions", Operation: "DeleteStateMachine", Category: "State machines", Status: capabilities.StatusSupported},
			capabilities.Capability{Service: "stepfunctions", Operation: "UpdateStateMachine", Category: "State machines", Status: capabilities.StatusSupported, Notes: "Definition/roleArn/loggingConfiguration/tracingConfiguration; no versioning (publish)"},
		capabilities.Capability{Service: "stepfunctions", Operation: "DescribeStateMachineForExecution", Category: "State machines", Status: capabilities.StatusSupported},
		// Executions
		capabilities.Capability{Service: "stepfunctions", Operation: "StartExecution", Category: "Executions", Status: capabilities.StatusSupported, Notes: "Interprets the ASL; returns while the execution is RUNNING, as AWS does"},
		capabilities.Capability{Service: "stepfunctions", Operation: "StartSyncExecution", Category: "Executions", Status: capabilities.StatusSupported, Notes: "EXPRESS only — same interpreter, run to completion before returning"},
		capabilities.Capability{Service: "stepfunctions", Operation: "DescribeExecution", Category: "Executions", Status: capabilities.StatusSupported, Notes: "Real status, output, error and cause"},
		capabilities.Capability{Service: "stepfunctions", Operation: "ListExecutions", Category: "Executions", Status: capabilities.StatusSupported, Notes: "statusFilter and maxResults honoured; no pagination token"},
		capabilities.Capability{Service: "stepfunctions", Operation: "GetExecutionHistory", Category: "Executions", Status: capabilities.StatusSupported, Notes: "Real state-transition events in AWS's vocabulary; readable while RUNNING"},
		capabilities.Capability{Service: "stepfunctions", Operation: "StopExecution", Category: "Executions", Status: capabilities.StatusSupported, Notes: "Interrupts a running execution; it reaches ABORTED asynchronously"},
		// Tags
		capabilities.Capability{Service: "stepfunctions", Operation: "TagResource", Category: "Tags", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "stepfunctions", Operation: "UntagResource", Category: "Tags", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "stepfunctions", Operation: "ListTagsForResource", Category: "Tags", Status: capabilities.StatusSupported},
	)
}

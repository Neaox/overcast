package lambda

// NodeRuntime implements the Runtime interface for Node.js Lambda functions.
// Supported runtime identifiers: nodejs18.x, nodejs20.x, nodejs22.x
//
// Execution strategy:
//  1. Extract the uploaded zip to a temp directory.
//  2. Write a thin bootstrap script that loads the handler module,
//     calls handler(event, context), and writes the result to stdout as JSON.
//  3. Exec `node bootstrap.js` with a timeout equal to fn.Timeout.
//  4. Capture stdout as the response payload and stderr as log output.
//
// This approach avoids any Lambda-specific SDK dependency and works with
// any standard Node.js package.

import (
	"github.com/your-org/overcast/internal/config"
)

// NodeRuntime executes Node.js Lambda functions.
type NodeRuntime struct {
	cfg *config.Config
}

func newNodeRuntime(cfg *config.Config) *NodeRuntime {
	return &NodeRuntime{cfg: cfg}
}

// CanHandle returns true for all supported Node.js runtime identifiers.
func (rt *NodeRuntime) CanHandle(runtimeID string) bool {
	switch runtimeID {
	case "nodejs18.x", "nodejs20.x", "nodejs22.x":
		return true
	}
	return false
}

// Invoke executes a Node.js Lambda function.
// TODO: implement in the TDD cycle — write the test first in runtime_test.go,
// then implement this method.
//
// Planned implementation steps:
//  1. base64-decode fn.CodeZip into a temp directory
//  2. write internal/services/lambda/runtime/bootstrap.js wrapper
//  3. exec: cfg.LambdaNodeBin bootstrap.js with event piped to stdin
//  4. collect stdout (response) and stderr (logs) with fn.Timeout deadline
//  5. parse stdout as JSON InvokeResult
func (rt *NodeRuntime) Invoke(fn *Function, event []byte) (*InvokeResult, error) {
	// TODO: implement
	return &InvokeResult{
		StatusCode: 200,
		Payload:    []byte(`{"statusCode":200,"body":"Lambda execution not yet implemented"}`),
	}, nil
}

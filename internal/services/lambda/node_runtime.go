package lambda

// NodeRuntime implements the Runtime interface for Node.js Lambda functions.
// Supported runtime identifiers: nodejs20.x, nodejs22.x
//
// This is a stub implementation that satisfies the two-level Runtime/RuntimeInstance
// interface. The real container-based implementation will replace this when
// ContainerRuntime is built in Phase 2.
//
// TODO(priority:P2): replace NodeRuntime with ContainerRuntime that pulls
// official public.ecr.aws/lambda/nodejs images and runs them via Docker.

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
)

// NodeRuntime is a stub Runtime for Node.js functions.
type NodeRuntime struct {
	clk    clock.Clock
	logger *zap.Logger
}

func newNodeRuntime(clk clock.Clock, logger *zap.Logger) *NodeRuntime {
	return &NodeRuntime{clk: clk, logger: logger}
}

// lambdaLogStreamName returns an AWS-style log stream name for a new container
// instance. Format: YYYY/MM/DD/[$LATEST]<26-char lowercase hex>.
func (rt *NodeRuntime) lambdaLogStreamName() string {
	date := rt.clk.Now().UTC().Format("2006/01/02")
	var b [13]byte
	_, _ = rand.Read(b[:])
	return date + "/[$LATEST]" + hex.EncodeToString(b[:])
}

// CanHandle returns true for all currently supported Node.js runtime identifiers.
// nodejs18.x is excluded — it reached end-of-life on 2025-04-30 and is no
// longer supported by AWS Lambda. Attempting to create a function with nodejs18.x
// will return an InvalidParameterValueException, matching AWS behaviour.
func (rt *NodeRuntime) CanHandle(runtimeID string) bool {
	switch runtimeID {
	case "nodejs20.x", "nodejs22.x", "nodejs24.x":
		return true
	}
	return false
}

// Acquire returns a stub RuntimeInstance. The real implementation will start a
// container via the Docker daemon and wait for the Lambda Runtime API to be ready.
func (rt *NodeRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	return &nodeRuntimeInstance{
		logStream:    rt.lambdaLogStreamName(),
		logger:       rt.logger,
		functionName: fn.Name,
	}, nil
}

// Release is a no-op for the stub. The real implementation will return the
// container to a warm pool (healthy=true) or stop/remove it (healthy=false).
func (rt *NodeRuntime) Release(_ context.Context, _ RuntimeInstance, _ bool) {}

// nodeRuntimeInstance is the stub RuntimeInstance returned by NodeRuntime.Acquire.
type nodeRuntimeInstance struct {
	logStream    string
	healthy      bool
	logger       *zap.Logger
	functionName string
}

// LogStreamName returns the CloudWatch Logs stream name assigned to this instance.
func (i *nodeRuntimeInstance) LogStreamName() string { return i.logStream }

// FunctionName returns the Lambda function name for this stub instance.
func (i *nodeRuntimeInstance) FunctionName() string { return i.functionName }

// CodeHash returns empty string — the stub runtime has no real code.
func (i *nodeRuntimeInstance) CodeHash() string { return "" }

// Invoke returns an error payload indicating that Docker is required for real
// Lambda execution. The ContainerRuntime handles actual invocations when Docker
// is available.
func (i *nodeRuntimeInstance) Invoke(_ context.Context, _ []byte) (*InvokeResult, error) {
	i.healthy = true
	const msg = "Docker is not available. Lambda invocation requires Docker — mount /var/run/docker.sock into the container or install Docker on the host. If Docker was started after the emulator, restart the emulator."
	i.logger.Warn("lambda: invocation attempted but Docker is not available — returning stub error",
		zap.String("function", i.functionName))
	return &InvokeResult{
		StatusCode:    200,
		FunctionError: "Unhandled",
		Payload:       []byte(`{"errorMessage":"` + msg + `","errorType":"Runtime.DockerUnavailable"}`),
	}, nil
}

// Healthy reports whether this instance is safe to return to the warm pool.
func (i *nodeRuntimeInstance) Healthy() bool { return i.healthy }

// Close is a no-op for the stub. The real implementation will stop and remove
// the container.
func (i *nodeRuntimeInstance) Close() error { return nil }

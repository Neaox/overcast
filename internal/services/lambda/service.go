// Package lambda is a stub — handlers are implemented test-first.
//
// Architecture uses the Strategy pattern for runtime execution:
//
//	Runtime interface ← NodeRuntime (v1) | PythonRuntime (future) | GoRuntime (future)
//
// The Lambda handler never knows which runtime it's talking to. Adding a new
// runtime means implementing the Runtime interface and registering it in the
// RuntimeRegistry — nothing else changes.
//
// Implementation order (TDD):
//  1. CreateFunction / GetFunction / ListFunctions / DeleteFunction / UpdateFunctionCode
//  2. Invoke (synchronous) — stub response mode
//  3. Invoke (synchronous) — real Node.js execution via NodeRuntime
//  4. InvokeAsync
//  5. Event source mapping (SQS→Lambda)
package lambda

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

// Runtime is the Strategy interface for Lambda execution environments.
// Each supported runtime implements this interface.
//
// TypeScript analogy:
//
//	interface Runtime {
//	  canHandle(runtime: string): boolean
//	  invoke(fn: LambdaFunction, event: any): Promise<InvokeResult>
//	}
type Runtime interface {
	// CanHandle returns true if this runtime can execute functions with the
	// given runtime identifier (e.g. "nodejs20.x", "nodejs18.x").
	CanHandle(runtimeID string) bool

	// Invoke executes the function and returns the result.
	// event is the raw JSON payload. Returns the raw JSON response body.
	Invoke(fn *Function, event []byte) (*InvokeResult, error)
}

// InvokeResult holds the outcome of a Lambda invocation.
type InvokeResult struct {
	// StatusCode is the HTTP status code returned by the function handler.
	StatusCode int
	// Payload is the raw JSON response body.
	Payload []byte
	// FunctionError is non-empty if the function returned an error response
	// (i.e. X-Amz-Function-Error: Handled or Unhandled).
	FunctionError string
	// LogResult contains base64-encoded tail log output (last 4KB).
	LogResult string
}

// Function is the domain model for a stored Lambda function definition.
type Function struct {
	Name         string            `json:"name"`
	ARN          string            `json:"arn"`
	Runtime      string            `json:"runtime"`
	Handler      string            `json:"handler"`
	Role         string            `json:"role"`
	Description  string            `json:"description,omitempty"`
	Timeout      int               `json:"timeout"`
	MemorySize   int               `json:"memory_size"`
	Environment  map[string]string `json:"environment,omitempty"`
	CodeZip      []byte            `json:"code_zip,omitempty"` // base64-decoded zip
	CodeS3Bucket string            `json:"code_s3_bucket,omitempty"`
	CodeS3Key    string            `json:"code_s3_key,omitempty"`
	State        string            `json:"state"` // "Active", "Pending", "Inactive"
}

// Service implements router.Service for Lambda.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured Lambda Service with all supported runtimes registered.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, "lambda")
	runtimes := []Runtime{newNodeRuntime(cfg)}
	s := &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, log, clk, runtimes),
	}
	return s
}

func (s *Service) Name() string { return "lambda" }

// RegisterRoutes mounts Lambda REST endpoints.
// Lambda uses versioned REST paths, not a single-dispatch target header.
func (s *Service) RegisterRoutes(r chi.Router) {
	const apiBase = "/2015-03-31"

	r.Post(apiBase+"/functions", s.handler.DispatchFunctionOp)
	r.Get(apiBase+"/functions", s.handler.DispatchFunctionOp)
	r.Get(apiBase+"/functions/{name}", s.handler.DispatchFunctionOp)
	r.Put(apiBase+"/functions/{name}/code", s.handler.DispatchFunctionOp)
	r.Delete(apiBase+"/functions/{name}", s.handler.DispatchFunctionOp)
	r.Post(apiBase+"/functions/{name}/invocations", s.handler.InvokeFunction)
}

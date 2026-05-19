package lambda

// invoker.go — ServiceInvoker implements events.FunctionInvoker.
//
// InvokeAsync is called by the S3 notification dispatcher (and in future by
// any other service that needs to trigger a Lambda function asynchronously).
// It looks up the function by ARN, finds a suitable runtime, executes it,
// and records the invocation in the state store.
//
// If the function is not found, the call is a no-op: a warning is logged and
// nil is returned. This matches AWS behaviour where a misconfigured notification
// silently fails rather than breaking the originating operation.

import (
	"context"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"go.uber.org/zap"
)

// ServiceInvoker implements events.FunctionInvoker for the Lambda service.
type ServiceInvoker struct {
	store     *lambdaStore
	runtimes  *runtimeRegistry
	logger    *zap.Logger
	tracker   *instanceTracker
	logWriter events.LogWriter // nil until InitLogWriter is called
	cfg       *config.Config
	bus       *events.Bus // nil until InitBus is called
	clk       clock.Clock // nil until InitBus is called
}

// newServiceInvoker creates a new ServiceInvoker.
func newServiceInvoker(store *lambdaStore, runtimes *runtimeRegistry, logger *zap.Logger, tracker *instanceTracker) *ServiceInvoker {
	return &ServiceInvoker{store: store, runtimes: runtimes, logger: logger, tracker: tracker}
}

// InitBus wires the event bus and clock so the invoker can publish
// ServiceError events for invocation failures that would otherwise only
// appear in server logs.
func (inv *ServiceInvoker) InitBus(b *events.Bus, clk clock.Clock) {
	inv.bus = b
	inv.clk = clk
}

// publishError emits a ServiceError event onto the bus. It is a no-op when
// the bus has not been wired (e.g. in unit tests that don't need it).
func (inv *ServiceInvoker) publishError(ctx context.Context, operation, message, code string) {
	if inv.bus == nil {
		return
	}
	var t time.Time
	if inv.clk != nil {
		t = inv.clk.Now()
	} else {
		t = time.Now()
	}
	inv.bus.Publish(ctx, events.Event{
		Type:   events.ServiceError,
		Time:   t,
		Source: "lambda",
		Payload: events.ErrorPayload{
			Service:   "lambda",
			Operation: operation,
			Message:   message,
			Code:      code,
		},
	})
}

// InvokeAsync satisfies events.FunctionInvoker.
// It is safe to call from any goroutine.
func (inv *ServiceInvoker) InvokeAsync(ctx context.Context, functionARN string, payload []byte) error {
	name := functionNameFromARN(functionARN)

	fn, aerr := inv.store.getFunction(ctx, name)
	if aerr != nil {
		inv.logger.Debug("lambda: invokeAsync: state lookup failed",
			zap.String("arn", functionARN),
			zap.String("error", aerr.Message),
		)
		return nil
	}
	if fn == nil {
		inv.logger.Debug("lambda: invokeAsync: function not found",
			zap.String("arn", functionARN),
			zap.String("name", name),
		)
		return nil
	}
	if aerr := checkInvokableState(fn); aerr != nil {
		msg := invokableStateMessage(fn.State)
		inv.logger.Warn("lambda: invokeAsync: function not invokable",
			zap.String("function", name),
			zap.String("state", fn.State),
			zap.String("reason", msg),
		)
		inv.publishError(ctx, "Invoke", name+": "+msg, aerr.Code)
		return nil
	}

	// Record the invocation before executing. This makes delivery observable
	// in tests even when the runtime cannot run (e.g. missing code zip).
	if err := inv.store.addInvocation(ctx, fn, payload); err != nil {
		inv.logger.Warn("lambda: invokeAsync: failed to record invocation",
			zap.String("function", name),
			zap.Error(err),
		)
		// Non-fatal: continue to attempt execution.
	}

	// Find a runtime that can handle the function.
	var rt Runtime
	for _, r := range inv.runtimes.get() {
		if r.CanHandle(fn.Runtime) {
			rt = r
			break
		}
	}
	if rt == nil {
		inv.logger.Debug("lambda: invokeAsync: no runtime for function",
			zap.String("function", name),
			zap.String("runtime", fn.Runtime),
		)
		return nil
	}

	// Acquire a warm instance, execute the invocation, then release it.
	if inv.tracker != nil {
		inv.tracker.Acquire(name, payload)
	}
	inst, err := rt.Acquire(ctx, fn)
	if err != nil {
		if inv.tracker != nil {
			inv.tracker.Release(name, false, err.Error())
		}
		inv.logger.Error("lambda: invokeAsync: acquire instance failed",
			zap.String("function", name),
			zap.Error(err),
		)
		return nil
	}

	if inv.tracker != nil {
		inv.tracker.Ready(name)
	}

	// Ensure the log stream exists so container logs are captured.
	// Use the function's own region (from its ARN) so the log stream is
	// created in the correct regional log group.
	if inv.logWriter != nil {
		fnRegion := regionFromFunctionARN(fn.ARN)
		if fnRegion == "" {
			fnRegion = inv.cfg.Region
		}
		fnCtx := middleware.ContextWithRegion(ctx, fnRegion)
		if inv.tracker != nil {
			inv.tracker.SetLogRefs(name, fn.logGroupName(), inst.LogStreamName())
		}
		if lsErr := inv.logWriter.EnsureLogStream(fnCtx, fn.logGroupName(), inst.LogStreamName()); lsErr != nil {
			inv.logger.Debug("lambda: invokeAsync: ensure log stream", zap.String("function", name), zap.Error(lsErr))
		}
	}

	result, err := inst.Invoke(ctx, payload)
	healthy := err == nil
	rt.Release(ctx, inst, healthy)
	if inv.tracker != nil {
		success := err == nil && result != nil && result.FunctionError == ""
		reason := ""
		if err != nil {
			reason = err.Error()
		} else if result != nil && result.FunctionError != "" {
			reason = result.FunctionError
		}
		inv.tracker.Release(name, success, reason)
	}

	if err != nil {
		inv.logger.Error("lambda: invokeAsync: invocation error",
			zap.String("function", name),
			zap.Error(err),
		)
		return nil
	}

	if result.FunctionError != "" {
		inv.logger.Warn("lambda: invokeAsync: function returned error",
			zap.String("function", name),
			zap.String("function_error", result.FunctionError),
		)
	}

	return nil
}

// Invoke executes the named function synchronously and returns the result.
// Satisfies events.FunctionSyncInvoker.
// If the function is not found, no runtime is available, or the container
// fails to start, (nil, nil) is returned and the issue is logged — consistent
// with InvokeAsync's fail-silent approach for missing configuration.
//
// A non-nil *events.InvokeOutcome with FunctionError != "" means the function ran
// but returned a handled or unhandled error; the caller should decide whether
// to retry or discard the event.
func (inv *ServiceInvoker) Invoke(ctx context.Context, functionName string, payload []byte) (*events.InvokeOutcome, error) {
	fn, aerr := inv.store.getFunction(ctx, functionName)
	if aerr != nil {
		inv.logger.Debug("lambda: invoke: state lookup failed",
			zap.String("function", functionName),
			zap.String("error", aerr.Message),
		)
		return nil, nil
	}
	if fn == nil {
		inv.logger.Debug("lambda: invoke: function not found",
			zap.String("function", functionName),
		)
		return nil, nil
	}
	if aerr := checkInvokableState(fn); aerr != nil {
		msg := invokableStateMessage(fn.State)
		inv.logger.Warn("lambda: invoke: function not invokable",
			zap.String("function", functionName),
			zap.String("state", fn.State),
			zap.String("reason", msg),
		)
		inv.publishError(ctx, "Invoke", functionName+": "+msg, aerr.Code)
		return nil, nil
	}

	var rt Runtime
	for _, r := range inv.runtimes.get() {
		if r.CanHandle(fn.Runtime) {
			rt = r
			break
		}
	}
	if rt == nil {
		inv.logger.Debug("lambda: invoke: no runtime",
			zap.String("function", functionName),
			zap.String("runtime", fn.Runtime),
		)
		return nil, nil
	}

	if inv.tracker != nil {
		inv.tracker.Acquire(functionName, payload)
	}
	inst, err := rt.Acquire(ctx, fn)
	if err != nil {
		if inv.tracker != nil {
			inv.tracker.Release(functionName, false, err.Error())
		}
		inv.logger.Error("lambda: invoke: acquire instance failed",
			zap.String("function", functionName),
			zap.Error(err),
		)
		return nil, err
	}

	if inv.tracker != nil {
		inv.tracker.Ready(functionName)
	}

	// Ensure the log stream exists so container logs are captured.
	// Use the function's own region (from its ARN) so the log stream is
	// created in the correct regional log group.
	if inv.logWriter != nil {
		fnRegion := regionFromFunctionARN(fn.ARN)
		if fnRegion == "" {
			fnRegion = inv.cfg.Region
		}
		fnCtx := middleware.ContextWithRegion(ctx, fnRegion)
		if inv.tracker != nil {
			inv.tracker.SetLogRefs(functionName, fn.logGroupName(), inst.LogStreamName())
		}
		if lsErr := inv.logWriter.EnsureLogStream(fnCtx, fn.logGroupName(), inst.LogStreamName()); lsErr != nil {
			inv.logger.Debug("lambda: invoke: ensure log stream", zap.String("function", functionName), zap.Error(lsErr))
		}
	}

	result, err := inst.Invoke(ctx, payload)
	healthy := err == nil
	rt.Release(ctx, inst, healthy)
	if inv.tracker != nil {
		success := err == nil && result != nil && result.FunctionError == ""
		reason := ""
		if err != nil {
			reason = err.Error()
		} else if result != nil && result.FunctionError != "" {
			reason = result.FunctionError
		}
		inv.tracker.Release(functionName, success, reason)
	}
	if err != nil {
		return nil, err
	}

	return &events.InvokeOutcome{
		Payload:       result.Payload,
		FunctionError: result.FunctionError,
	}, nil
}

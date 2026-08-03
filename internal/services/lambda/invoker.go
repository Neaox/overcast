package lambda

// invoker.go — ServiceInvoker implements events.FunctionInvoker,
// events.FunctionEventInvoker and events.FunctionSyncInvoker.
//
// InvokeEvent is called by any service that needs to trigger a Lambda function
// asynchronously — the S3 notification dispatcher, EventBridge/Scheduler
// targets, SNS subscription fan-out. It looks up the function by ARN, finds a
// suitable runtime, executes it, and records the invocation in the state store.
//
// InvokeAsync is the same call for callers that do not act on the outcome: it
// swallows the error, matching AWS behaviour where a misconfigured notification
// silently fails rather than breaking the originating operation. SNS does act
// on it — an undeliverable notification has to reach the subscription's
// dead-letter queue — so it calls InvokeEvent directly.

import (
	"context"
	"fmt"
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
//
// Delivery problems are logged and swallowed: S3 notification configs,
// EventBridge targets and Scheduler targets all treat a misconfigured
// destination the way AWS does — the originating operation still succeeds.
// Callers that need the outcome use InvokeEvent instead.
func (inv *ServiceInvoker) InvokeAsync(ctx context.Context, functionARN string, payload []byte) error {
	if err := inv.InvokeEvent(ctx, functionARN, payload); err != nil {
		inv.logger.Debug("lambda: invokeAsync: event not accepted",
			zap.String("arn", functionARN),
			zap.Error(err),
		)
	}
	return nil
}

// InvokeEvent satisfies events.FunctionEventInvoker.
// It is safe to call from any goroutine.
//
// The returned error mirrors what real Lambda answers synchronously to an
// `InvocationType=Event` call, and nothing more: the function does not exist,
// is not in an invokable state, or is throttled. Once the event is accepted,
// anything that goes wrong while running it — no runtime for the declared
// language, a container that will not start, a handler that raises — is logged
// but reported as a successful delivery, because on AWS the event source has
// already had its 202 by then and the failure belongs to Lambda's own retry and
// dead-letter machinery.
func (inv *ServiceInvoker) InvokeEvent(ctx context.Context, functionARN string, payload []byte) error {
	name := functionNameFromARN(functionARN)

	fn, aerr := inv.store.getFunction(ctx, name)
	if aerr != nil {
		inv.logger.Debug("lambda: invokeEvent: state lookup failed",
			zap.String("arn", functionARN),
			zap.String("error", aerr.Message),
		)
		return fmt.Errorf("lambda %s: %s", name, aerr.Message)
	}
	if fn == nil {
		inv.logger.Debug("lambda: invokeEvent: function not found",
			zap.String("arn", functionARN),
			zap.String("name", name),
		)
		return fmt.Errorf("lambda %s: function not found", name)
	}
	if aerr := checkInvokableState(fn); aerr != nil {
		msg := invokableStateMessage(fn.State)
		inv.logger.Warn("lambda: invokeEvent: function not invokable",
			zap.String("function", name),
			zap.String("state", fn.State),
			zap.String("reason", msg),
		)
		inv.publishError(ctx, "Invoke", name+": "+msg, aerr.Code)
		return fmt.Errorf("lambda %s: %s", name, msg)
	}

	// Record the invocation before executing. This makes delivery observable
	// in tests even when the runtime cannot run (e.g. missing code zip).
	if err := inv.store.addInvocation(ctx, fn, payload); err != nil {
		inv.logger.Warn("lambda: invokeEvent: failed to record invocation",
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
		inv.logger.Debug("lambda: invokeEvent: no runtime for function",
			zap.String("function", name),
			zap.String("runtime", fn.Runtime),
		)
		return nil
	}

	// Acquire a warm instance, execute the invocation, then release it.
	tracked := inv.tracker.Begin(name, payload)
	inst, err := rt.Acquire(ctx, fn)
	if err != nil {
		tracked.Abandon(err.Error())
		if _, throttled := asThrottle(err); throttled {
			inv.logger.Warn("lambda: invokeEvent: throttled",
				zap.String("function", name), zap.Error(err))
			return fmt.Errorf("lambda %s: throttled: %w", name, err)
		}
		inv.logger.Error("lambda: invokeEvent: acquire instance failed",
			zap.String("function", name),
			zap.Error(err),
		)
		return nil
	}
	tracked.Bind(inst)
	tracked.Ready()
	if err := awaitRuntimeReady(ctx, inv.cfg, inst); err != nil {
		rt.Release(ctx, inst, false)
		tracked.Abandon(err.Error())
		inv.logger.Error("lambda: invokeEvent: runtime init failed",
			zap.String("function", name),
			zap.Error(err),
		)
		return nil
	}
	tracked.Running()

	// Ensure the log stream exists so container logs are captured.
	// Use the function's own region (from its ARN) so the log stream is
	// created in the correct regional log group.
	if inv.logWriter != nil {
		fnRegion := regionFromFunctionARN(fn.ARN)
		if fnRegion == "" {
			fnRegion = inv.cfg.Region
		}
		fnCtx := middleware.ContextWithRegion(ctx, fnRegion)
		tracked.SetLogRefs(fn.logGroupName(), inst.LogStreamName())
		if lsErr := inv.logWriter.EnsureLogStream(fnCtx, fn.logGroupName(), inst.LogStreamName()); lsErr != nil {
			inv.logger.Debug("lambda: invokeEvent: ensure log stream", zap.String("function", name), zap.Error(lsErr))
		}
	}

	// No tail: an asynchronous invocation has no caller to hand a LogResult to.
	result, err := inst.Invoke(ctx, payload, InvokeOptions{})
	healthy := err == nil
	rt.Release(ctx, inst, healthy)
	tracked.Finish(invocationOutcome(err, result))

	if err != nil {
		inv.logger.Error("lambda: invokeEvent: invocation error",
			zap.String("function", name),
			zap.Error(err),
		)
		return nil
	}

	if result.FunctionError != "" {
		inv.logger.Warn("lambda: invokeEvent: function returned error",
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

	tracked := inv.tracker.Begin(functionName, payload)
	inst, err := rt.Acquire(ctx, fn)
	if err != nil {
		tracked.Abandon(err.Error())
		if _, throttled := asThrottle(err); throttled {
			inv.logger.Warn("lambda: invoke: throttled",
				zap.String("function", functionName), zap.Error(err))
			return nil, err
		}
		inv.logger.Error("lambda: invoke: acquire instance failed",
			zap.String("function", functionName),
			zap.Error(err),
		)
		return nil, err
	}
	tracked.Bind(inst)
	tracked.Ready()
	if err := awaitRuntimeReady(ctx, inv.cfg, inst); err != nil {
		rt.Release(ctx, inst, false)
		tracked.Abandon(err.Error())
		inv.logger.Error("lambda: invoke: runtime init failed",
			zap.String("function", functionName),
			zap.Error(err),
		)
		return nil, err
	}
	tracked.Running()

	// Ensure the log stream exists so container logs are captured.
	// Use the function's own region (from its ARN) so the log stream is
	// created in the correct regional log group.
	if inv.logWriter != nil {
		fnRegion := regionFromFunctionARN(fn.ARN)
		if fnRegion == "" {
			fnRegion = inv.cfg.Region
		}
		fnCtx := middleware.ContextWithRegion(ctx, fnRegion)
		tracked.SetLogRefs(fn.logGroupName(), inst.LogStreamName())
		if lsErr := inv.logWriter.EnsureLogStream(fnCtx, fn.logGroupName(), inst.LogStreamName()); lsErr != nil {
			inv.logger.Debug("lambda: invoke: ensure log stream", zap.String("function", functionName), zap.Error(lsErr))
		}
	}

	// No tail: InvokeOutcome carries no log field, so nothing would read it.
	result, err := inst.Invoke(ctx, payload, InvokeOptions{})
	healthy := err == nil
	rt.Release(ctx, inst, healthy)
	tracked.Finish(invocationOutcome(err, result))
	if err != nil {
		return nil, err
	}

	return &events.InvokeOutcome{
		Payload:       result.Payload,
		FunctionError: result.FunctionError,
	}, nil
}

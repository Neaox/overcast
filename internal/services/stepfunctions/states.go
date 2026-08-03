package stepfunctions

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
)

// Per-state-type execution, and the Retry/Catch machinery that wraps the three
// state types that can fail. The driver that sequences states, the ASL error
// model and the input/output pipeline live in interpreter.go.

// ─── Individual state types ───────────────────────────────────────────────────

func (in *interpreter) runFail(state *aslState, effective any, ctxObj map[string]any) *stateError {
	errName := state.Error
	cause := state.Cause
	if state.ErrorPath != "" {
		value, err := evaluateTemplateExpression(state.ErrorPath, effective, ctxObj)
		if err != nil {
			return newStateError(errRuntime, "%s", err.Error())
		}
		text, ok := value.(string)
		if !ok {
			return newStateError(errRuntime, "ErrorPath must resolve to a string")
		}
		errName = text
	}
	if state.CausePath != "" {
		value, err := evaluateTemplateExpression(state.CausePath, effective, ctxObj)
		if err != nil {
			return newStateError(errRuntime, "%s", err.Error())
		}
		text, ok := value.(string)
		if !ok {
			return newStateError(errRuntime, "CausePath must resolve to a string")
		}
		cause = text
	}
	if errName == "" {
		errName = errRuntime
	}
	return &stateError{name: errName, cause: cause}
}

func (in *interpreter) runChoice(name string, state *aslState, effective any, ctxObj map[string]any) (any, string, bool, *stateError) {
	for _, rule := range state.Choices {
		matched, err := evaluateChoiceRule(rule, effective, ctxObj)
		if err != nil {
			return nil, "", false, newStateError(errRuntime, "%s", err.Error())
		}
		if matched {
			output, serr := applyOutputPath(state, effective, ctxObj)
			if serr != nil {
				return nil, "", false, serr
			}
			in.recordExit(name, state.Type, output)
			return output, rule.Next, false, nil
		}
	}
	if state.Default == "" {
		return nil, "", false, newStateError(errNoChoiceMatched, "no choice rule matched and the state has no Default transition")
	}
	output, serr := applyOutputPath(state, effective, ctxObj)
	if serr != nil {
		return nil, "", false, serr
	}
	in.recordExit(name, state.Type, output)
	return output, state.Default, false, nil
}

func (in *interpreter) runWait(ctx context.Context, name string, state *aslState, effective any, ctxObj map[string]any) (any, string, bool, *stateError) {
	delay, serr := in.waitDuration(state, effective, ctxObj)
	if serr != nil {
		return nil, "", false, serr
	}
	if !in.pause(ctx, delay) {
		return nil, "", false, in.unwindReason(name)
	}
	output, serr := applyOutputPath(state, effective, ctxObj)
	if serr != nil {
		return nil, "", false, serr
	}
	in.recordExit(name, state.Type, output)
	return output, state.Next, state.End, nil
}

// waitDuration resolves a Wait state's Seconds/SecondsPath/Timestamp/
// TimestampPath into a delay measured against the injected clock.
func (in *interpreter) waitDuration(state *aslState, effective any, ctxObj map[string]any) (time.Duration, *stateError) {
	now := in.handler.clk.Now()
	switch {
	case state.Seconds != nil:
		return time.Duration(*state.Seconds) * time.Second, nil
	case state.SecondsPath != "":
		value, err := selectPath(effective, ctxObj, state.SecondsPath)
		if err != nil {
			return 0, newStateError(errRuntime, "%s", err.Error())
		}
		seconds, ok := toNumber(value)
		if !ok {
			return 0, newStateError(errRuntime, "SecondsPath %q did not resolve to a number", state.SecondsPath)
		}
		return time.Duration(seconds * float64(time.Second)), nil
	case state.Timestamp != "":
		target, err := parseASLTimestamp(state.Timestamp)
		if err != nil {
			return 0, newStateError(errRuntime, "%s", err.Error())
		}
		return maxDuration(target.Sub(now), 0), nil
	case state.TimestampPath != "":
		value, err := selectPath(effective, ctxObj, state.TimestampPath)
		if err != nil {
			return 0, newStateError(errRuntime, "%s", err.Error())
		}
		text, ok := value.(string)
		if !ok {
			return 0, newStateError(errRuntime, "TimestampPath %q did not resolve to a string", state.TimestampPath)
		}
		target, err := parseASLTimestamp(text)
		if err != nil {
			return 0, newStateError(errRuntime, "%s", err.Error())
		}
		return maxDuration(target.Sub(now), 0), nil
	}
	return 0, nil
}

// pause blocks for d on the injected clock, or until the execution budget
// runs out. It reports whether the full pause elapsed. The timer is always
// stopped, so a wait cut short by the deadline leaves nothing pending.
func (in *interpreter) pause(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := in.handler.clk.Timer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func maxDuration(d, floor time.Duration) time.Duration {
	if d < floor {
		return floor
	}
	return d
}

func (in *interpreter) runPass(name string, state *aslState, raw, effective any, ctxObj map[string]any) (any, string, bool, *stateError) {
	result := effective
	if len(state.Parameters) > 0 {
		rendered, err := renderPayloadTemplate(state.Parameters, effective, ctxObj)
		if err != nil {
			return nil, "", false, newStateError(errParameterPathFailure, "%s", err.Error())
		}
		result = rendered
	}
	if len(state.Result) > 0 {
		var decoded any
		if err := json.Unmarshal(state.Result, &decoded); err != nil {
			return nil, "", false, newStateError(errRuntime, "Result is not valid JSON: %v", err)
		}
		result = decoded
	}
	output, serr := in.applyResult(state, raw, result, ctxObj)
	if serr != nil {
		return nil, "", false, serr
	}
	in.recordExit(name, state.Type, output)
	return output, state.Next, state.End, nil
}

// ─── Retry / Catch ────────────────────────────────────────────────────────────

// runRetryable executes a Task, Parallel or Map state under its Retry and
// Catch policies. States.Runtime is never retried or caught — AWS documents it
// that way, and it is also what keeps an Overcast "not supported" failure from
// being swallowed by a `Catch` on States.ALL.
func (in *interpreter) runRetryable(ctx context.Context, name string, state *aslState, raw, effective any) (any, string, bool, *stateError) {
	attempts := make([]int, len(state.Retry))
	retryCount := 0

	for {
		entered := in.handler.clk.Now()
		ctxObj := in.stateContext(name, entered, retryCount, in.mapItemFromContext())

		result, serr := in.runRetryableOnce(ctx, name, state, effective, ctxObj)
		if serr == nil {
			output, applyErr := in.applyResult(state, raw, result, ctxObj)
			if applyErr != nil {
				return nil, "", false, applyErr
			}
			in.recordExit(name, state.Type, output)
			return output, state.Next, state.End, nil
		}
		if serr.name == errRuntime {
			return nil, "", false, serr
		}

		if idx := matchRetrier(state.Retry, attempts, serr); idx >= 0 {
			delay := retryDelay(state.Retry[idx], attempts[idx])
			attempts[idx]++
			retryCount++
			if !in.pause(ctx, delay) {
				return nil, "", false, in.unwindReason(name)
			}
			continue
		}

		catcher := matchCatcher(state.Catch, serr)
		if catcher == nil {
			return nil, "", false, serr
		}
		errorOutput := map[string]any{"Error": serr.name, "Cause": serr.cause}
		output, applyErr := applyResultPath(catcher.ResultPath, raw, errorOutput)
		if applyErr != nil {
			return nil, "", false, applyErr
		}
		in.recordExit(name, state.Type, output)
		return output, catcher.Next, false, nil
	}
}

// runRetryableOnce runs one attempt of a Task, Parallel or Map state.
func (in *interpreter) runRetryableOnce(ctx context.Context, name string, state *aslState, effective any, ctxObj map[string]any) (any, *stateError) {
	switch state.Type {
	case stateTypeTask:
		return in.runTask(ctx, name, state, effective, ctxObj)
	case stateTypeParallel:
		return in.runParallel(ctx, state, effective, ctxObj)
	case stateTypeMap:
		return in.runMap(ctx, state, effective, ctxObj)
	}
	return nil, unsupportedError("state type %q", state.Type)
}

// matchRetrier returns the index of the first retrier that matches the error
// and still has attempts left, or -1.
func matchRetrier(retriers []aslRetrier, attempts []int, serr *stateError) int {
	for i, retrier := range retriers {
		if !errorMatches(retrier.ErrorEquals, serr) {
			continue
		}
		if attempts[i] >= retrier.maxAttempts() {
			return -1
		}
		return i
	}
	return -1
}

func matchCatcher(catchers []aslCatcher, serr *stateError) *aslCatcher {
	for i := range catchers {
		if errorMatches(catchers[i].ErrorEquals, serr) {
			return &catchers[i]
		}
	}
	return nil
}

// errorMatches implements ASL's ErrorEquals matching. States.ALL is a wildcard
// over every error name except States.Runtime, which AWS documents as neither
// retriable nor catchable.
func errorMatches(errorEquals []string, serr *stateError) bool {
	for _, want := range errorEquals {
		if want == errAll && serr.name != errRuntime {
			return true
		}
		if want == serr.name {
			return true
		}
	}
	return false
}

// retryDelay computes the backoff for the next attempt of a retrier.
func retryDelay(retrier aslRetrier, priorAttempts int) time.Duration {
	seconds := retrier.interval() * math.Pow(retrier.backoffRate(), float64(priorAttempts))
	if retrier.MaxDelaySeconds != nil && *retrier.MaxDelaySeconds > 0 && seconds > *retrier.MaxDelaySeconds {
		seconds = *retrier.MaxDelaySeconds
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// ─── Parallel and Map ─────────────────────────────────────────────────────────

// runParallel runs every branch against the same effective input and returns
// their outputs as an array. Branches run sequentially: the interpreter is
// synchronous by design and a local emulator gains nothing from real
// concurrency here, while deterministic ordering makes assertions stable.
func (in *interpreter) runParallel(ctx context.Context, state *aslState, effective any, _ map[string]any) (any, *stateError) {
	in.hist.add(in.handler.clk.Now(), HistoryEvent{Type: evtParallelStateStarted})
	results := make([]any, 0, len(state.Branches))
	for i, branch := range state.Branches {
		output, serr := in.runBranch(ctx, branch, cloneJSON(effective))
		if serr != nil {
			in.hist.add(in.handler.clk.Now(), HistoryEvent{Type: evtParallelStateFailed})
			if serr.name == errRuntime {
				return nil, serr
			}
			return nil, newStateError(errBranchFailed, "branch %d failed with %s: %s", i, serr.name, serr.cause)
		}
		results = append(results, output)
	}
	in.hist.add(in.handler.clk.Now(), HistoryEvent{Type: evtParallelStateSucceeded})
	return results, nil
}

// runMap iterates an inline ItemProcessor over the array selected by ItemsPath.
func (in *interpreter) runMap(ctx context.Context, state *aslState, effective any, ctxObj map[string]any) (any, *stateError) {
	if len(state.ItemReader) > 0 {
		return nil, unsupportedError("Map ItemReader (reading items from S3)")
	}
	if len(state.ItemBatcher) > 0 {
		return nil, unsupportedError("Map ItemBatcher")
	}
	if len(state.ResultWriter) > 0 {
		return nil, unsupportedError("Map ResultWriter")
	}
	processor := state.processor()
	if mode := processorMode(processor); mode != "" && !strings.EqualFold(mode, "INLINE") {
		return nil, unsupportedError("distributed Map (ProcessorConfig.Mode %q) — only inline Map is interpreted", mode)
	}

	source := effective
	if state.ItemsPath != "" {
		selected, err := selectPath(effective, ctxObj, state.ItemsPath)
		if err != nil {
			return nil, newStateError(errRuntime, "%s", err.Error())
		}
		source = selected
	}
	items, ok := source.([]any)
	if !ok {
		return nil, newStateError(errRuntime, "Map state input is not an array (ItemsPath %q)", state.ItemsPath)
	}

	in.hist.add(in.handler.clk.Now(), HistoryEvent{
		Type:            evtMapStateStarted,
		MapStateStarted: &mapStateStartedDetails{Length: int64(len(items))},
	})

	results := make([]any, 0, len(items))
	for index, item := range items {
		in.hist.add(in.handler.clk.Now(), HistoryEvent{
			Type:                evtMapIterationStarted,
			MapIterationStarted: &mapIterationDetails{Index: int64(index)},
		})

		iterationInput := item
		itemCtx := map[string]any{"Index": float64(index), "Value": item}
		if selector := mapItemSelector(state); len(selector) > 0 {
			scoped := in.stateContext("", in.handler.clk.Now(), 0, itemCtx)
			rendered, err := renderPayloadTemplate(selector, item, scoped)
			if err != nil {
				in.hist.add(in.handler.clk.Now(), HistoryEvent{
					Type:               evtMapIterationFailed,
					MapIterationFailed: &mapIterationDetails{Index: int64(index)},
				})
				return nil, newStateError(errParameterPathFailure, "%s", err.Error())
			}
			iterationInput = rendered
		}

		output, serr := in.runMapIteration(ctx, processor, iterationInput, itemCtx)
		if serr != nil {
			in.hist.add(in.handler.clk.Now(), HistoryEvent{
				Type:               evtMapIterationFailed,
				MapIterationFailed: &mapIterationDetails{Index: int64(index)},
			})
			in.hist.add(in.handler.clk.Now(), HistoryEvent{Type: evtMapStateFailed})
			if serr.name == errRuntime {
				return nil, serr
			}
			return nil, newStateError(errTaskFailed, "Map iteration %d failed with %s: %s", index, serr.name, serr.cause)
		}
		in.hist.add(in.handler.clk.Now(), HistoryEvent{
			Type:                  evtMapIterationSucceeded,
			MapIterationSucceeded: &mapIterationDetails{Index: int64(index)},
		})
		results = append(results, output)
	}

	in.hist.add(in.handler.clk.Now(), HistoryEvent{Type: evtMapStateSucceeded})
	return results, nil
}

// runMapIteration installs Map.Item on the context object for the duration of
// one iteration and restores it afterwards, so nested states see $$.Map.Item.
func (in *interpreter) runMapIteration(ctx context.Context, processor *aslBranch, input any, itemCtx map[string]any) (any, *stateError) {
	previous, had := in.baseCtx["Map"]
	in.baseCtx["Map"] = map[string]any{"Item": itemCtx}
	defer func() {
		if had {
			in.baseCtx["Map"] = previous
			return
		}
		delete(in.baseCtx, "Map")
	}()
	return in.runBranch(ctx, processor, input)
}

// mapItemSelector returns the per-item payload template: ItemSelector, or the
// legacy Parameters spelling that pre-ItemProcessor Map states used.
func mapItemSelector(state *aslState) json.RawMessage {
	if len(state.ItemSelector) > 0 {
		return state.ItemSelector
	}
	if state.ItemProcessor == nil && len(state.Parameters) > 0 {
		return state.Parameters
	}
	return nil
}

// processorMode reads ProcessorConfig.Mode from a Map's ItemProcessor.
func processorMode(processor *aslBranch) string {
	if processor == nil || len(processor.ProcessorConfig) == 0 {
		return ""
	}
	var config struct {
		Mode string `json:"Mode"`
	}
	if json.Unmarshal(processor.ProcessorConfig, &config) != nil {
		return ""
	}
	return config.Mode
}

package stepfunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The Amazon States Language interpreter.
//
// Executions run in-process, matching the single-node deterministic-clock
// architecture the rest of Overcast uses. StartExecution answers as soon as
// the RUNNING record is persisted and interprets on a tracked goroutine, as
// AWS does; only StartSyncExecution and a `states:startExecution.sync` child
// wait for the terminal state (see executionMode in execution_ops.go). All
// eight ASL state types are interpreted. Everything Overcast cannot interpret
// — an unsupported Task resource, `.waitForTaskToken`, distributed Map, the
// JSONata query language — fails the execution loudly with an AWS-shaped error
// surfaced through DescribeExecution and GetExecutionHistory. There is no
// silent pass-through; see docs/plans/full-emulation-priority.md §2.1.

// Standard ASL error names.
const (
	errAll                    = "States.ALL"
	errTimeout                = "States.Timeout"
	errTaskFailed             = "States.TaskFailed"
	errRuntime                = "States.Runtime"
	errBranchFailed           = "States.BranchFailed"
	errNoChoiceMatched        = "States.NoChoiceMatched"
	errParameterPathFailure   = "States.ParameterPathFailure"
	errResultPathMatchFailure = "States.ResultPathMatchFailure"
)

// Execution status values, as DescribeExecution reports them.
const (
	statusRunning   = "RUNNING"
	statusSucceeded = "SUCCEEDED"
	statusFailed    = "FAILED"
	statusTimedOut  = "TIMED_OUT"
	statusAborted   = "ABORTED"
)

const (
	// maxHistoryEvents matches AWS's 25,000-event cap on a Standard
	// execution's history. Reaching it fails the execution loudly, which is
	// also what stops a runaway Choice loop.
	maxHistoryEvents = 25000

	// maxNestedExecutionDepth bounds `states:startExecution.sync` recursion so
	// a state machine that starts itself terminates with an honest error
	// instead of exhausting the stack.
	maxNestedExecutionDepth = 10

	// defaultExecutionTimeout is used when the config carries no budget (a
	// hand-built *config.Config rather than one from config.Load).
	defaultExecutionTimeout = 30 * time.Second
)

// stateError is an ASL failure: an error name plus a human-readable cause.
// It is what DescribeExecution's error/cause fields and the *Failed history
// events carry.
type stateError struct {
	name  string
	cause string
	// aborted marks an unwind that StopExecution asked for, so the execution
	// ends ABORTED rather than being reported as a failure or a timeout.
	aborted bool
	// budgetExpired marks the execution's own wall-clock budget running out,
	// which is the only States.Timeout that ends an execution TIMED_OUT. A
	// Task's TimeoutSeconds and a Fail state spelling States.Timeout are both
	// ordinary failures of a still-healthy execution, exactly as on AWS.
	budgetExpired bool
}

func (e *stateError) Error() string { return e.name + ": " + e.cause }

func newStateError(name, format string, args ...any) *stateError {
	return &stateError{name: name, cause: fmt.Sprintf(format, args...)}
}

// unsupportedError is the loud failure Overcast raises for valid ASL it does
// not interpret. States.Runtime is deliberate: AWS documents it as neither
// retriable nor catchable, so a `Catch` on States.ALL cannot swallow an
// Overcast gap and turn it back into a silent pass-through.
func unsupportedError(format string, args ...any) *stateError {
	return &stateError{name: errRuntime, cause: "Overcast does not support " + fmt.Sprintf(format, args...)}
}

// isJSONPath reports whether a QueryLanguage field names the only query
// language Overcast evaluates. An absent field inherits JSONPath, which is
// also ASL's own default.
func isJSONPath(queryLanguage string) bool {
	return queryLanguage == "" || strings.EqualFold(queryLanguage, "JSONPath")
}

// unsupportedStateFields reports the first field on a state that Overcast
// accepts structurally but cannot evaluate.
//
// QueryLanguage is a per-state field as well as a top-level one, so a JSONata
// state inside a JSONPath state machine is legal ASL that the definition-level
// check in runBranch never sees. Left unread it was accepted, ignored, and its
// Output dropped — the execution then answered SUCCEEDED with the state's
// input, which is exactly the silent pass-through §2.1 forbids and worse than
// the stub this engine replaced. The same applies to Output on its own and to
// Assign (variables), whose consumers otherwise fail later with a confusing
// States.ParameterPathFailure about a path that was never assigned.
//
// This is a run-time refusal rather than a CreateStateMachine one on purpose:
// all three are valid ASL, and validateDefinitionForCreate deliberately admits
// valid ASL that Overcast cannot interpret so CDK and CloudFormation deploys
// keep working. It is the same contract top-level JSONata already had.
func unsupportedStateFields(name string, state *aslState) *stateError {
	if !isJSONPath(state.QueryLanguage) {
		return unsupportedError("the %s query language on state %q — only JSONPath states are interpreted", state.QueryLanguage, name)
	}
	if len(state.Output) > 0 {
		return unsupportedError("the JSONata Output field on state %q", name)
	}
	if len(state.Assign) > 0 {
		return unsupportedError("Assign (variables) on state %q", name)
	}
	return nil
}

// interpreter carries everything one execution needs. One is built per
// execution (and per nested `.sync` child execution); it owns no goroutines,
// timers or tickers of its own.
type interpreter struct {
	handler *Handler
	region  string
	hist    *historyRecorder
	// run is the live registration for this execution. It is how an unwind
	// caused by StopExecution is told apart from the budget running out.
	run     *executionRun
	baseCtx map[string]any
	// depth counts nested `states:startExecution.sync` levels so recursion
	// terminates with an honest error rather than a stack overflow.
	depth int
}

// executionOutcome is the terminal result of one execution.
type executionOutcome struct {
	status string
	output string
	err    *stateError
	events []HistoryEvent
}

// executionTimeout returns the wall-clock budget for one execution: the
// configured ceiling, lowered (never raised) by the definition's own
// top-level TimeoutSeconds.
func (h *Handler) executionTimeout(def *aslBranch) time.Duration {
	budget := h.cfg.StepFunctionsExecutionTimeout
	if budget <= 0 {
		budget = defaultExecutionTimeout
	}
	if def != nil && def.TimeoutSeconds > 0 {
		if declared := time.Duration(def.TimeoutSeconds) * time.Second; declared < budget {
			budget = declared
		}
	}
	return budget
}

// runExecution interprets one state machine to completion and returns the
// terminal status, output and history. The caller persists them.
func (h *Handler) runExecution(ctx context.Context, sm *StateMachine, exec *Execution, def *aslBranch, region string, depth int, run *executionRun) executionOutcome {
	// A hard wall-clock bound on the run. It is a runaway guard rather than a
	// request timeout: StartExecution has already answered by the time this
	// runs, and only the synchronous callers still hold a request open. It
	// holds whatever clock is injected. The cancel is deferred so no timer
	// outlives the execution.
	budget := h.executionTimeout(def)
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	in := &interpreter{
		handler: h,
		region:  region,
		hist:    run.hist,
		run:     run,
		depth:   depth,
	}
	in.baseCtx = in.buildBaseContext(sm, exec)

	input, err := decodeExecutionInput(exec.Input)
	if err != nil {
		return in.finish(statusFailed, "", newStateError(errRuntime, "%s", err.Error()))
	}

	output, serr := in.runBranch(runCtx, def, input)
	if serr != nil {
		return in.finish(statusForError(serr), "", serr)
	}
	encoded, encErr := encodeJSON(output)
	if encErr != nil {
		return in.finish(statusFailed, "", newStateError(errRuntime, "%s", encErr.Error()))
	}
	return in.finish(statusSucceeded, encoded, nil)
}

// executionStartedEvent builds the ExecutionStarted event every execution
// opens with. startExecution records it before the interpreter is handed the
// run, so an execution that has been acknowledged always has a history — even
// one that fails before the interpreter can run.
func executionStartedEvent(sm *StateMachine, exec *Execution) HistoryEvent {
	return HistoryEvent{
		Type: evtExecutionStarted,
		ExecutionStarted: &executionStartedDetails{
			Input:        exec.Input,
			InputDetails: &executionDataDetails{},
			RoleArn:      sm.RoleArn,
		},
	}
}

// statusForError maps a terminal ASL error to an execution status. AWS
// reports a timed-out execution as TIMED_OUT and a stopped one as ABORTED,
// neither of which is a FAILED execution.
func statusForError(serr *stateError) string {
	switch {
	case serr.aborted:
		return statusAborted
	case serr.budgetExpired:
		return statusTimedOut
	default:
		return statusFailed
	}
}

// finish records the terminal history event and returns the outcome.
func (in *interpreter) finish(status, output string, serr *stateError) executionOutcome {
	now := in.handler.clk.Now()
	switch status {
	case statusSucceeded:
		in.hist.add(now, HistoryEvent{
			Type: evtExecutionSucceeded,
			ExecutionSucceeded: &executionSucceededDetails{
				Output:        output,
				OutputDetails: &executionDataDetails{},
			},
		})
	case statusTimedOut:
		in.hist.add(now, HistoryEvent{
			Type:              evtExecutionTimedOut,
			ExecutionTimedOut: &errorCauseDetails{Error: serr.name, Cause: serr.cause},
		})
	case statusAborted:
		in.hist.add(now, HistoryEvent{
			Type:             evtExecutionAborted,
			ExecutionAborted: &errorCauseDetails{Error: serr.name, Cause: serr.cause},
		})
	default:
		details := &errorCauseDetails{}
		if serr != nil {
			details.Error = serr.name
			details.Cause = serr.cause
		}
		in.hist.add(now, HistoryEvent{Type: evtExecutionFailed, ExecutionFailed: details})
	}
	return executionOutcome{status: status, output: output, err: serr, events: in.hist.snapshot()}
}

// buildBaseContext assembles the parts of the ASL context object ($$) that do
// not change during the execution.
func (in *interpreter) buildBaseContext(sm *StateMachine, exec *Execution) map[string]any {
	var parsedInput any
	if exec.Input != "" {
		_ = json.Unmarshal([]byte(exec.Input), &parsedInput)
	}
	return map[string]any{
		"Execution": map[string]any{
			"Id":        exec.ExecutionArn,
			"Name":      exec.Name,
			"Input":     parsedInput,
			"RoleArn":   sm.RoleArn,
			"StartTime": exec.StartDate.UTC().Format(time.RFC3339Nano),
		},
		"StateMachine": map[string]any{
			"Id":   sm.ARN,
			"Name": sm.Name,
		},
	}
}

// stateContext returns the context object for one state evaluation.
func (in *interpreter) stateContext(name string, entered time.Time, retryCount int, mapItem map[string]any) map[string]any {
	ctxObj := make(map[string]any, len(in.baseCtx)+2)
	for key, value := range in.baseCtx {
		ctxObj[key] = value
	}
	ctxObj["State"] = map[string]any{
		"Name":        name,
		"EnteredTime": entered.UTC().Format(time.RFC3339Nano),
		"RetryCount":  float64(retryCount),
	}
	if mapItem != nil {
		ctxObj["Map"] = map[string]any{"Item": mapItem}
	}
	return ctxObj
}

// ─── Branch execution ─────────────────────────────────────────────────────────

// runBranch interprets one state machine body from its StartAt state until a
// state ends the branch. It is used for the top-level definition, for every
// Parallel branch and for every Map iteration.
func (in *interpreter) runBranch(ctx context.Context, branch *aslBranch, input any) (any, *stateError) {
	if !isJSONPath(branch.QueryLanguage) {
		return nil, unsupportedError("the %s query language — only JSONPath state machines are interpreted", branch.QueryLanguage)
	}
	current := branch.StartAt
	data := input
	for {
		if ctx.Err() != nil {
			return nil, in.unwindReason("")
		}
		if in.hist.full() {
			return nil, newStateError(errRuntime, "the execution exceeded the maximum of %d history events", maxHistoryEvents)
		}
		state := branch.States[current]
		if state == nil {
			return nil, newStateError(errRuntime, "state %q is not defined", current)
		}
		output, next, done, serr := in.runState(ctx, current, state, data)
		if serr != nil {
			return nil, serr
		}
		data = output
		if done {
			return data, nil
		}
		current = next
	}
}

// unwindReason explains why the interpreter is unwinding after its context was
// cancelled: StopExecution asked for it, or the runaway guard fired. state
// names the state it was in, when known.
func (in *interpreter) unwindReason(state string) *stateError {
	if in.run != nil {
		if stopped, errName, cause := in.run.abortReason(); stopped {
			return &stateError{name: errName, cause: cause, aborted: true}
		}
	}
	where := ""
	if state != "" {
		where = fmt.Sprintf(" in state %q", state)
	}
	budget := newStateError(errTimeout,
		"the execution exceeded Overcast's execution budget%s — raise OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT (currently %s) if the workflow legitimately takes this long",
		where, in.handler.executionTimeout(nil))
	budget.budgetExpired = true
	return budget
}

// runState interprets one state and reports the next transition. done means
// the branch ends here (End: true, or a Succeed state).
func (in *interpreter) runState(ctx context.Context, name string, state *aslState, raw any) (any, string, bool, *stateError) {
	if serr := unsupportedStateFields(name, state); serr != nil {
		return nil, "", false, serr
	}
	entered := in.handler.clk.Now()
	ctxObj := in.stateContext(name, entered, 0, in.mapItemFromContext())

	effective, serr := applyInputPath(state, raw, ctxObj)
	if serr != nil {
		return nil, "", false, serr
	}

	enteredJSON, encErr := encodeJSON(effective)
	if encErr != nil {
		return nil, "", false, newStateError(errRuntime, "%s", encErr.Error())
	}
	in.hist.add(entered, HistoryEvent{
		Type: stateEnteredEventType(state.Type),
		StateEntered: &stateEnteredDetails{
			Name:         name,
			Input:        enteredJSON,
			InputDetails: &executionDataDetails{},
		},
	})

	switch state.Type {
	case stateTypeFail:
		return nil, "", false, in.runFail(state, effective, ctxObj)
	case stateTypeSucceed:
		output, serr := applyOutputPath(state, effective, ctxObj)
		if serr != nil {
			return nil, "", false, serr
		}
		in.recordExit(name, state.Type, output)
		return output, "", true, nil
	case stateTypeChoice:
		return in.runChoice(name, state, effective, ctxObj)
	case stateTypeWait:
		return in.runWait(ctx, name, state, effective, ctxObj)
	case stateTypePass:
		return in.runPass(name, state, raw, effective, ctxObj)
	case stateTypeTask, stateTypeParallel, stateTypeMap:
		return in.runRetryable(ctx, name, state, raw, effective)
	}
	// parseDefinition rejects unknown state types, so this is unreachable in
	// practice; keeping it loud rather than falling through is the point.
	return nil, "", false, unsupportedError("state type %q", state.Type)
}

// recordExit emits the `<Type>StateExited` event for a state.
func (in *interpreter) recordExit(name, stateType string, output any) {
	encoded, err := encodeJSON(output)
	if err != nil {
		encoded = ""
	}
	in.hist.add(in.handler.clk.Now(), HistoryEvent{
		Type: stateExitedEventType(stateType),
		StateExited: &stateExitedDetails{
			Name:          name,
			Output:        encoded,
			OutputDetails: &executionDataDetails{},
		},
	})
}

// mapItemFromContext returns the Map.Item entry already installed on the base
// context, so nested states inside a Map iteration keep seeing it.
func (in *interpreter) mapItemFromContext() map[string]any {
	mapCtx, ok := in.baseCtx["Map"].(map[string]any)
	if !ok {
		return nil
	}
	item, _ := mapCtx["Item"].(map[string]any)
	return item
}

// ─── Input / output processing ────────────────────────────────────────────────

// applyInputPath narrows the raw state input. An absent InputPath means `$`;
// an explicit null means the effective input is an empty object.
func applyInputPath(state *aslState, raw any, ctxObj map[string]any) (any, *stateError) {
	if !state.InputPath.Set || state.InputPath.Value == "$" {
		return raw, nil
	}
	if state.InputPath.Null {
		return map[string]any{}, nil
	}
	value, err := selectPath(raw, ctxObj, state.InputPath.Value)
	if err != nil {
		return nil, newStateError(errRuntime, "%s", err.Error())
	}
	return value, nil
}

// applyOutputPath narrows a state's output. An absent OutputPath means `$`; an
// explicit null means an empty object.
func applyOutputPath(state *aslState, value any, ctxObj map[string]any) (any, *stateError) {
	if !state.OutputPath.Set || state.OutputPath.Value == "$" {
		return value, nil
	}
	if state.OutputPath.Null {
		return map[string]any{}, nil
	}
	selected, err := selectPath(value, ctxObj, state.OutputPath.Value)
	if err != nil {
		return nil, newStateError(errRuntime, "%s", err.Error())
	}
	return selected, nil
}

// applyResult runs the ResultSelector → ResultPath → OutputPath pipeline that
// turns a state's result into its output.
func (in *interpreter) applyResult(state *aslState, raw, result any, ctxObj map[string]any) (any, *stateError) {
	if len(state.ResultSelector) > 0 {
		selected, err := renderPayloadTemplate(state.ResultSelector, result, ctxObj)
		if err != nil {
			return nil, newStateError(errParameterPathFailure, "%s", err.Error())
		}
		result = selected
	}
	combined, serr := applyResultPath(state.ResultPath, raw, result)
	if serr != nil {
		return nil, serr
	}
	return applyOutputPath(state, combined, ctxObj)
}

// applyResultPath places a result into the raw state input. An absent
// ResultPath means `$` (the result replaces the input); an explicit null
// discards the result and passes the input through unchanged.
func applyResultPath(resultPath aslPath, raw, result any) (any, *stateError) {
	switch {
	case !resultPath.Set, resultPath.Value == "$":
		return result, nil
	case resultPath.Null:
		return raw, nil
	}
	combined, err := insertPath(raw, resultPath.Value, result)
	if err != nil {
		return nil, newStateError(errResultPathMatchFailure, "%s", err.Error())
	}
	return combined, nil
}

// ─── JSON helpers ─────────────────────────────────────────────────────────────

// decodeExecutionInput parses an execution's input document. An empty input is
// the empty object, matching AWS's default.
func decodeExecutionInput(input string) (any, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		return nil, fmt.Errorf("the execution input is not valid JSON: %w", err)
	}
	return decoded, nil
}

func encodeJSON(v any) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("state output could not be encoded as JSON: %w", err)
	}
	return string(encoded), nil
}

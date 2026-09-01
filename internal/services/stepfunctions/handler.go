package stepfunctions

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// Handler holds Step Functions handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *Store
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	bus   *events.Bus
	// router is Overcast's own root handler. Task states dispatch through it
	// so a workflow step runs exactly the handler an SDK call would, rather
	// than a second, divergent code path. Nil until InitRouter is called, and
	// a Task state then fails loudly rather than pretending to succeed.
	router  http.Handler
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation

	// Executions run on their own goroutines so StartExecution can accept and
	// return while the execution is still RUNNING, as AWS does. runs tracks
	// the live ones (for DescribeExecution, GetExecutionHistory and
	// StopExecution), wg lets shutdown drain them, and shutdownCancel unwinds
	// an execution parked in a Wait so it cannot hold shutdown open.
	//
	// stopping is guarded by runsMu, the same lock reserveRun (runs.go) takes
	// to register a run and Add to wg. Stop sets it inside that critical
	// section before it ever calls wg.Wait, so a StartExecution/
	// StartSyncExecution/nested-startExecution arriving concurrently either
	// completes its reserveRun call — Add and all — strictly before Stop's
	// critical section runs, or observes stopping and refuses before
	// touching wg at all. Either way, wg.Add can never race wg.Wait (issue
	// #1315, the same shape #1290 fixed in lifecycle.Scheduler and #1298
	// fixed in EKS).
	runs           map[string]*executionRun
	runsMu         sync.Mutex
	stopping       bool
	wg             sync.WaitGroup
	shutdown       context.Context
	shutdownCancel context.CancelFunc
}

func newHandler(cfg *config.Config, store *Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log, clk: clk, runs: map[string]*executionRun{}}
	// Parent of every execution context. Creating it does no I/O, so this is
	// safe in a constructor called from router.New.
	h.shutdown, h.shutdownCancel = context.WithCancel(context.Background())
	h.initOps()
	return h
}

// initOps registers every known StepFunctions operation to its handler.
// Adding a new operation: add an entry here, implement in handler.go.
func (h *Handler) initOps() {
	h.typedOp = h.typedOps()
	h.ops = map[string]http.HandlerFunc{
		"CreateStateMachine":   h.CreateStateMachine,
		"DescribeStateMachine": h.DescribeStateMachine,
		"ListStateMachines":    h.ListStateMachines,
		"StartExecution":       h.StartExecution,
		"DeleteStateMachine":   h.DeleteStateMachine,
		"UpdateStateMachine":   h.typedJSONHandler("UpdateStateMachine"),
		"TagResource":          h.TagResource,
		"UntagResource":        h.UntagResource,
		"ListTagsForResource":  h.ListTagsForResource,
		// The execution plane has a single, typed implementation. These entries
		// route the legacy X-Amz-Target path to it rather than duplicating the
		// logic in a second handler that could drift.
		"StartSyncExecution":               h.typedJSONHandler("StartSyncExecution"),
		"DescribeExecution":                h.typedJSONHandler("DescribeExecution"),
		"GetExecutionHistory":              h.typedJSONHandler("GetExecutionHistory"),
		"ListExecutions":                   h.typedJSONHandler("ListExecutions"),
		"StopExecution":                    h.typedJSONHandler("StopExecution"),
		"DescribeStateMachineForExecution": h.typedJSONHandler("DescribeStateMachineForExecution"),
	}
}

// typedJSONHandler adapts a typed operation to the legacy http.HandlerFunc
// dispatch table, so a caller arriving on the X-Amz-Target path reaches exactly
// the implementation the codec path uses.
func (h *Handler) typedJSONHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		operation, ok := h.typedOp[name]
		if !ok {
			protocol.NotImplementedJSON(w, r)
			return
		}
		// Step Functions' own wire protocol is AWS JSON 1.0; prefer whatever
		// the router already resolved for this request.
		wire := codec.JSON10
		if resolved, resolvedOp := codec.FromContext(r.Context()); resolved != nil && resolvedOp == name {
			wire = resolved
		}
		operation.Invoke(w, r, wire)
	}
}

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

func (h *Handler) CreateStateMachine(w http.ResponseWriter, r *http.Request) {
	// Delegates to createStateMachineTyped (typed_logic.go) so the legacy
	// JSON1.0/1.1 path and the CBOR typed path share one implementation —
	// the legacy copy previously re-implemented this inline and silently
	// ignored tags (#1196).
	var req createStateMachineRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.createStateMachineTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"stateMachineArn": resp.StateMachineArn,
		"creationDate":    resp.CreationDate,
	})
}

// ── DescribeStateMachine ──────────────────────────────────────────────────────

func (h *Handler) DescribeStateMachine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	name := extractSMName(req.StateMachineArn)
	sm, err := h.store.GetStateMachine(r.Context(), name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if sm == nil {
		protocol.WriteJSONError(w, r, errSMNotFound(req.StateMachineArn))
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"stateMachineArn":      sm.ARN,
		"name":                 sm.Name,
		"definition":           sm.Definition,
		"roleArn":              sm.RoleArn,
		"type":                 sm.Type,
		"status":               sm.Status,
		"creationDate":         float64(sm.CreatedAt.UnixMilli()) / 1000.0,
		"loggingConfiguration": sfnLoggingConfigOrDefault(sm.LoggingConfiguration),
		"tracingConfiguration": sfnTracingConfigOrDefault(sm.TracingConfiguration),
	})
}

// sfnLoggingConfigOrDefault and sfnTracingConfigOrDefault return AWS's
// documented defaults — logging OFF, tracing disabled — when the state
// machine did not configure either, so DescribeStateMachine always echoes
// the shape real AWS returns instead of omitting the field.
func sfnLoggingConfigOrDefault(v map[string]any) map[string]any {
	if v != nil {
		return v
	}
	return map[string]any{
		"level":                "OFF",
		"includeExecutionData": false,
		"destinations":         []any{},
	}
}

func sfnTracingConfigOrDefault(v map[string]any) map[string]any {
	if v != nil {
		return v
	}
	return map[string]any{"enabled": false}
}

// ── ListStateMachines ─────────────────────────────────────────────────────────

func (h *Handler) ListStateMachines(w http.ResponseWriter, r *http.Request) {
	sms, err := h.store.ListStateMachines(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	items := make([]map[string]any, 0, len(sms))
	for _, sm := range sms {
		items = append(items, map[string]any{
			"stateMachineArn": sm.ARN,
			"name":            sm.Name,
			"type":            sm.Type,
			"creationDate":    float64(sm.CreatedAt.UnixMilli()) / 1000.0,
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"stateMachines": items,
	})
}

// ── StartExecution ────────────────────────────────────────────────────────────

func (h *Handler) StartExecution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StateMachineArn string `json:"stateMachineArn"`
		Input           string `json:"input"`
		Name            string `json:"name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	exec, aerr := h.startExecution(r.Context(), req.StateMachineArn, req.Name, req.Input, 0, executionAsync)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"executionArn": exec.ExecutionArn,
		"startDate":    epochSeconds(exec.StartDate),
	})
}

// ── DeleteStateMachine ────────────────────────────────────────────────────────

func (h *Handler) DeleteStateMachine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StateMachineArn string `json:"stateMachineArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	name := extractSMName(req.StateMachineArn)
	if err := h.store.DeleteStateMachine(r.Context(), name); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	h.publish(r, events.SFNStateMachineDeleted, events.ResourcePayload{Name: name})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// extractSMName extracts the state machine name from an ARN.
// ARN format: arn:aws:states:region:account:stateMachine:name.
func extractSMName(arn string) string {
	// Split: [arn, aws, states, region, account, stateMachine, name...]
	parts := strings.SplitN(arn, ":", 7)
	if len(parts) == 7 {
		return parts[6]
	}
	// Fallback: return the last segment
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

// validateDefinitionForCreate rejects a definition that is not valid Amazon
// States Language, the way AWS's CreateStateMachine does. Definitions that are
// valid ASL but use features Overcast cannot interpret are accepted here — so
// CDK and CloudFormation deploys keep working — and fail the execution loudly
// when they actually run.
func validateDefinitionForCreate(definition string) *protocol.AWSError {
	if _, err := parseDefinition(definition); err != nil {
		return &protocol.AWSError{
			Code:       "InvalidDefinition",
			Message:    fmt.Sprintf("Invalid State Machine Definition: '%s'", err.Error()),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

func errSMNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "StateMachineDoesNotExist",
		Message:    fmt.Sprintf("State Machine Does Not Exist: '%s'", arn),
		HTTPStatus: http.StatusBadRequest,
	}
}

// ── Tags ──────────────────────────────────────────────────────────────────────

var sfnTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "TooManyTags",
	InvalidCode:     "InvalidParameter",
	ExceededMessage: "Tag count exceeded the maximum of 50 tags per resource.",
}

type tagResourceRequest struct {
	ResourceArn string `json:"resourceArn"`
	Tags        []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"tags"`
}

type untagResourceRequest struct {
	ResourceArn string   `json:"resourceArn"`
	TagKeys     []string `json:"tagKeys"`
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"resourceArn"`
}

func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	var req tagResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	name := extractSMName(req.ResourceArn)

	incoming := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		incoming[t.Key] = t.Value
	}

	if aerr := serviceutil.ApplyInlineTags(r.Context(), name, incoming, sfnTagCfg,
		func(ctx context.Context, name string) (*StateMachine, *protocol.AWSError) {
			sm, err := h.store.GetStateMachine(ctx, name)
			if err != nil {
				return nil, protocol.Wrap(protocol.ErrInternalError, err)
			}
			if sm == nil {
				return nil, errSMNotFound(req.ResourceArn)
			}
			return sm, nil
		},
		func(ctx context.Context, sm *StateMachine) *protocol.AWSError {
			if err := h.store.PutStateMachine(ctx, sm); err != nil {
				return protocol.Wrap(protocol.ErrInternalError, err)
			}
			return nil
		},
	); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	var req untagResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	name := extractSMName(req.ResourceArn)

	if aerr := serviceutil.RemoveInlineTags(r.Context(), name, req.TagKeys,
		func(ctx context.Context, name string) (*StateMachine, *protocol.AWSError) {
			sm, err := h.store.GetStateMachine(ctx, name)
			if err != nil {
				return nil, protocol.Wrap(protocol.ErrInternalError, err)
			}
			if sm == nil {
				return nil, errSMNotFound(req.ResourceArn)
			}
			return sm, nil
		},
		func(ctx context.Context, sm *StateMachine) *protocol.AWSError {
			if err := h.store.PutStateMachine(ctx, sm); err != nil {
				return protocol.Wrap(protocol.ErrInternalError, err)
			}
			return nil
		},
	); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req listTagsForResourceRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	name := extractSMName(req.ResourceArn)

	tags, aerr := serviceutil.ListInlineTags(r.Context(), name,
		func(ctx context.Context, name string) (*StateMachine, *protocol.AWSError) {
			sm, err := h.store.GetStateMachine(ctx, name)
			if err != nil {
				return nil, protocol.Wrap(protocol.ErrInternalError, err)
			}
			if sm == nil {
				return nil, errSMNotFound(req.ResourceArn)
			}
			return sm, nil
		},
	)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	tagList := serviceutil.TagElements(tags, func(k, v string) map[string]string {
		return map[string]string{"key": k, "value": v}
	})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"tags": tagList})
}

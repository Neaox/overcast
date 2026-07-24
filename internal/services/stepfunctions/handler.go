package stepfunctions

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Handler holds Step Functions handler dependencies.
type Handler struct {
	cfg     *config.Config
	store   *Store
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	bus     *events.Bus
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

func newHandler(cfg *config.Config, store *Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log, clk: clk}
	h.initOps()
	return h
}

// initOps registers every known StepFunctions operation to its handler.
// Adding a new operation: add an entry here, implement in handler.go.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"CreateStateMachine":   h.CreateStateMachine,
		"DescribeStateMachine": h.DescribeStateMachine,
		"ListStateMachines":    h.ListStateMachines,
		"StartExecution":       h.StartExecution,
		"DeleteStateMachine":   h.DeleteStateMachine,
	}
	h.typedOp = h.typedOps()
}

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

func (h *Handler) CreateStateMachine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Definition string `json:"definition"`
		RoleArn    string `json:"roleArn"`
		Type       string `json:"type"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidName",
			Message:    "Value null at 'name' failed to satisfy constraint",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Check for duplicate: AWS CreateStateMachine is idempotent when the name,
	// definition, roleArn, and type all match the existing state machine.
	existing, err := h.store.GetStateMachine(r.Context(), req.Name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing != nil {
		smType := req.Type
		if smType == "" {
			smType = "STANDARD"
		}
		if existing.Definition == req.Definition && existing.RoleArn == req.RoleArn && existing.Type == smType {
			// Idempotent create — return existing ARN.
			protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
				"stateMachineArn": existing.ARN,
				"creationDate":    float64(existing.CreatedAt.UnixMilli()) / 1000.0,
			})
			return
		}
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "StateMachineAlreadyExists",
			Message:    fmt.Sprintf("State Machine Already Exists: '%s'", req.Name),
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	smType := req.Type
	if smType == "" {
		smType = "STANDARD"
	}

	now := h.clk.Now()
	arn := protocol.ARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, "states", "stateMachine:"+req.Name)
	sm := &StateMachine{
		Name:       req.Name,
		ARN:        arn,
		Definition: req.Definition,
		RoleArn:    req.RoleArn,
		Type:       smType,
		Status:     "ACTIVE",
		CreatedAt:  now,
	}

	if err := h.store.PutStateMachine(r.Context(), sm); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	h.publish(r, events.SFNStateMachineCreated, events.ResourcePayload{Name: req.Name})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"stateMachineArn": arn,
		"creationDate":    float64(now.UnixMilli()) / 1000.0,
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
		"stateMachineArn": sm.ARN,
		"name":            sm.Name,
		"definition":      sm.Definition,
		"roleArn":         sm.RoleArn,
		"type":            sm.Type,
		"status":          sm.Status,
		"creationDate":    float64(sm.CreatedAt.UnixMilli()) / 1000.0,
	})
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

	smName := extractSMName(req.StateMachineArn)
	sm, err := h.store.GetStateMachine(r.Context(), smName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if sm == nil {
		protocol.WriteJSONError(w, r, errSMNotFound(req.StateMachineArn))
		return
	}

	execName := req.Name
	if execName == "" {
		execName = uuid.NewString()
	}

	now := h.clk.Now()
	execArn := protocol.ARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, "states", "execution:"+smName+":"+execName)
	exec := &Execution{
		ExecutionArn:    execArn,
		StateMachineArn: req.StateMachineArn,
		Name:            execName,
		Input:           req.Input,
		Status:          "SUCCEEDED",
		StartDate:       now,
	}

	if err := h.store.PutExecution(r.Context(), exec); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	h.publish(r, events.SFNExecutionStarted, events.ResourcePayload{Name: execName})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"executionArn": execArn,
		"startDate":    float64(now.UnixMilli()) / 1000.0,
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

func errSMNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "StateMachineDoesNotExist",
		Message:    fmt.Sprintf("State Machine Does Not Exist: '%s'", arn),
		HTTPStatus: http.StatusBadRequest,
	}
}

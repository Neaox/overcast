package stepfunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	storeNS    = "stepfunctions"
	smPrefix   = "sm:"
	execPrefix = "exec:"
	histPrefix = "hist:"
)

// StateMachine represents a Step Functions state machine.
type StateMachine struct {
	Name       string            `json:"Name"`
	ARN        string            `json:"ARN"`
	Definition string            `json:"Definition"`
	RoleArn    string            `json:"RoleArn"`
	Type       string            `json:"Type"` // "STANDARD" or "EXPRESS"
	Status     string            `json:"Status"`
	CreatedAt  time.Time         `json:"CreatedAt"`
	Tags       map[string]string `json:"Tags,omitempty"`
}

// Execution represents a Step Functions execution.
type Execution struct {
	ExecutionArn    string     `json:"ExecutionArn"`
	StateMachineArn string     `json:"StateMachineArn"`
	Name            string     `json:"Name"`
	Input           string     `json:"Input"`
	Output          string     `json:"Output,omitempty"`
	Status          string     `json:"Status"`
	StartDate       time.Time  `json:"StartDate"`
	StopDate        *time.Time `json:"StopDate,omitempty"`
	// Error and Cause carry the AWS-shaped failure reason for a FAILED,
	// TIMED_OUT or ABORTED execution — including the loud "not supported"
	// failures Overcast raises for ASL features it does not interpret.
	Error string `json:"Error,omitempty"`
	Cause string `json:"Cause,omitempty"`
}

// Store wraps state.Store with Step Functions-specific helpers.
type Store struct {
	s             state.Store
	defaultRegion string
}

func newStore(s state.Store, defaultRegion string) *Store {
	return &Store{s: s, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (st *Store) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, st.defaultRegion)
}

// GetStateMachine retrieves a state machine by name. Returns nil, nil if not found.
func (st *Store) GetStateMachine(ctx context.Context, name string) (*StateMachine, error) {
	raw, found, err := st.s.Get(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), smPrefix+name))
	if err != nil {
		return nil, fmt.Errorf("stepfunctions: get sm %q: %w", name, err)
	}
	if !found {
		return nil, nil
	}
	var sm StateMachine
	if err := json.Unmarshal([]byte(raw), &sm); err != nil {
		return nil, fmt.Errorf("stepfunctions: unmarshal sm %q: %w", name, err)
	}
	return &sm, nil
}

// PutStateMachine saves a state machine record.
func (st *Store) PutStateMachine(ctx context.Context, sm *StateMachine) error {
	raw, err := json.Marshal(sm)
	if err != nil {
		return fmt.Errorf("stepfunctions: marshal sm %q: %w", sm.Name, err)
	}
	return st.s.Set(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), smPrefix+sm.Name), string(raw))
}

// DeleteStateMachine removes a state machine by name.
func (st *Store) DeleteStateMachine(ctx context.Context, name string) error {
	return st.s.Delete(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), smPrefix+name))
}

// ListStateMachines returns all state machines.
func (st *Store) ListStateMachines(ctx context.Context) ([]*StateMachine, error) {
	pairs, err := st.s.Scan(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), smPrefix))
	if err != nil {
		return nil, fmt.Errorf("stepfunctions: scan sms: %w", err)
	}
	sms := make([]*StateMachine, 0, len(pairs))
	for _, p := range pairs {
		var sm StateMachine
		if err := json.Unmarshal([]byte(p.Value), &sm); err != nil {
			return nil, fmt.Errorf("stepfunctions: unmarshal sm: %w", err)
		}
		sms = append(sms, &sm)
	}
	return sms, nil
}

// PutExecution saves an execution record.
func (st *Store) PutExecution(ctx context.Context, exec *Execution) error {
	raw, err := json.Marshal(exec)
	if err != nil {
		return fmt.Errorf("stepfunctions: marshal exec %q: %w", exec.ExecutionArn, err)
	}
	return st.s.Set(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), execPrefix+exec.ExecutionArn), string(raw))
}

// GetExecution retrieves one execution by ARN. Returns nil, nil if not found.
func (st *Store) GetExecution(ctx context.Context, arn string) (*Execution, error) {
	raw, found, err := st.s.Get(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), execPrefix+arn))
	if err != nil {
		return nil, fmt.Errorf("stepfunctions: get exec %q: %w", arn, err)
	}
	if !found {
		return nil, nil
	}
	var exec Execution
	if err := json.Unmarshal([]byte(raw), &exec); err != nil {
		return nil, fmt.Errorf("stepfunctions: unmarshal exec %q: %w", arn, err)
	}
	return &exec, nil
}

// ListExecutions returns every execution of one state machine, newest first.
//
// Execution ARNs embed the state machine name, so the store key prefix scopes
// the scan to that machine rather than walking every execution in the region.
// A record that cannot be decoded is skipped rather than failing the whole
// list — one corrupt row must not take out the page.
func (st *Store) ListExecutions(ctx context.Context, smARN string) ([]*Execution, error) {
	prefix := execPrefix + executionARNPrefix(smARN)
	pairs, err := st.s.Scan(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), prefix))
	if err != nil {
		return nil, fmt.Errorf("stepfunctions: scan executions: %w", err)
	}
	execs := make([]*Execution, 0, len(pairs))
	for _, p := range pairs {
		var exec Execution
		if json.Unmarshal([]byte(p.Value), &exec) != nil {
			continue
		}
		execs = append(execs, &exec)
	}
	sort.SliceStable(execs, func(i, j int) bool { return execs[i].StartDate.After(execs[j].StartDate) })
	return execs, nil
}

// PutHistory saves an execution's history in one write. The interpreter
// accumulates events in memory and calls this once, so a long execution costs
// a single store write rather than one per state transition.
func (st *Store) PutHistory(ctx context.Context, execARN string, events []HistoryEvent) error {
	raw, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("stepfunctions: marshal history %q: %w", execARN, err)
	}
	return st.s.Set(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), histPrefix+execARN), string(raw))
}

// GetHistory returns an execution's recorded history events, oldest first.
func (st *Store) GetHistory(ctx context.Context, execARN string) ([]HistoryEvent, error) {
	raw, found, err := st.s.Get(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), histPrefix+execARN))
	if err != nil {
		return nil, fmt.Errorf("stepfunctions: get history %q: %w", execARN, err)
	}
	if !found {
		return nil, nil
	}
	var events []HistoryEvent
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		return nil, fmt.Errorf("stepfunctions: unmarshal history %q: %w", execARN, err)
	}
	return events, nil
}

// executionARNPrefix turns a state machine ARN into the prefix every one of
// its execution ARNs starts with:
//
//	arn:aws:states:R:A:stateMachine:Name → arn:aws:states:R:A:execution:Name:
func executionARNPrefix(smARN string) string {
	name := extractSMName(smARN)
	parts := strings.SplitN(smARN, ":", 7)
	if len(parts) < 6 {
		return "arn:aws:states:::execution:" + name + ":"
	}
	return strings.Join(parts[:5], ":") + ":execution:" + name + ":"
}

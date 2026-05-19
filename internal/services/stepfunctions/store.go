package stepfunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	storeNS    = "stepfunctions"
	smPrefix   = "sm:"
	execPrefix = "exec:"
)

// StateMachine represents a Step Functions state machine.
type StateMachine struct {
	Name       string    `json:"Name"`
	ARN        string    `json:"ARN"`
	Definition string    `json:"Definition"`
	RoleArn    string    `json:"RoleArn"`
	Type       string    `json:"Type"` // "STANDARD" or "EXPRESS"
	Status     string    `json:"Status"`
	CreatedAt  time.Time `json:"CreatedAt"`
}

// Execution represents a Step Functions execution.
type Execution struct {
	ExecutionArn    string    `json:"ExecutionArn"`
	StateMachineArn string    `json:"StateMachineArn"`
	Name            string    `json:"Name"`
	Input           string    `json:"Input"`
	Status          string    `json:"Status"`
	StartDate       time.Time `json:"StartDate"`
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

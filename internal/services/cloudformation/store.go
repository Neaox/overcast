package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsStacks     = "cfn:stacks"
	nsChangeSets = "cfn:changesets"
	nsEvents     = "cfn:events"
)

// cfnStore wraps state.Store with typed CloudFormation access.
type cfnStore struct {
	s             state.Store
	defaultRegion string
}

func newCFNStore(s state.Store, defaultRegion string) *cfnStore {
	return &cfnStore{s: s, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (st *cfnStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, st.defaultRegion)
}

// ── Stack CRUD ─────────────────────────────────────────────────────────────

func (st *cfnStore) putStack(ctx context.Context, stack *Stack) error {
	data, err := json.Marshal(stack)
	if err != nil {
		return fmt.Errorf("cfn: marshal stack: %w", err)
	}
	return st.s.Set(ctx, nsStacks, serviceutil.RegionKey(st.region(ctx), stack.StackName), string(data))
}

func (st *cfnStore) getStack(ctx context.Context, name string) (*Stack, *protocol.AWSError) {
	raw, found, err := st.s.Get(ctx, nsStacks, serviceutil.RegionKey(st.region(ctx), name))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, nil
	}
	var stack Stack
	if err := json.Unmarshal([]byte(raw), &stack); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &stack, nil
}

func (st *cfnStore) listStacks(ctx context.Context) ([]*Stack, *protocol.AWSError) {
	items, err := st.s.Scan(ctx, nsStacks, serviceutil.RegionKey(st.region(ctx), ""))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	stacks := make([]*Stack, 0, len(items))
	for _, kv := range items {
		var stack Stack
		if err := json.Unmarshal([]byte(kv.Value), &stack); err != nil {
			continue
		}
		stacks = append(stacks, &stack)
	}
	return stacks, nil
}

// ── Stack events ───────────────────────────────────────────────────────────

// getStackEvents returns all events recorded for the named stack, in the order
// they were appended (oldest first). Returns nil, nil when no events exist yet.
func (st *cfnStore) getStackEvents(ctx context.Context, name string) ([]StackEvent, error) {
	raw, found, err := st.s.Get(ctx, nsEvents, serviceutil.RegionKey(st.region(ctx), name))
	if err != nil {
		return nil, fmt.Errorf("cfn: get events: %w", err)
	}
	if !found {
		return nil, nil
	}
	var evts []StackEvent
	if err := json.Unmarshal([]byte(raw), &evts); err != nil {
		return nil, fmt.Errorf("cfn: unmarshal events: %w", err)
	}
	return evts, nil
}

// appendStackEvent appends a single event to the stack's event history.
// Each append is a read-modify-write. Callers must ensure only one goroutine
// appends events for a given stack at a time (the provisioner guarantees this).
func (st *cfnStore) appendStackEvent(ctx context.Context, name string, event StackEvent) error {
	key := serviceutil.RegionKey(st.region(ctx), name)
	raw, found, err := st.s.Get(ctx, nsEvents, key)
	if err != nil {
		return fmt.Errorf("cfn: read events for append: %w", err)
	}
	var evts []StackEvent
	if found {
		if err := json.Unmarshal([]byte(raw), &evts); err != nil {
			return fmt.Errorf("cfn: unmarshal events for append: %w", err)
		}
	}
	evts = append(evts, event)
	data, err := json.Marshal(evts)
	if err != nil {
		return fmt.Errorf("cfn: marshal events: %w", err)
	}
	return st.s.Set(ctx, nsEvents, key, string(data))
}

// ── ChangeSet CRUD ─────────────────────────────────────────────────────────

// changeSetKey combines stack name and change set name for unique storage.
func changeSetKey(stackName, csName string) string {
	return stackName + "/" + csName
}

func isARN(value string) bool {
	return strings.HasPrefix(value, "arn:")
}

func changeSetMatchesStack(cs *ChangeSet, stackNameOrID string) bool {
	return stackNameOrID == "" || cs.StackName == stackNameOrID || cs.StackID == stackNameOrID
}

func (st *cfnStore) putChangeSet(ctx context.Context, cs *ChangeSet) error {
	data, err := json.Marshal(cs)
	if err != nil {
		return fmt.Errorf("cfn: marshal changeset: %w", err)
	}
	return st.s.Set(ctx, nsChangeSets, serviceutil.RegionKey(st.region(ctx), changeSetKey(cs.StackName, cs.ChangeSetName)), string(data))
}

func (st *cfnStore) getChangeSet(ctx context.Context, stackName, csName string) (*ChangeSet, *protocol.AWSError) {
	if isARN(csName) {
		return st.getChangeSetByID(ctx, stackName, csName)
	}

	raw, found, err := st.s.Get(ctx, nsChangeSets, serviceutil.RegionKey(st.region(ctx), changeSetKey(stackName, csName)))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if found {
		var cs ChangeSet
		if err := json.Unmarshal([]byte(raw), &cs); err != nil {
			return nil, protocol.ErrInternalError
		}
		return &cs, nil
	}

	if !isARN(stackName) {
		return nil, nil
	}
	return st.getChangeSetByName(ctx, stackName, csName)
}

func (st *cfnStore) getChangeSetByID(ctx context.Context, stackName, changeSetID string) (*ChangeSet, *protocol.AWSError) {
	items, err := st.s.Scan(ctx, nsChangeSets, serviceutil.RegionKey(st.region(ctx), ""))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	for _, kv := range items {
		var cs ChangeSet
		if err := json.Unmarshal([]byte(kv.Value), &cs); err != nil {
			continue
		}
		if cs.ChangeSetID == changeSetID && changeSetMatchesStack(&cs, stackName) {
			return &cs, nil
		}
	}
	return nil, nil
}

func (st *cfnStore) getChangeSetByName(ctx context.Context, stackNameOrID, changeSetName string) (*ChangeSet, *protocol.AWSError) {
	items, err := st.s.Scan(ctx, nsChangeSets, serviceutil.RegionKey(st.region(ctx), ""))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	for _, kv := range items {
		var cs ChangeSet
		if err := json.Unmarshal([]byte(kv.Value), &cs); err != nil {
			continue
		}
		if cs.ChangeSetName == changeSetName && changeSetMatchesStack(&cs, stackNameOrID) {
			return &cs, nil
		}
	}
	return nil, nil
}

func (st *cfnStore) deleteChangeSet(ctx context.Context, stackName, csName string) error {
	return st.s.Delete(ctx, nsChangeSets, serviceutil.RegionKey(st.region(ctx), changeSetKey(stackName, csName)))
}

func (st *cfnStore) listChangeSetsForStack(ctx context.Context, stackName string) ([]*ChangeSet, *protocol.AWSError) {
	items, err := st.s.Scan(ctx, nsChangeSets, serviceutil.RegionKey(st.region(ctx), ""))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	var result []*ChangeSet
	for _, kv := range items {
		var cs ChangeSet
		if err := json.Unmarshal([]byte(kv.Value), &cs); err != nil {
			continue
		}
		if changeSetMatchesStack(&cs, stackName) {
			result = append(result, &cs)
		}
	}
	return result, nil
}

// ── Exports ────────────────────────────────────────────────────────────────

// Export represents a stack output that is exported for cross-stack references.
type Export struct {
	ExportingStackId string
	Name             string
	Value            string
}

// listExports returns all exports from active (non-deleted) stacks in the
// current region. It scans all stacks and collects outputs with ExportName set.
func (st *cfnStore) listExports(ctx context.Context) ([]Export, *protocol.AWSError) {
	stacks, aerr := st.listStacks(ctx)
	if aerr != nil {
		return nil, aerr
	}
	var exports []Export
	for _, s := range stacks {
		if s.Status == StatusDeleteComplete || s.Status == StatusDeleteInProgress {
			continue
		}
		for _, o := range s.Outputs {
			if o.ExportName != "" {
				exports = append(exports, Export{
					ExportingStackId: s.StackID,
					Name:             o.ExportName,
					Value:            o.Value,
				})
			}
		}
	}
	return exports, nil
}

// listImportingStacks returns the names of stacks that import a given export name.
// It scans all stacks, parses their templates, and checks for Fn::ImportValue usage.
func (st *cfnStore) listImportingStacks(ctx context.Context, exportName string) ([]string, *protocol.AWSError) {
	stacks, aerr := st.listStacks(ctx)
	if aerr != nil {
		return nil, aerr
	}
	var importers []string
	for _, s := range stacks {
		if s.Status == StatusDeleteComplete || s.Status == StatusDeleteInProgress {
			continue
		}
		if s.TemplateBody != "" && templateImports(s.TemplateBody, exportName) {
			importers = append(importers, s.StackName)
		}
	}
	return importers, nil
}

// templateImports checks whether a template body references a given export name
// via Fn::ImportValue. Uses simple string matching for efficiency.
func templateImports(body, exportName string) bool {
	return strings.Contains(body, exportName) &&
		(strings.Contains(body, "Fn::ImportValue") || strings.Contains(body, "!ImportValue"))
}

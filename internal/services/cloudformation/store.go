package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Neaox/overcast/internal/clock"
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
	clk           clock.Clock
}

func newCFNStore(s state.Store, defaultRegion string, clk clock.Clock) *cfnStore {
	return &cfnStore{s: s, defaultRegion: defaultRegion, clk: clk}
}

func (st *cfnStore) flush(ctx context.Context) error {
	// Unwrap with the "cfn" namespace prefix (not the "cloudformation" service
	// name — NamespacedStore routes on the namespace prefix embedded in every
	// Get/Set/etc call, e.g. "cfn:stacks", "cfn:events"). Without unwrapping
	// first, state.Flush's direct Flushable type assertion silently no-ops
	// whenever any unrelated OVERCAST_STATE_<SVC> override wraps the store —
	// the same erasure class fixed elsewhere via state.Unwrap.
	return state.Flush(ctx, state.Unwrap(st.s, "cfn"))
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
//
// Each event is stored as its own row, keyed "<region>/<stackName>/<seq>"
// where <seq> is produced by uniqueSuffix — a nanosecond timestamp plus a
// monotonic counter, so keys sort in append order even when multiple events
// land in the same nanosecond (concurrent provisioner goroutines). This
// replaces the earlier single-JSON-array-per-stack layout, which required a
// full Get+unmarshal+append+marshal+Set read-modify-write on every append
// (quadratic over a deployment) and was not safe for concurrent appends to
// the same stack — a second writer's read-modify-write could race the
// first and silently drop its event. Per-row Sets have no such race: each
// append is a single independent write to a unique key.
//
// eventSeq is a process-wide counter (not per-stack) — uniqueness only
// requires that no two calls produce the same suffix, and a single global
// counter is simplest.
var eventSeq atomic.Int64

// uniqueSuffix returns a lexicographically sortable, monotonically
// increasing string combining the current nanosecond timestamp with a
// zero-padded process-wide counter, guaranteeing uniqueness even for events
// recorded within the same nanosecond. Mirrors the invocation-key pattern in
// internal/services/lambda/store.go.
func uniqueSuffix(clk clock.Clock) string {
	seq := eventSeq.Add(1)
	return strconv.FormatInt(clk.Now().UnixNano(), 10) + "-" + fmt.Sprintf("%010d", seq)
}

// stackEventsPrefix returns the Scan/DeletePrefix prefix covering every
// per-event row for a stack: "<region>/<stackName>/".
func stackEventsPrefix(region, name string) string {
	return serviceutil.RegionKey(region, name) + "/"
}

// getStackEvents returns all events recorded for the named stack, in the order
// they were appended (oldest first — callers that need AWS's newest-first
// DescribeStackEvents ordering reverse the slice themselves). Returns nil, nil
// when no events exist yet.
//
// Legacy compatibility: stacks whose events were recorded before the
// row-per-event migration have their full history under a single blob key
// "<region>/<stackName>" (no trailing segment) instead of per-event rows. If
// the prefix scan comes back empty, getStackEvents falls back to that blob
// key; when found, it decodes the events, opportunistically rewrites them as
// individual rows, and deletes the blob so subsequent reads take the fast
// per-row path. A blob that fails to decode is left in place (not deleted)
// and treated as "no events" rather than aborting the call — a single
// unreadable legacy record must not fail DescribeStackEvents.
func (st *cfnStore) getStackEvents(ctx context.Context, name string) ([]StackEvent, error) {
	region := st.region(ctx)
	prefix := stackEventsPrefix(region, name)
	items, err := st.s.Scan(ctx, nsEvents, prefix)
	if err != nil {
		return nil, fmt.Errorf("cfn: scan events: %w", err)
	}
	if len(items) > 0 {
		return decodeStackEventRows(items), nil
	}
	return st.getLegacyStackEvents(ctx, region, name)
}

// decodeStackEventRows decodes each row's value as a StackEvent, skipping
// (not erroring on) rows that fail to decode — a single corrupt persisted
// record must not fail the whole read. Rows are sorted by key, which sorts
// chronologically because keys embed uniqueSuffix's timestamp+counter.
func decodeStackEventRows(items []state.KV) []StackEvent {
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	evts := make([]StackEvent, 0, len(items))
	for _, kv := range items {
		var e StackEvent
		if err := json.Unmarshal([]byte(kv.Value), &e); err != nil {
			continue // skip corrupt entries
		}
		evts = append(evts, e)
	}
	return evts
}

// getLegacyStackEvents reads the pre-migration single-blob event history for
// a stack, if any, and opportunistically converts it to per-row storage.
func (st *cfnStore) getLegacyStackEvents(ctx context.Context, region, name string) ([]StackEvent, error) {
	blobKey := serviceutil.RegionKey(region, name)
	raw, found, err := st.s.Get(ctx, nsEvents, blobKey)
	if err != nil {
		return nil, fmt.Errorf("cfn: get legacy events: %w", err)
	}
	if !found {
		return nil, nil
	}
	var evts []StackEvent
	if err := json.Unmarshal([]byte(raw), &evts); err != nil {
		// Corrupt legacy blob: isolate the bad record rather than failing the
		// whole call, and leave it in place rather than deleting data we
		// couldn't read.
		return nil, nil
	}
	st.convertLegacyStackEvents(ctx, blobKey, evts)
	return evts, nil
}

// convertLegacyStackEvents writes each already-decoded legacy event as an
// individual row (preserving original order via successive uniqueSuffix
// calls) and removes the old blob key. This is a best-effort, one-time
// migration: on partial failure it leaves the blob in place so the next read
// retries the conversion, but still returns the events that were already
// decoded from it.
func (st *cfnStore) convertLegacyStackEvents(ctx context.Context, blobKey string, evts []StackEvent) {
	for _, e := range evts {
		data, err := json.Marshal(e)
		if err != nil {
			return
		}
		key := blobKey + "/" + uniqueSuffix(st.clk)
		if err := st.s.Set(ctx, nsEvents, key, string(data)); err != nil {
			return
		}
	}
	_ = st.s.Delete(ctx, nsEvents, blobKey)
}

// appendStackEvent appends a single event to the stack's event history as an
// independent row keyed by a unique, monotonically sortable suffix. Unlike
// the old blob layout, concurrent appends for the same stack are safe: each
// call is a single Set to a key no other call can produce.
func (st *cfnStore) appendStackEvent(ctx context.Context, name string, event StackEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("cfn: marshal event: %w", err)
	}
	key := stackEventsPrefix(st.region(ctx), name) + uniqueSuffix(st.clk)
	return st.s.Set(ctx, nsEvents, key, string(data))
}

// deleteStackEvents removes every recorded event for the named stack (and
// only that stack — the region+name prefix scopes the delete to it), plus
// the legacy blob key if one still exists.
func (st *cfnStore) deleteStackEvents(ctx context.Context, name string) error {
	region := st.region(ctx)
	prefix := stackEventsPrefix(region, name)
	if deleter, ok := st.s.(state.PrefixDeleter); ok {
		if err := deleter.DeletePrefix(ctx, nsEvents, prefix); err != nil {
			return fmt.Errorf("cfn: delete events: %w", err)
		}
	} else {
		keys, err := st.s.List(ctx, nsEvents, prefix)
		if err != nil {
			return fmt.Errorf("cfn: list events for delete: %w", err)
		}
		for _, key := range keys {
			if err := st.s.Delete(ctx, nsEvents, key); err != nil {
				return fmt.Errorf("cfn: delete event %q: %w", key, err)
			}
		}
	}
	// Delete is a no-op when the key doesn't exist, so this is safe to call
	// unconditionally even when the stack was never on the legacy blob layout.
	if err := st.s.Delete(ctx, nsEvents, serviceutil.RegionKey(region, name)); err != nil {
		return fmt.Errorf("cfn: delete legacy events blob: %w", err)
	}
	return nil
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

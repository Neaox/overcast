package cloudformation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// provisioner creates/updates/deletes resources asynchronously via the router.
// It dispatches internal HTTP requests to the emulator's own router so that
// each resource is created through its service's handler — no direct coupling.
type provisioner struct {
	cfg    *config.Config
	store  *cfnStore
	clk    clock.Clock
	log    *serviceutil.ServiceLogger
	bus    *events.Bus
	router http.Handler // the main emulator router

	mu     sync.Mutex
	wg     sync.WaitGroup
	cancel context.CancelFunc
	ctx    context.Context
}

type stackCompletionFunc func(ctx context.Context, stack *Stack)

func newProvisioner(cfg *config.Config, store *cfnStore, clk clock.Clock, log *serviceutil.ServiceLogger) *provisioner {
	ctx, cancel := context.WithCancel(context.Background())
	return &provisioner{
		cfg:    cfg,
		store:  store,
		clk:    clk,
		log:    log,
		ctx:    ctx,
		cancel: cancel,
	}
}

// initRouter sets the HTTP handler used for internal dispatch. Called after
// the router is fully constructed to avoid circular dependencies.
func (p *provisioner) initRouter(router http.Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.router = router
}

// initBus sets the event bus after construction.
func (p *provisioner) initBus(bus *events.Bus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bus = bus
}

// stop cancels all in-flight provisioning and waits for goroutines to drain.
// It honours the deadline on ctx so that a stuck goroutine cannot block
// shutdown indefinitely.
func (p *provisioner) stop(ctx context.Context) {
	p.cancel()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// regionCtx returns p.ctx enriched with the stack's region so that
// region-scoped store operations resolve the correct namespace.
func (p *provisioner) regionCtx(region string) context.Context {
	if region == "" {
		region = p.cfg.Region
	}
	return middleware.ContextWithRegion(p.ctx, region)
}

// ── Create stack (async) ───────────────────────────────────────────────────

// createStack provisions all resources in a template asynchronously, but waits
// briefly for fast stacks so SDK waiters can observe the terminal status on
// their immediate first DescribeStacks call.
func (p *provisioner) createStack(stack *Stack, tmpl *Template, onComplete stackCompletionFunc) {
	done := make(chan struct{})
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer close(done)
		p.provisionStackResources(stack, tmpl)
		if onComplete != nil {
			onComplete(p.regionCtx(stack.Region), stack)
		}
	}()
	p.awaitBriefly(done)
}

func (p *provisioner) awaitBriefly(done <-chan struct{}) {
	if p == nil || p.cfg == nil || p.cfg.CFNSyncWait <= 0 {
		return
	}
	budget := p.cfg.CFNSyncWait
	if budget <= 0 {
		return
	}
	select {
	case <-done:
	case <-p.clk.After(budget):
	}
}

func changeSetExecutionStatus(stackStatus string) string {
	switch stackStatus {
	case StatusCreateComplete, StatusUpdateComplete:
		return ExecStatusExecuteComplete
	case StatusCreateFailed, StatusUpdateFailed, StatusRollbackComplete, StatusRollbackFailed,
		StatusUpdateRollbackComplete, StatusUpdateRollbackFailed:
		return ExecStatusExecuteFailed
	default:
		return ExecStatusExecuteInProgress
	}
}

func (p *provisioner) completeChangeSet(cs *ChangeSet) stackCompletionFunc {
	if cs == nil {
		return nil
	}
	return func(ctx context.Context, stack *Stack) {
		status := changeSetExecutionStatus(stack.Status)
		if status == ExecStatusExecuteInProgress {
			return
		}
		cs.ExecutionStatus = status
		if err := p.store.putChangeSet(ctx, cs); err != nil {
			p.log.Warn("cfn: failed to persist changeset execution status",
				zap.String("changeSet", cs.ChangeSetName),
				zap.String("status", status),
				zap.Error(err))
		}
	}
}

// provisionStackResources is the synchronous core of stack provisioning.
// It builds the resolve context, provisions each resource in dependency order,
// resolves outputs, and sets the final stack status. Both top-level createStack
// (async) and nestedStackHandler (inline) use this method.
func (p *provisioner) provisionStackResources(stack *Stack, tmpl *Template) {
	ctx := p.regionCtx(stack.Region)

	rCtx := p.buildResolveContext(stack, tmpl)

	// Emit the initial stack CREATE_IN_PROGRESS event (the handler already
	// set this status; we record the event so DescribeStackEvents has history).
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusCreateInProgress, "User Initiated")

	// Determine resource ordering (respecting DependsOn).
	order, err := topoSort(tmpl.Resources)
	if err != nil {
		p.failStack(ctx, stack, StatusCreateFailed, fmt.Sprintf("dependency cycle: %v", err))
		return
	}

	// Provision each resource in order.
	stackStart := p.clk.Now()
	for _, logicalID := range order {
		if ctx.Err() != nil {
			p.failStack(ctx, stack, StatusCreateFailed, "cancelled")
			return
		}
		res := tmpl.Resources[logicalID]

		// Emit CREATE_IN_PROGRESS before attempting provisioning.
		p.recordEvent(ctx, stack, logicalID, "", res.Type, ResourceCreateInProgress, "")

		// An unresolvable dynamic reference fails the resource. Provisioning it
		// anyway would persist the literal "{{resolve:...}}" text as the
		// property's value, which every service downstream then treats as data.
		props, recordedProps, provErr := p.resolveProperties(res, rCtx)
		propsHash := hashResourceProperties(res.Type, recordedProps, stack.Tags)
		resStart := p.clk.Now()
		var physID string
		if provErr == nil {
			physID, provErr = p.provisionResource(ctx, logicalID, res, props, rCtx)
		}
		resElapsed := p.clk.Since(resStart)
		now := p.clk.Now()
		if provErr != nil {
			// Record failed resource state and emit CREATE_FAILED with reason.
			// physID is empty for a create that never got as far as naming the
			// resource, and set for one that failed to stabilize — which leaves
			// a real resource behind for rollback to delete.
			stack.Resources = append(stack.Resources, StackResource{
				LogicalID:    logicalID,
				PhysicalID:   physID,
				Type:         res.Type,
				Status:       ResourceCreateFailed,
				StatusReason: provErr.Error(),
				Timestamp:    now,
			})
			p.recordEvent(ctx, stack, logicalID, physID, res.Type, ResourceCreateFailed, provErr.Error())

			if stack.DisableRollback {
				// DisableRollback: leave partial stack, status CREATE_FAILED.
				p.failStack(ctx, stack, StatusCreateFailed,
					fmt.Sprintf("resource %s failed: %v", logicalID, provErr))
				return
			}

			// Default behaviour: roll back already-created resources, then set
			// status to ROLLBACK_COMPLETE (matching real AWS CloudFormation).
			p.rollbackCreate(ctx, stack, rCtx,
				fmt.Sprintf("resource %s failed: %v", logicalID, provErr))
			return
		}

		// Record successful resource state and emit CREATE_COMPLETE.
		stack.Resources = append(stack.Resources, StackResource{
			LogicalID:           logicalID,
			PhysicalID:          physID,
			Type:                res.Type,
			Status:              ResourceCreateComplete,
			Timestamp:           now,
			Attributes:          rCtx.Attributes[logicalID],
			PropertiesHash:      propsHash,
			Properties:          recordedProps,
			DeletionPolicy:      res.DeletionPolicy,
			UpdateReplacePolicy: res.UpdateReplacePolicy,
		})
		rCtx.Resources[logicalID] = physID
		p.recordEvent(ctx, stack, logicalID, physID, res.Type, ResourceCreateComplete, "")
		p.publishResourceEvent(ctx, events.CFNResourceProvisioned, stack.StackName, logicalID, res.Type, physID)
		p.log.Debug("cfn: resource provisioned",
			zap.String("stack", stack.StackName),
			zap.String("logicalId", logicalID),
			zap.String("type", res.Type),
			zap.Duration("elapsed", resElapsed))

	}

	// Resolve outputs.
	stack.Outputs = p.resolveOutputs(tmpl, rCtx)

	// Mark stack complete and emit the final stack event.
	stack.Status = StatusCreateComplete
	stack.StatusReason = ""
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusCreateComplete, "")
	if err := p.flushCriticalState(ctx); err != nil {
		p.failStack(ctx, stack, StatusCreateFailed, fmt.Sprintf("persistent state flush failed: %v", err))
		return
	}
	p.publishStackEvent(ctx, events.CFNStackCreated, stack)
	p.log.Debug("cfn: stack provisioned",
		zap.String("stack", stack.StackName),
		zap.Int("resources", len(order)),
		zap.Duration("elapsed", p.clk.Since(stackStart)))
}

// ── Update stack (async) ───────────────────────────────────────────────────

func (p *provisioner) updateStack(stack *Stack, tmpl *Template, previousStackTags []Tag, onComplete stackCompletionFunc) {
	done := make(chan struct{})
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer close(done)
		p.updateStackResources(stack, tmpl, previousStackTags)
		if onComplete != nil {
			onComplete(p.regionCtx(stack.Region), stack)
		}
	}()
	p.awaitBriefly(done)
}

func (p *provisioner) updateStackResources(stack *Stack, tmpl *Template, previousStackTags []Tag) {
	ctx := p.regionCtx(stack.Region)

	rCtx := p.buildResolveContext(stack, tmpl)
	rCtx.PreviousStackTags = append([]Tag(nil), previousStackTags...)

	// Emit the initial stack UPDATE_IN_PROGRESS event.
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusUpdateInProgress, "User Initiated")

	// For simplicity: treat all resources as requiring re-creation.
	// existing tracks resources still to be accounted for; entries are removed
	// as the template's resources are processed, so whatever remains at the end
	// is what the template dropped and the cleanup phase must delete.
	existing := map[string]StackResource{}
	// preUpdate is the same set, kept intact: rollback restores the stack's
	// resource list from it. It must not be the map above — that one is
	// emptied as the update proceeds, so using it would drop every resource
	// the update had already handled and leave the stack owning nothing.
	preUpdate := map[string]StackResource{}
	for _, r := range stack.Resources {
		existing[r.LogicalID] = r
		preUpdate[r.LogicalID] = r
	}

	order, err := topoSort(tmpl.Resources)
	if err != nil {
		p.failStack(ctx, stack, StatusUpdateFailed, fmt.Sprintf("dependency cycle: %v", err))
		return
	}

	var newResources []StackResource
	// Old resources that replacements superseded. They stay alive until the
	// whole update succeeds — see updateResource — and are deleted in the
	// cleanup phase at the end, or left standing for rollback if it fails.
	var superseded []supersededResource
	// logical ID → the replacement's new physical ID, so rollback knows which
	// resources it created and must remove.
	replacedBy := map[string]string{}
	// Tracks only updates that actually succeeded through resourceUpdater.
	// Replacement fallbacks can return the same physical ID for upsert-shaped
	// services and must not be replayed as in-place updates during rollback.
	inPlaceUpdated := map[string]bool{}
	// A handler may fail after applying a mutation and also fail its own
	// compensation. Keep that distinct from a validation rejection so rollback
	// does not report a clean restoration over service-side state it could not
	// restore.
	dirtyUpdates := map[string]bool{}

	for _, logicalID := range order {
		if ctx.Err() != nil {
			p.failStack(ctx, stack, StatusUpdateFailed, "cancelled")
			return
		}
		res := tmpl.Resources[logicalID]

		// As in the create path: a reference that will not resolve fails the
		// resource rather than being written into it verbatim.
		props, recordedProps, refErr := p.resolveProperties(res, rCtx)
		propsHash := hashResourceProperties(res.Type, recordedProps, stack.Tags)

		if old, ok := existing[logicalID]; ok && old.Type == res.Type {
			// Same logical ID and type. Diff the resolved properties and
			// either skip (no change), update in-place (handler supports it),
			// or fall back to delete + create (handler doesn't).
			rCtx.Resources[logicalID] = old.PhysicalID
			if old.Attributes != nil {
				if rCtx.Attributes == nil {
					rCtx.Attributes = make(map[string]map[string]string)
				}
				rCtx.Attributes[logicalID] = old.Attributes
			}

			if refErr == nil && resourcePropertiesMatch(old.PropertiesHash, res.Type, recordedProps, stack.Tags, previousStackTags) {
				// No change, or legacy resource without a recorded hash —
				// treat as unchanged. (Stacks created before property
				// hashing was added have no recorded hash; without a
				// known prior state we assume the user did not intend
				// to mutate every resource on the next update.)
				newResources = append(newResources, StackResource{
					LogicalID:           logicalID,
					PhysicalID:          old.PhysicalID,
					Type:                res.Type,
					Status:              old.Status,
					StatusReason:        old.StatusReason,
					Timestamp:           old.Timestamp,
					Attributes:          old.Attributes,
					PropertiesHash:      propsHash,
					Properties:          recordedProps,
					DeletionPolicy:      res.DeletionPolicy,
					UpdateReplacePolicy: res.UpdateReplacePolicy,
				})
				delete(existing, logicalID)
				continue
			}

			// Properties changed — attempt update.
			p.recordEvent(ctx, stack, logicalID, old.PhysicalID, res.Type, ResourceUpdateInProgress, "")
			outcome, updErr := resourceUpdateOutcome{}, refErr
			if updErr == nil {
				outcome, updErr = p.updateResource(ctx, logicalID, res, props, old.PhysicalID, &old, rCtx)
			}
			physID := outcome.PhysicalID
			if outcome.Replaced() {
				// Rollback must remove the replacement whether or not the
				// original is retained.
				replacedBy[logicalID] = physID
				if !outcome.RetainReplaced {
					// The original stays alive until the whole update succeeds,
					// so a later failure can still roll back to it.
					superseded = append(superseded, supersededResource{
						LogicalID: logicalID, Type: old.Type, PhysicalID: outcome.ReplacedPhysicalID,
					})
				}
			}
			if outcome.UpdatedInPlace {
				inPlaceUpdated[logicalID] = true
			}
			now := p.clk.Now()
			if updErr != nil {
				resourceDirty := isDirtyUpdateFailure(updErr)
				resourceStateChanged := resourceDirty || outcome.UpdatedInPlace
				if resourceDirty {
					dirtyUpdates[logicalID] = true
				}
				failedResource := StackResource{
					LogicalID:    logicalID,
					PhysicalID:   old.PhysicalID,
					Type:         res.Type,
					Status:       ResourceUpdateFailed,
					StatusReason: updErr.Error(),
					Timestamp:    now,
				}
				if resourceStateChanged {
					if outcome.PhysicalID != "" {
						failedResource.PhysicalID = outcome.PhysicalID
					}
					failedResource.Attributes = rCtx.Attributes[logicalID]
					failedResource.PropertiesHash = propsHash
					failedResource.Properties = recordedProps
					failedResource.DeletionPolicy = res.DeletionPolicy
					failedResource.UpdateReplacePolicy = res.UpdateReplacePolicy
				}
				newResources = append(newResources, failedResource)
				p.recordEvent(ctx, stack, logicalID, failedResource.PhysicalID, res.Type, ResourceUpdateFailed, updErr.Error())
				if stack.DisableRollback {
					p.failStack(ctx, stack, StatusUpdateFailed,
						fmt.Sprintf("resource %s failed: %v", logicalID, updErr))
					return
				}
				p.rollbackUpdate(ctx, stack, newResources, preUpdate, replacedBy, inPlaceUpdated, dirtyUpdates, rCtx,
					fmt.Sprintf("resource %s failed: %v", logicalID, updErr))
				return
			}
			newResources = append(newResources, StackResource{
				LogicalID:           logicalID,
				PhysicalID:          physID,
				Type:                res.Type,
				Status:              ResourceUpdateComplete,
				Timestamp:           now,
				Attributes:          rCtx.Attributes[logicalID],
				PropertiesHash:      propsHash,
				Properties:          recordedProps,
				DeletionPolicy:      res.DeletionPolicy,
				UpdateReplacePolicy: res.UpdateReplacePolicy,
			})
			rCtx.Resources[logicalID] = physID
			p.recordEvent(ctx, stack, logicalID, physID, res.Type, ResourceUpdateComplete, "")
			p.publishResourceEvent(ctx, events.CFNResourceProvisioned, stack.StackName, logicalID, res.Type, physID)
			delete(existing, logicalID)
			continue
		}

		// New (or different type) — emit CREATE_IN_PROGRESS before provisioning.
		p.recordEvent(ctx, stack, logicalID, "", res.Type, ResourceCreateInProgress, "")

		var physID string
		provErr := refErr
		if provErr == nil {
			physID, provErr = p.provisionResource(ctx, logicalID, res, props, rCtx)
		}
		now := p.clk.Now()
		if provErr != nil {
			// As on the create path: a resource that failed to stabilize has a
			// physical ID, and rollback needs it to clean the resource up.
			newResources = append(newResources, StackResource{
				LogicalID:    logicalID,
				PhysicalID:   physID,
				Type:         res.Type,
				Status:       ResourceCreateFailed,
				StatusReason: provErr.Error(),
				Timestamp:    now,
			})
			p.recordEvent(ctx, stack, logicalID, physID, res.Type, ResourceCreateFailed, provErr.Error())
			if stack.DisableRollback {
				p.failStack(ctx, stack, StatusUpdateFailed,
					fmt.Sprintf("resource %s failed: %v", logicalID, provErr))
				return
			}
			// Roll back: delete newly created resources (those not in `existing`)
			// in reverse order, then restore the previous resource list.
			p.rollbackUpdate(ctx, stack, newResources, preUpdate, replacedBy, inPlaceUpdated, dirtyUpdates, rCtx,
				fmt.Sprintf("resource %s failed: %v", logicalID, provErr))
			return
		}
		newResources = append(newResources, StackResource{
			LogicalID:           logicalID,
			PhysicalID:          physID,
			Type:                res.Type,
			Status:              ResourceCreateComplete,
			Timestamp:           now,
			Attributes:          rCtx.Attributes[logicalID],
			PropertiesHash:      propsHash,
			Properties:          recordedProps,
			DeletionPolicy:      res.DeletionPolicy,
			UpdateReplacePolicy: res.UpdateReplacePolicy,
		})
		rCtx.Resources[logicalID] = physID
		p.recordEvent(ctx, stack, logicalID, physID, res.Type, ResourceCreateComplete, "")
		p.publishResourceEvent(ctx, events.CFNResourceProvisioned, stack.StackName, logicalID, res.Type, physID)
	}

	// Cleanup phase. Every resource is updated; what remains is removing what
	// the update superseded. CloudFormation reports this separately rather than
	// folding it into UPDATE_IN_PROGRESS, so a stack held up by a resource that
	// will not delete is visibly stuck here rather than looking mid-update.
	stack.Status = StatusUpdateCompleteCleanupInProgress
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack",
		StatusUpdateCompleteCleanupInProgress, "")

	// Delete removed resources, honouring DeletionPolicy=Retain.
	for logicalID, old := range existing {
		if old.shouldRetainOnDelete() {
			p.log.Info("cfn: retaining removed resource (DeletionPolicy=Retain)",
				zap.String("type", old.Type),
				zap.String("logicalId", logicalID),
				zap.String("physicalId", old.PhysicalID))
			continue
		}
		p.recordEvent(ctx, stack, logicalID, old.PhysicalID, old.Type, ResourceDeleteInProgress, "")
		// A refusal here does not fail the update. This is the cleanup phase,
		// which AWS runs after the update has already succeeded and does not
		// roll back — so the event says the resource is still standing and the
		// stack still completes, rather than either lying about the delete or
		// failing an update that worked.
		if err := p.deleteResource(ctx, logicalID, old.Type, old.PhysicalID, rCtx); err != nil {
			p.recordEvent(ctx, stack, logicalID, old.PhysicalID, old.Type, ResourceDeleteFailed, err.Error())
			continue
		}
		p.recordEvent(ctx, stack, logicalID, old.PhysicalID, old.Type, ResourceDeleteComplete, "")
		p.publishResourceEvent(ctx, events.CFNResourceDeleted, stack.StackName, logicalID, old.Type, old.PhysicalID)
	}

	// The originals that replacements superseded, deleted here rather than at
	// the point of replacement so that a failure anywhere earlier could still
	// roll back to them.
	for _, s := range superseded {
		p.recordEvent(ctx, stack, s.LogicalID, s.PhysicalID, s.Type, ResourceDeleteInProgress, "")
		if err := p.deleteResource(ctx, s.LogicalID, s.Type, s.PhysicalID, rCtx); err != nil {
			p.recordEvent(ctx, stack, s.LogicalID, s.PhysicalID, s.Type, ResourceDeleteFailed, err.Error())
			continue
		}
		p.recordEvent(ctx, stack, s.LogicalID, s.PhysicalID, s.Type, ResourceDeleteComplete, "")
	}

	stack.Resources = newResources
	stack.Outputs = p.resolveOutputs(tmpl, rCtx)
	now := p.clk.Now()
	stack.UpdatedAt = &now
	stack.Status = StatusUpdateComplete
	stack.StatusReason = ""
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusUpdateComplete, "")
	if err := p.flushCriticalState(ctx); err != nil {
		p.failStack(ctx, stack, StatusUpdateFailed, fmt.Sprintf("persistent state flush failed: %v", err))
		return
	}
	p.publishStackEvent(ctx, events.CFNStackUpdated, stack)
}

// ── Delete stack (async) ───────────────────────────────────────────────────

func (p *provisioner) deleteStack(stack *Stack) {
	done := make(chan struct{})
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer close(done)
		p.deleteStackResources(stack)
	}()
	p.awaitBriefly(done)
}

// deleteStackResources is the synchronous core of stack deletion.
// It tears down all resources in reverse order and marks the stack as
// DELETE_COMPLETE. Both top-level deleteStack (async) and nestedStackHandler
// (inline) use this method.
func (p *provisioner) deleteStackResources(stack *Stack) {
	ctx := p.regionCtx(stack.Region)

	rCtx := &resolveContext{
		Region:    stack.Region,
		AccountID: p.cfg.AccountID,
		StackName: stack.StackName,
		StackID:   stack.StackID,
	}
	if rCtx.Region == "" {
		rCtx.Region = p.cfg.Region
	}

	// Emit the initial stack DELETE_IN_PROGRESS event.
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusDeleteInProgress, "User Initiated")

	// Delete resources in reverse order, honouring DeletionPolicy=Retain.
	for i := len(stack.Resources) - 1; i >= 0; i-- {
		r := stack.Resources[i]
		if ctx.Err() != nil {
			p.failStack(ctx, stack, StatusDeleteFailed, "cancelled")
			return
		}
		if r.PhysicalID == "" {
			continue
		}
		if r.shouldRetainOnDelete() {
			p.log.Info("cfn: retaining resource on stack delete (DeletionPolicy=Retain)",
				zap.String("type", r.Type),
				zap.String("logicalId", r.LogicalID),
				zap.String("physicalId", r.PhysicalID))
			stack.Resources[i].Status = ResourceDeleteSkipped
			p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteSkipped, "DeletionPolicy=Retain")
			continue
		}
		stack.Resources[i].Status = ResourceDeleteInProgress
		p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteInProgress, "")
		if err := p.deleteResource(ctx, r.LogicalID, r.Type, r.PhysicalID, rCtx); err != nil {
			// The resource refused. Leave it in the stack's resource list —
			// AWS keeps a DELETE_FAILED resource visible so the retry, once
			// the block is cleared, knows what is still standing.
			stack.Resources[i].Status = ResourceDeleteFailed
			stack.Resources[i].StatusReason = err.Error()
			p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteFailed, err.Error())
			p.failStack(ctx, stack, StatusDeleteFailed, err.Error())
			return
		}
		stack.Resources[i].Status = ResourceDeleteComplete
		p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteComplete, "")
		p.publishResourceEvent(ctx, events.CFNResourceDeleted, stack.StackName, r.LogicalID, r.Type, r.PhysicalID)
	}

	now := p.clk.Now()
	stack.DeletedAt = &now
	stack.Status = StatusDeleteComplete
	stack.StatusReason = ""
	stack.Resources = nil
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusDeleteComplete, "")
	if err := p.flushCriticalState(ctx); err != nil {
		p.failStack(ctx, stack, StatusDeleteFailed, fmt.Sprintf("persistent state flush failed: %v", err))
		return
	}
	p.publishStackEvent(ctx, events.CFNStackDeleted, stack)
}

// ── Resource provisioning ──────────────────────────────────────────────────

// resolveHandler returns the resource handler for a given CloudFormation type.
// It first checks the static resourceHandlers map, then handles provisioner-
// linked types that require access to the provisioner (custom resources and
// nested stacks).
func (p *provisioner) resolveHandler(resType string) (resourceHandler, bool) {
	if h, ok := resourceHandlers[resType]; ok {
		return h, true
	}
	// Custom::* and AWS::CloudFormation::CustomResource both use the custom
	// resource protocol (Lambda invocation via ServiceToken).
	if strings.HasPrefix(resType, "Custom::") || resType == "AWS::CloudFormation::CustomResource" {
		return &customResourceHandler{p: p}, true
	}
	// Nested stacks require synchronous provisioning through the provisioner.
	if resType == "AWS::CloudFormation::Stack" {
		return &nestedStackHandler{p: p}, true
	}
	return nil, false
}

// provisionResource creates a resource by dispatching an internal HTTP request.
// props are the already-resolved properties (after Ref / Fn::GetAtt / etc.
// substitution). Callers resolve once, hash, and pass in.
func (p *provisioner) provisionResource(ctx context.Context, logicalID string, res TemplateResource, props map[string]any, rCtx *resolveContext) (string, error) {
	p.mu.Lock()
	router := p.router
	p.mu.Unlock()
	if router == nil {
		return "", fmt.Errorf("router not initialised")
	}

	// Handlers name unnamed resources from the logical ID (see
	// resolveContext.generatedName). Provisioning walks the dependency order
	// one resource at a time, so a single field on the shared context is safe.
	rCtx.LogicalID = logicalID

	handler, ok := p.resolveHandler(res.Type)
	if !ok {
		// Unknown resource type — generate a fake physical ID and succeed.
		// This allows templates with unsupported resources to partially deploy.
		physID := fmt.Sprintf("%s-%s-stub", rCtx.StackName, logicalID)
		p.log.Warn("cfn: unsupported resource type, creating stub",
			zap.String("type", res.Type),
			zap.String("logicalId", logicalID),
			zap.String("physicalId", physID))
		return physID, nil
	}

	physID, attrs, err := handler.Create(ctx, router, p.cfg, props, rCtx)
	if err != nil {
		// A handler may create the primary resource before a later configuration
		// call fails. Preserve its identity so the caller can record the failed
		// resource and rollback can delete what exists service-side.
		return physID, err
	}
	// Store attributes for Fn::GetAtt resolution.
	if len(attrs) > 0 {
		if rCtx.Attributes == nil {
			rCtx.Attributes = make(map[string]map[string]string)
		}
		rCtx.Attributes[logicalID] = attrs
	}

	// The resource exists but may not yet be usable — see resourceStabilizer.
	// The physical ID travels out with a failure here precisely because the
	// resource is real: the caller records it against the failed resource so
	// rollback can delete it.
	if stabilizer, ok := handler.(resourceStabilizer); ok {
		if err := stabilizer.Stabilize(ctx, router, p.cfg, p.clk, physID, rCtx); err != nil {
			return physID, err
		}
	}

	// CDK's Application construct propagates `awsApplication=<app-arn>` to
	// every resource in the stack. Honour it by recording a direct resource
	// association so the UI can resolve ownership via ListAssociatedResources.
	if appTag := extractAwsApplicationTag(props); appTag != "" && physID != "" {
		p.autoAssociateResource(ctx, rCtx, appTag, physID)
	}
	return physID, nil
}

// updateResource updates an existing resource in place when the handler
// supports it (resourceUpdater interface). Otherwise it replaces the resource:
// a new one is created and the old one is handed back to the caller to delete
// once the whole stack update has succeeded.
//
// Returns what the update did (see resourceUpdateOutcome) and an error.
//
// Replacement creates before deleting, as real CloudFormation does. The old
// resource is what an update rolls back to, so destroying it up front would
// make rollback impossible: a create that then failed would leave nothing
// behind at all. Deletion is deferred to the caller's cleanup phase, which
// only runs once every resource in the stack has been updated successfully.
//
// If the resource has UpdateReplacePolicy=Retain (or Snapshot) the original is
// orphaned rather than deleted, so it outlives the stack. It is still reported
// as replaced, because rollback must remove the replacement regardless.
func (p *provisioner) updateResource(ctx context.Context, logicalID string, res TemplateResource, props map[string]any, oldPhysicalID string, oldResource *StackResource, rCtx *resolveContext) (resourceUpdateOutcome, error) {
	p.mu.Lock()
	router := p.router
	p.mu.Unlock()
	if router == nil {
		return resourceUpdateOutcome{}, fmt.Errorf("router not initialised")
	}

	// Same reason as in provisionResource: an update may fall through to a
	// create, which needs the logical ID to name an unnamed resource.
	rCtx.LogicalID = logicalID

	handler, ok := p.resolveHandler(res.Type)
	if !ok {
		// Unknown resource type — keep stub physical ID, no-op.
		return resourceUpdateOutcome{PhysicalID: oldPhysicalID}, nil
	}

	// Prefer in-place update when supported.
	if updater, ok := handler.(resourceUpdater); ok {
		var oldProps map[string]any
		if oldResource != nil {
			oldProps = oldResource.Properties
		}
		physID, attrs, err := updater.Update(ctx, router, p.cfg, oldPhysicalID, props, oldProps, rCtx)
		if err == nil {
			if physID == "" {
				physID = oldPhysicalID
			}
			outcome := resourceUpdateOutcome{PhysicalID: physID, UpdatedInPlace: true}
			if len(attrs) > 0 {
				if rCtx.Attributes == nil {
					rCtx.Attributes = make(map[string]map[string]string)
				}
				rCtx.Attributes[logicalID] = attrs
			}
			// The change has been applied; the resource may still be settling
			// into it — see resourceStabilizer. Failing to settle is terminal
			// for the resource rather than a reason to replace it, so it takes
			// the same updateFailure path a handler's own failure does. The
			// successful outcome still travels back so rollback can reverse it.
			if stabilizer, ok := handler.(resourceStabilizer); ok {
				if stErr := stabilizer.Stabilize(ctx, router, p.cfg, p.clk, physID, rCtx); stErr != nil {
					return outcome, failUpdate(stErr)
				}
			}
			return outcome, nil
		}
		// An update that applied and then failed is terminal — replacing the
		// resource would neither fix it nor be what AWS does. See updateFailure.
		var failed updateFailure
		if errors.As(err, &failed) {
			return resourceUpdateOutcome{}, err
		}
		// Sentinel: fall through to replacement (mirrors AWS "Replacement: Yes"
		// for properties like resource Name or DynamoDB KeySchema).
		if !errors.Is(err, errReplacementRequired) {
			p.log.Warn("cfn: in-place update failed, falling back to replace",
				zap.String("type", res.Type),
				zap.String("logicalId", logicalID),
				zap.Error(err))
		}
	}

	// Replacement path: create the new resource first, leaving the old one
	// untouched. If this fails the caller rolls back to the old resource,
	// which is only possible because it still exists.
	//
	// A name the template supplies is the caller's problem — creating a second
	// resource with the same name will 409, exactly as it does on AWS, which
	// is why AWS treats a name change as replacement and generates unique
	// names for resources the template does not name (see generatedName).
	newPhysID, err := p.provisionResource(ctx, logicalID, res, props, rCtx)
	if err != nil {
		// Create may have named the replacement before its stabilizer failed.
		// Preserve both IDs so the caller can delete the failed replacement
		// during rollback while leaving the original resource in place.
		if newPhysID != "" {
			return resourceUpdateOutcome{
				PhysicalID:         newPhysID,
				ReplacedPhysicalID: oldPhysicalID,
			}, err
		}
		return resourceUpdateOutcome{}, err
	}

	// A "replacement" that hands back the original's physical ID is not a
	// replacement — there is only one resource. Services whose create is an
	// upsert keyed by a caller-pinned name (CloudWatch PutMetricAlarm, Route53
	// UPSERT-shaped records) do exactly this. Reporting it as replaced would
	// hand that one ID to the post-success cleanup and to rollback, both of
	// which delete it — destroying the resource behind a stack that claims
	// UPDATE_COMPLETE. Real CloudFormation cannot reach this state: it refuses
	// to replace a custom-named resource whose name did not change.
	if newPhysID == oldPhysicalID {
		return resourceUpdateOutcome{PhysicalID: newPhysID}, nil
	}

	outcome := resourceUpdateOutcome{PhysicalID: newPhysID, ReplacedPhysicalID: oldPhysicalID}
	if oldResource != nil && oldResource.shouldRetainOnReplace() {
		// UpdateReplacePolicy=Retain orphans the original instead of deleting
		// it. Still a replacement, so rollback removes the replacement.
		outcome.RetainReplaced = true
		p.log.Info("cfn: retaining old resource on replacement (UpdateReplacePolicy=Retain)",
			zap.String("type", res.Type),
			zap.String("logicalId", logicalID),
			zap.String("orphanedPhysicalId", oldPhysicalID))
	}
	return outcome, nil
}

// supersededResource is an old physical resource that a replacement replaced.
// It is deleted only after the whole stack update succeeds — until then it is
// what the update rolls back to.
type supersededResource struct {
	LogicalID  string
	Type       string
	PhysicalID string
}

// resourceUpdateOutcome describes what an update did to a single resource.
//
// Replacement and retention are separate facts, and conflating them loses one:
// a retained original must not be deleted at cleanup, but the resource was
// still replaced, and rollback has to remove the replacement either way.
type resourceUpdateOutcome struct {
	// PhysicalID is the resource's physical ID after the update.
	PhysicalID string
	// ReplacedPhysicalID is the original that a replacement superseded, or ""
	// when the resource was updated in place.
	ReplacedPhysicalID string
	// RetainReplaced reports that UpdateReplacePolicy keeps the original, so
	// the cleanup phase must leave it alone.
	RetainReplaced bool
	UpdatedInPlace bool
}

// Replaced reports whether this update replaced the resource rather than
// updating it in place.
func (o resourceUpdateOutcome) Replaced() bool { return o.ReplacedPhysicalID != "" }

// hashProps returns a stable sha256 hash of the resolved property map.
// Used by UpdateStack to detect property drift and reprovision only
// resources whose properties actually changed.
func hashProps(props map[string]any) string {
	// json.Marshal of a map produces keys in sorted order in Go's encoding/json,
	// so the hash is stable across runs for equivalent inputs.
	data, err := json.Marshal(props)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hashResourceProperties includes only tags CloudFormation propagates outside
// the resource property map. Resource-level tags are already present in props;
// merging here also avoids updates when they override a changed stack tag.
func hashResourceProperties(resourceType string, props map[string]any, stackTags []Tag) string {
	switch resourceType {
	case "AWS::Lambda::Function", "AWS::Lambda::EventSourceMapping":
		return hashProps(map[string]any{
			"Properties":    props,
			"EffectiveTags": mergeLambdaTags(stackTags, props["Tags"]),
		})
	case "AWS::CloudFormation::Stack":
		return hashProps(map[string]any{
			"Properties":    props,
			"EffectiveTags": mergeNestedStackTags(stackTags, props["Tags"]),
		})
	default:
		return hashProps(props)
	}
}

func resourcePropertiesMatch(oldHash, resourceType string, props map[string]any, stackTags, previousStackTags []Tag) bool {
	if oldHash == hashResourceProperties(resourceType, props, stackTags) {
		return true
	}
	// Hashes written before propagated-tag tracking contained only the property
	// map. Preserve their no-op behavior when effective tags did not change,
	// while still reconciling a real stack-only tag delta.
	if resourceType != "AWS::Lambda::Function" && resourceType != "AWS::Lambda::EventSourceMapping" && resourceType != "AWS::CloudFormation::Stack" {
		return oldHash == ""
	}
	var currentTags, previousTags any
	if resourceType == "AWS::CloudFormation::Stack" {
		currentTags = mergeNestedStackTags(stackTags, props["Tags"])
		previousTags = mergeNestedStackTags(previousStackTags, props["Tags"])
	} else {
		currentTags = mergeLambdaTags(stackTags, props["Tags"])
		previousTags = mergeLambdaTags(previousStackTags, props["Tags"])
	}
	effectiveTagsUnchanged := reflect.DeepEqual(currentTags, previousTags)
	return effectiveTagsUnchanged && (oldHash == "" || oldHash == hashProps(props))
}

// deleteResource tears down a provisioned resource.
//
// It reports only a refusal by the resource itself (errDeletionBlocked); every
// other teardown error is logged and swallowed, so a resource that has already
// gone cannot wedge a stack teardown.
func (p *provisioner) deleteResource(ctx context.Context, logicalID, resType, physicalID string, rCtx *resolveContext) error {
	p.mu.Lock()
	router := p.router
	p.mu.Unlock()
	if router == nil {
		return nil
	}

	handler, ok := p.resolveHandler(resType)
	if !ok {
		return nil // stub resources have nothing to delete
	}

	err := handler.Delete(ctx, router, p.cfg, physicalID, rCtx)
	if err == nil {
		return nil
	}
	p.log.Warn("cfn: failed to delete resource",
		zap.String("type", resType),
		zap.String("logicalId", logicalID),
		zap.String("physicalId", physicalID),
		zap.Error(err))
	if errors.Is(err, errDeletionBlocked) {
		return err
	}
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────────

func (p *provisioner) buildResolveContext(stack *Stack, tmpl *Template) *resolveContext {
	params := make(map[string]string)
	for _, param := range stack.Parameters {
		params[param.Key] = param.Value
	}
	// Apply defaults for unset parameters. Empty string is a valid default —
	// CDK bootstrap templates rely on empty-string defaults for optional
	// parameters like FileAssetsBucketName so that Fn::Equals/Fn::If
	// resolves correctly.
	for name, def := range tmpl.Parameters {
		if _, ok := params[name]; !ok {
			params[name] = string(def.Default)
		}
	}

	region := stack.Region
	if region == "" {
		region = p.cfg.Region
	}

	// Collect cross-stack exports for Fn::ImportValue resolution.
	exports := p.collectExports(stack)

	return &resolveContext{
		Region:     region,
		AccountID:  p.cfg.AccountID,
		StackName:  stack.StackName,
		StackID:    stack.StackID,
		StackTags:  append([]Tag(nil), stack.Tags...),
		Params:     params,
		Resources:  make(map[string]string),
		Conditions: evaluateConditions(tmpl.Conditions, params),
		Mappings:   tmpl.Mappings,
		Exports:    exports,
		DynamicRef: p.dynamicRefResolver(region),
	}
}

// resolveProperties returns the two forms of a resource's resolved properties,
// and surfaces any dynamic reference that could not be resolved.
//
// recorded has the intrinsics resolved but every "{{resolve:...}}" left exactly
// as the template wrote it. It is what gets hashed for change detection and
// what gets stored on the stack resource, because that is what CloudFormation
// itself compares — the literal reference string, not the value behind it:
// "Updating only the secret value in Secrets Manager doesn't automatically
// cause CloudFormation to retrieve the new value." Hashing the resolved form
// instead would mean a rotated secret made every stack think its database had
// changed, and — now that a MasterUsername change forces replacement — could
// replace a database because a secret rotated. It also keeps resolved secrets
// out of the store: for secure references AWS "never stores the actual secure
// string value... only the literal dynamic reference".
//
// expanded additionally has the references resolved, and is what the resource
// handler is given. That is the one place AWS does let the value reach: "the
// secret value may show up in the service whose resource it's being used in".
//
// The error must be taken here rather than at dispatch: a resource whose
// properties are unchanged never dispatches at all, and a failure left on the
// context would be blamed on the next resource instead.
func (p *provisioner) resolveProperties(res TemplateResource, rCtx *resolveContext) (expanded, recorded map[string]any, err error) {
	recorded = resolveAllProperties(res.Properties, rCtx)
	expanded, _ = expandDynamicRefs(recorded, rCtx).(map[string]any)
	if expanded == nil {
		expanded = recorded
	}
	return expanded, recorded, rCtx.takeDynamicRefErr()
}

// collectExports gathers all cross-stack exports from completed stacks in the
// same region. Uses a background context so region is derived from the stack.
func (p *provisioner) collectExports(stack *Stack) map[string]string {
	region := stack.Region
	if region == "" {
		region = p.cfg.Region
	}
	ctx := middleware.ContextWithRegion(p.ctx, region)
	allExports, aerr := p.store.listExports(ctx)
	if aerr != nil {
		return nil
	}
	exports := make(map[string]string, len(allExports))
	for _, e := range allExports {
		exports[e.Name] = e.Value
	}
	return exports
}

func (p *provisioner) resolveOutputs(tmpl *Template, rCtx *resolveContext) []Output {
	if tmpl.Outputs == nil {
		return nil
	}
	outputs := make([]Output, 0, len(tmpl.Outputs))
	for name, o := range tmpl.Outputs {
		if o.Condition != "" && !rCtx.Conditions[o.Condition] {
			continue
		}
		// Dynamic references are deliberately not expanded in outputs. Real
		// CloudFormation leaves them as written here — an output built from a
		// secretsmanager reference comes back as the literal
		// "{{resolve:secretsmanager:...}}" string, with no error — and an
		// output is exactly where a resolved secret would be most exposed:
		// DescribeStacks returns it, and CloudFormation "doesn't redact or
		// obfuscate any information you include in the Outputs section".
		val := resolveIntrinsics(o.Value, rCtx)
		out := Output{
			Key:         name,
			Value:       fmt.Sprintf("%v", val),
			Description: o.Description,
		}
		if o.Export != nil {
			out.ExportName = fmt.Sprintf("%v", resolveIntrinsics(o.Export.Name, rCtx))
		}
		outputs = append(outputs, out)
	}
	return outputs
}

func (p *provisioner) failStack(ctx context.Context, stack *Stack, status, reason string) {
	stack.Status = status
	stack.StatusReason = reason
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", status, reason)
	if err := p.flushCriticalState(ctx); err != nil {
		p.log.Warn("cfn: failed to flush terminal stack state", zap.String("stack", stack.StackName), zap.Error(err))
	}
	p.publishStackEvent(ctx, events.CFNStackFailed, stack)
}

func (p *provisioner) flushCriticalState(ctx context.Context) error {
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return p.store.flush(flushCtx)
}

// rollbackCreate is the default failure handler for CreateStack.
// It mirrors real AWS CloudFormation behaviour: delete every successfully
// created resource in reverse order, then mark the stack ROLLBACK_COMPLETE.
// If a delete fails the stack is marked ROLLBACK_FAILED instead.
func (p *provisioner) rollbackCreate(ctx context.Context, stack *Stack, rCtx *resolveContext, reason string) {
	stack.Status = StatusRollbackInProgress
	stack.StatusReason = reason
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusRollbackInProgress, reason)
	p.publishStackEvent(ctx, events.CFNStackFailed, stack)

	rollbackFailed := false
	for i := len(stack.Resources) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			rollbackFailed = true
			break
		}
		r := stack.Resources[i]
		if !r.existsServiceSide() {
			continue
		}
		stack.Resources[i].Status = ResourceDeleteInProgress
		p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteInProgress, "")

		handler, ok := p.resolveHandler(r.Type)
		if !ok {
			stack.Resources[i].Status = ResourceDeleteComplete
			p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteComplete, "")
			continue
		}
		p.mu.Lock()
		router := p.router
		p.mu.Unlock()
		if err := handler.Delete(ctx, router, p.cfg, r.PhysicalID, rCtx); err != nil {
			p.log.Warn("cfn: rollback: failed to delete resource",
				zap.String("logicalId", r.LogicalID),
				zap.String("type", r.Type),
				zap.Error(err))
			stack.Resources[i].Status = ResourceDeleteFailed
			stack.Resources[i].StatusReason = err.Error()
			p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteFailed, err.Error())
			rollbackFailed = true
		} else {
			stack.Resources[i].Status = ResourceDeleteComplete
			p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteComplete, "")
			p.publishResourceEvent(ctx, events.CFNResourceDeleted, stack.StackName, r.LogicalID, r.Type, r.PhysicalID)
		}
	}

	if rollbackFailed {
		stack.Status = StatusRollbackFailed
	} else {
		stack.Status = StatusRollbackComplete
		stack.StatusReason = ""
	}
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", stack.Status, stack.StatusReason)
	if err := p.flushCriticalState(ctx); err != nil {
		p.log.Warn("cfn: failed to flush rollback state", zap.String("stack", stack.StackName), zap.Error(err))
	}
	p.publishStackEvent(ctx, events.CFNStackFailed, stack)
}

// rollbackUpdate is the default failure handler for UpdateStack.
//
// It undoes the update in reverse order and restores the previous resource
// list, marking the stack UPDATE_ROLLBACK_COMPLETE when every resource is
// restored. Four kinds of resource are handled:
//
//   - Resources created by this update (absent before it) are deleted.
//   - Resources this update *replaced* are rolled back to the original: the
//     replacement that was created is deleted, and the original — still alive,
//     because replacement creates before deleting and defers the delete to the
//     post-success cleanup — is what the stack keeps. This is why a failed
//     replacement leaves the resource intact rather than destroying it.
//   - Resources successfully updated in place are passed back through their
//     resourceUpdater with the previous properties.
//   - Resources whose updater could not compensate an applied mutation, or
//     whose reverse update fails here, are retained as UPDATE_FAILED and make
//     the rollback fail truthfully.
//
// replacedBy maps a logical ID to the physical ID of the replacement created
// for it, for exactly that second case.
func (p *provisioner) rollbackUpdate(ctx context.Context, stack *Stack, attempted []StackResource, previous map[string]StackResource, replacedBy map[string]string, inPlaceUpdated, dirtyUpdates map[string]bool, rCtx *resolveContext, reason string) {
	stack.Status = StatusUpdateRollbackInProgress
	stack.StatusReason = reason
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusUpdateRollbackInProgress, reason)
	p.publishStackEvent(ctx, events.CFNStackFailed, stack)

	// Removing what the failed update created is its own reported phase, just
	// as it is on the success path.
	stack.Status = StatusUpdateRollbackCompleteCleanupInProgress
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack",
		StatusUpdateRollbackCompleteCleanupInProgress, reason)

	rollbackFailed := false
	var rollbackErr error
	// Track resources that were created during the failed update but could
	// not be deleted during rollback. Real CloudFormation keeps these in the
	// stack's resource list with status DELETE_FAILED so subsequent
	// operations can see them (instead of treating them as new and double-
	// creating against an orphaned service-side resource).
	var orphaned []StackResource
	dirtyResources := make(map[string]StackResource, len(dirtyUpdates))
	// Delete newly created resources in reverse order. Resources that existed
	// before the update (present in `previous`) are left untouched.
	for i := len(attempted) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			rollbackFailed = true
			break
		}
		r := attempted[i]

		// Replaced by this update: the replacement we created must go, and the
		// original — still alive, because replacement defers its delete to the
		// post-success cleanup — is what the stack rolls back to.
		//
		// Checked before `previous` deliberately: the caller removes entries
		// from that map as it processes them, so a resource updated earlier in
		// this run is no longer in it.
		if newPhysID, wasReplaced := replacedBy[r.LogicalID]; wasReplaced && newPhysID != "" {
			handler, ok := p.resolveHandler(r.Type)
			if !ok {
				continue
			}
			p.mu.Lock()
			router := p.router
			p.mu.Unlock()
			p.recordEvent(ctx, stack, r.LogicalID, newPhysID, r.Type, ResourceDeleteInProgress, "")
			if err := handler.Delete(ctx, router, p.cfg, newPhysID, rCtx); err != nil {
				p.log.Warn("cfn: update rollback: failed to delete replacement",
					zap.String("logicalId", r.LogicalID),
					zap.String("type", r.Type),
					zap.String("physicalId", newPhysID),
					zap.Error(err))
				p.recordEvent(ctx, stack, r.LogicalID, newPhysID, r.Type, ResourceDeleteFailed, err.Error())
				rollbackFailed = true
				continue
			}
			p.recordEvent(ctx, stack, r.LogicalID, newPhysID, r.Type, ResourceDeleteComplete, "")
			continue
		}

		if old, wasExisting := previous[r.LogicalID]; wasExisting {
			if dirtyUpdates[r.LogicalID] {
				rollbackFailed = true
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf(
					"restore %s: resource may still be mutated after failed compensation: %s",
					r.LogicalID, r.StatusReason))
				dirtyResources[r.LogicalID] = r
				continue
			}
			if inPlaceUpdated[r.LogicalID] && r.PhysicalID == old.PhysicalID {
				if err := p.rollbackInPlaceUpdate(ctx, stack, r, old, rCtx); err != nil {
					rollbackFailed = true
					rollbackErr = errors.Join(rollbackErr, err)
					r.Status = ResourceUpdateFailed
					r.StatusReason = err.Error()
					r.Timestamp = p.clk.Now()
					dirtyResources[r.LogicalID] = r
				}
			}
			continue
		}
		if !r.existsServiceSide() {
			continue
		}
		handler, ok := p.resolveHandler(r.Type)
		if !ok {
			continue
		}
		p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteInProgress, "")
		p.mu.Lock()
		router := p.router
		p.mu.Unlock()
		if err := handler.Delete(ctx, router, p.cfg, r.PhysicalID, rCtx); err != nil {
			p.log.Warn("cfn: update rollback: failed to delete new resource",
				zap.String("logicalId", r.LogicalID),
				zap.String("type", r.Type),
				zap.Error(err))
			p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteFailed, err.Error())
			rollbackFailed = true
			// Keep the resource in stack state so the next UpdateStack routes
			// through the update/skip path instead of provisioning fresh.
			r.Status = ResourceDeleteFailed
			r.StatusReason = err.Error()
			r.Timestamp = p.clk.Now()
			orphaned = append(orphaned, r)
		} else {
			p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteComplete, "")
			p.publishResourceEvent(ctx, events.CFNResourceDeleted, stack.StackName, r.LogicalID, r.Type, r.PhysicalID)
		}
	}

	// Restore the pre-update resource list, plus any orphaned resources that
	// could not be cleaned up. This matches real AWS: UPDATE_ROLLBACK leaves
	// failed-delete resources visible in the stack so they're not double-
	// created on the next attempt.
	restored := make([]StackResource, 0, len(previous)+len(orphaned))
	for logicalID, res := range previous {
		if dirty, ok := dirtyResources[logicalID]; ok {
			restored = append(restored, dirty)
			continue
		}
		restored = append(restored, res)
	}
	restored = append(restored, orphaned...)
	stack.Resources = restored
	stack.Tags = append([]Tag(nil), rCtx.PreviousStackTags...)

	if rollbackFailed {
		stack.Status = StatusUpdateRollbackFailed
		if rollbackErr != nil {
			stack.StatusReason = fmt.Sprintf("%s; rollback failed: %v", reason, rollbackErr)
		}
	} else {
		stack.Status = StatusUpdateRollbackComplete
		stack.StatusReason = ""
	}
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", stack.Status, stack.StatusReason)
	if err := p.flushCriticalState(ctx); err != nil {
		p.log.Warn("cfn: failed to flush update rollback state", zap.String("stack", stack.StackName), zap.Error(err))
	}
	p.publishStackEvent(ctx, events.CFNStackFailed, stack)
}

// rollbackInPlaceUpdate restores a resource that was successfully changed
// before a later resource failed. Reusing the resourceUpdater in reverse keeps
// validation, persistence, and service behavior out of CloudFormation.
func (p *provisioner) rollbackInPlaceUpdate(ctx context.Context, stack *Stack, attempted, previous StackResource, rCtx *resolveContext) error {
	p.recordEvent(ctx, stack, previous.LogicalID, previous.PhysicalID, previous.Type, ResourceUpdateInProgress, "Update rollback initiated")
	handler, ok := p.resolveHandler(previous.Type)
	if !ok {
		return p.failRollbackInPlaceUpdate(ctx, stack, previous, errors.New("resource handler is unavailable"))
	}
	updater, ok := handler.(resourceUpdater)
	if !ok {
		return p.failRollbackInPlaceUpdate(ctx, stack, previous, errors.New("resource handler cannot update in place"))
	}
	p.mu.Lock()
	router := p.router
	p.mu.Unlock()
	if router == nil {
		return p.failRollbackInPlaceUpdate(ctx, stack, previous, errors.New("router not initialised"))
	}

	rollbackCtx := *rCtx
	rollbackCtx.StackTags = append([]Tag(nil), rCtx.PreviousStackTags...)
	rollbackCtx.PreviousStackTags = append([]Tag(nil), rCtx.StackTags...)
	physicalID, _, err := updater.Update(ctx, router, p.cfg, previous.PhysicalID, previous.Properties, attempted.Properties, &rollbackCtx)
	if err == nil && physicalID != "" && physicalID != previous.PhysicalID {
		err = fmt.Errorf("rollback changed physical ID from %q to %q", previous.PhysicalID, physicalID)
	}
	if err == nil {
		if stabilizer, ok := handler.(resourceStabilizer); ok {
			err = stabilizer.Stabilize(ctx, router, p.cfg, p.clk, previous.PhysicalID, rCtx)
		}
	}
	if err != nil {
		return p.failRollbackInPlaceUpdate(ctx, stack, previous, err)
	}
	if rCtx.Attributes == nil {
		rCtx.Attributes = make(map[string]map[string]string)
	}
	rCtx.Attributes[previous.LogicalID] = previous.Attributes
	rCtx.Resources[previous.LogicalID] = previous.PhysicalID
	p.recordEvent(ctx, stack, previous.LogicalID, previous.PhysicalID, previous.Type, ResourceUpdateComplete, "")
	return nil
}

func (p *provisioner) failRollbackInPlaceUpdate(ctx context.Context, stack *Stack, previous StackResource, err error) error {
	rollbackErr := fmt.Errorf("restore %s: %w", previous.LogicalID, err)
	p.recordEvent(ctx, stack, previous.LogicalID, previous.PhysicalID, previous.Type, ResourceUpdateFailed, rollbackErr.Error())
	return rollbackErr
}

// ── Rollback stack (async) ─────────────────────────────────────────────────

// rollbackStack services an operator-requested RollbackStack, mirroring the
// async shape of createStack/updateStack so that a fast rollback is already
// terminal by the time the caller's next DescribeStacks lands.
//
// createPath selects the CREATE_FAILED → ROLLBACK_COMPLETE flow (unwind
// everything the failed create built) over the UPDATE_FAILED →
// UPDATE_ROLLBACK_COMPLETE flow.
func (p *provisioner) rollbackStack(stack *Stack, createPath bool) {
	done := make(chan struct{})
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer close(done)
		p.rollbackStackResources(stack, createPath)
	}()
	p.awaitBriefly(done)
}

// rollbackStackResources is the synchronous core of an explicit rollback.
func (p *provisioner) rollbackStackResources(stack *Stack, createPath bool) {
	ctx := p.regionCtx(stack.Region)

	// The stored template drives Ref/GetAtt resolution for any deletes. A
	// stack that failed early may have no usable template; an empty one still
	// yields a valid resolve context, and the resource list is the real source
	// of truth for what has to be retired.
	tmpl, err := parseTemplate(stack.TemplateBody)
	if err != nil || tmpl == nil {
		tmpl = &Template{Resources: map[string]TemplateResource{}}
	}
	rCtx := p.buildResolveContext(stack, tmpl)
	for _, r := range stack.Resources {
		if r.PhysicalID != "" {
			rCtx.Resources[r.LogicalID] = r.PhysicalID
		}
	}

	if createPath {
		// A failed create unwinds exactly like an automatic create rollback.
		p.rollbackCreate(ctx, stack, rCtx, "User Initiated")
		return
	}
	p.rollbackToStable(ctx, stack, rCtx, "User Initiated")
}

// rollbackToStable services RollbackStack for a stack whose update failed, or
// whose automatic update rollback failed and is being retried.
//
// Overcast keeps no snapshot of each resource's pre-update properties, so it
// cannot literally restore prior configuration the way real CloudFormation
// does. What it can do — and what unblocks a client stuck behind
// UPDATE_FAILED — is retire the resources the failed attempt left in a failed
// state and drive the stack to a terminal UPDATE_ROLLBACK_COMPLETE.
func (p *provisioner) rollbackToStable(ctx context.Context, stack *Stack, rCtx *resolveContext, reason string) {
	stack.Status = StatusUpdateRollbackInProgress
	stack.StatusReason = reason
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusUpdateRollbackInProgress, reason)
	p.publishStackEvent(ctx, events.CFNStackFailed, stack)

	rollbackFailed := false
	// Walk in reverse so deletes happen in reverse dependency order, then flip
	// the survivors back to template order at the end.
	kept := make([]StackResource, 0, len(stack.Resources))
	for i := len(stack.Resources) - 1; i >= 0; i-- {
		r := stack.Resources[i]
		if ctx.Err() != nil {
			rollbackFailed = true
			kept = append(kept, r)
			continue
		}

		switch r.Status {
		case ResourceCreateFailed:
			// The failed attempt half-created this resource. With no physical
			// resource behind it there is nothing to retire and it simply
			// leaves the stack; otherwise delete it.
			if r.PhysicalID == "" {
				continue
			}
			if err := p.deleteRollbackResource(ctx, stack, r, rCtx); err != nil {
				r.Status = ResourceDeleteFailed
				r.StatusReason = err.Error()
				r.Timestamp = p.clk.Now()
				kept = append(kept, r)
				rollbackFailed = true
			}
		case ResourceDeleteFailed:
			// Orphaned by an earlier automatic rollback that could not clean
			// up. Retrying that delete is the point of an explicit rollback.
			if err := p.deleteRollbackResource(ctx, stack, r, rCtx); err != nil {
				r.StatusReason = err.Error()
				r.Timestamp = p.clk.Now()
				kept = append(kept, r)
				rollbackFailed = true
			}
		case ResourceUpdateFailed:
			// The physical resource keeps whatever the failed update left on
			// it (see the method comment). Record the AWS-observable outcome —
			// the resource is back under the stack's last known stable state —
			// rather than leaving a failed status that blocks future updates.
			r.Status = ResourceUpdateComplete
			r.StatusReason = ""
			r.Timestamp = p.clk.Now()
			p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceUpdateComplete, "")
			kept = append(kept, r)
		default:
			kept = append(kept, r)
		}
	}
	slices.Reverse(kept)
	stack.Resources = kept

	if rollbackFailed {
		stack.Status = StatusUpdateRollbackFailed
	} else {
		stack.Status = StatusUpdateRollbackComplete
		stack.StatusReason = ""
	}
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", stack.Status, stack.StatusReason)
	if err := p.flushCriticalState(ctx); err != nil {
		p.log.Warn("cfn: failed to flush rollback state", zap.String("stack", stack.StackName), zap.Error(err))
	}
	p.publishStackEvent(ctx, events.CFNStackFailed, stack)
}

// deleteRollbackResource deletes one resource as part of an explicit rollback,
// emitting the DELETE_IN_PROGRESS / DELETE_COMPLETE / DELETE_FAILED events that
// DescribeStackEvents surfaces. A resource type with no registered handler is
// treated as already gone — the same allowance rollbackCreate makes.
func (p *provisioner) deleteRollbackResource(ctx context.Context, stack *Stack, r StackResource, rCtx *resolveContext) error {
	p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteInProgress, "")

	handler, ok := p.resolveHandler(r.Type)
	if !ok {
		p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteComplete, "")
		return nil
	}

	p.mu.Lock()
	router := p.router
	p.mu.Unlock()

	if err := handler.Delete(ctx, router, p.cfg, r.PhysicalID, rCtx); err != nil {
		p.log.Warn("cfn: rollback: failed to delete resource",
			zap.String("logicalId", r.LogicalID),
			zap.String("type", r.Type),
			zap.Error(err))
		p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteFailed, err.Error())
		return err
	}

	p.recordEvent(ctx, stack, r.LogicalID, r.PhysicalID, r.Type, ResourceDeleteComplete, "")
	p.publishResourceEvent(ctx, events.CFNResourceDeleted, stack.StackName, r.LogicalID, r.Type, r.PhysicalID)
	return nil
}

func (p *provisioner) publishStackEvent(ctx context.Context, t events.Type, stack *Stack) {
	p.mu.Lock()
	bus := p.bus
	p.mu.Unlock()
	if bus == nil {
		return
	}
	bus.Publish(ctx, events.Event{
		Type:    t,
		Source:  "cloudformation",
		Payload: events.CFNStackPayload{StackName: stack.StackName, Status: stack.Status},
	})
}

func (p *provisioner) publishResourceEvent(ctx context.Context, t events.Type, stackName, logicalID, resType, physicalID string) {
	p.mu.Lock()
	bus := p.bus
	p.mu.Unlock()
	if bus == nil {
		return
	}
	bus.Publish(ctx, events.Event{
		Type:   t,
		Source: "cloudformation",
		Payload: events.CFNResourcePayload{
			StackName:         stackName,
			LogicalResourceID: logicalID,
			ResourceType:      resType,
			PhysicalID:        physicalID,
		},
	})
}

// recordEvent appends an immutable lifecycle event to the stack's separate
// event store and persists the current stack metadata. All provisioning state
// transitions call this so that DescribeStackEvents always returns accurate,
// ordered history without embedding the growing event list in the stack blob.
func (p *provisioner) recordEvent(ctx context.Context, stack *Stack, logicalID, physicalID, resType, status, reason string) {
	event := StackEvent{
		EventID:              uuid.New().String(),
		StackID:              stack.StackID,
		StackName:            stack.StackName,
		LogicalResourceID:    logicalID,
		PhysicalResourceID:   physicalID,
		ResourceType:         resType,
		ResourceStatus:       status,
		ResourceStatusReason: reason,
		Timestamp:            p.clk.Now(),
	}
	if err := p.store.appendStackEvent(ctx, stack.StackName, event); err != nil {
		p.log.Error("cfn: failed to persist stack event", zap.Error(err))
	}
	if err := p.store.putStack(ctx, stack); err != nil {
		p.log.Error("cfn: failed to persist stack state", zap.Error(err))
	}
}

// evaluateConditions evaluates template conditions against parameters.
// For simplicity, we support Fn::Equals only (the most common condition).
func evaluateConditions(conditions map[string]any, params map[string]string) map[string]bool {
	result := make(map[string]bool, len(conditions))
	for name, cond := range conditions {
		result[name] = evalCondition(cond, params)
	}
	return result
}

func evalCondition(cond any, params map[string]string) bool {
	m, ok := cond.(map[string]any)
	if !ok {
		return false
	}
	if eq, ok := m["Fn::Equals"]; ok {
		arr, ok := eq.([]any)
		if !ok || len(arr) != 2 {
			return false
		}
		a := resolveConditionValue(arr[0], params)
		b := resolveConditionValue(arr[1], params)
		return a == b
	}
	if not, ok := m["Fn::Not"]; ok {
		arr, ok := not.([]any)
		if !ok || len(arr) != 1 {
			return false
		}
		return !evalCondition(arr[0], params)
	}
	if and, ok := m["Fn::And"]; ok {
		arr, ok := and.([]any)
		if !ok {
			return false
		}
		for _, item := range arr {
			if !evalCondition(item, params) {
				return false
			}
		}
		return true
	}
	if or, ok := m["Fn::Or"]; ok {
		arr, ok := or.([]any)
		if !ok {
			return false
		}
		for _, item := range arr {
			if evalCondition(item, params) {
				return true
			}
		}
		return false
	}
	return false
}

func resolveConditionValue(v any, params map[string]string) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]any:
		if ref, ok := val["Ref"]; ok {
			name, _ := ref.(string)
			if p, ok := params[name]; ok {
				return p
			}
			return name
		}
	}
	return cfnScalarString(v)
}

// ── Topology sort ──────────────────────────────────────────────────────────

// topoSort returns a topological ordering of resources respecting DependsOn.
// implicitResourceDeps scans a property value tree and returns all logical
// resource IDs referenced via Ref, Fn::GetAtt, or Fn::Sub.  These create
// implicit dependency edges in real AWS CloudFormation — no explicit DependsOn
// is required when a resource references another via an intrinsic function.
func implicitResourceDeps(v any, resourceNames map[string]struct{}) []string {
	seen := map[string]struct{}{}
	collectResourceRefs(v, resourceNames, seen)
	result := make([]string, 0, len(seen))
	for k := range seen {
		result = append(result, k)
	}
	return result
}

func collectResourceRefs(v any, resourceNames map[string]struct{}, seen map[string]struct{}) {
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 1 {
			// Ref
			if ref, ok := val["Ref"]; ok {
				if name, ok := ref.(string); ok {
					if _, is := resourceNames[name]; is {
						seen[name] = struct{}{}
					}
				}
				return
			}
			// Fn::GetAtt
			if ga, ok := val["Fn::GetAtt"]; ok {
				switch g := ga.(type) {
				case []any:
					if len(g) >= 1 {
						if name, ok := g[0].(string); ok {
							if _, is := resourceNames[name]; is {
								seen[name] = struct{}{}
							}
						}
					}
				case string:
					// "LogicalId.Attribute" form
					if dot := strings.IndexByte(g, '.'); dot > 0 {
						name := g[:dot]
						if _, is := resourceNames[name]; is {
							seen[name] = struct{}{}
						}
					}
				}
				return
			}
			// Fn::Sub
			if sub, ok := val["Fn::Sub"]; ok {
				collectSubResourceRefs(sub, resourceNames, seen)
				return
			}
		}
		// Not an intrinsic — recurse into all child values.
		for _, child := range val {
			collectResourceRefs(child, resourceNames, seen)
		}
	case []any:
		for _, item := range val {
			collectResourceRefs(item, resourceNames, seen)
		}
	}
}

// collectSubResourceRefs extracts resource logical IDs from an Fn::Sub value.
// It handles both the string form ("${LogicalId.Attr}") and the
// [string, {vars}] form, and also recurses into the variable-map values.
func collectSubResourceRefs(sub any, resourceNames map[string]struct{}, seen map[string]struct{}) {
	var tmplStr string
	switch val := sub.(type) {
	case string:
		tmplStr = val
	case []any:
		if len(val) >= 1 {
			if s, ok := val[0].(string); ok {
				tmplStr = s
			}
		}
		if len(val) >= 2 {
			// Variable map may itself contain intrinsics.
			collectResourceRefs(val[1], resourceNames, seen)
		}
	default:
		return
	}

	// Scan for ${VarName} and ${VarName.Attr} patterns.
	for {
		start := strings.Index(tmplStr, "${")
		if start < 0 {
			break
		}
		end := strings.Index(tmplStr[start:], "}")
		if end < 0 {
			break
		}
		varName := tmplStr[start+2 : start+end]
		// Strip .Attribute suffix if present.
		if dot := strings.IndexByte(varName, '.'); dot >= 0 {
			varName = varName[:dot]
		}
		if _, is := resourceNames[varName]; is {
			seen[varName] = struct{}{}
		}
		tmplStr = tmplStr[start+end+1:]
	}
}

func topoSort(resources map[string]TemplateResource) ([]string, error) {
	// Build a set of all resource logical IDs so we can distinguish resource
	// references from parameter/pseudo-parameter references in intrinsics.
	resourceNames := make(map[string]struct{}, len(resources))
	for name := range resources {
		resourceNames[name] = struct{}{}
	}

	deps := make(map[string][]string, len(resources))
	for name, res := range resources {
		explicit := parseDependsOn(res.DependsOn)
		implicit := implicitResourceDeps(res.Properties, resourceNames)

		// Merge, deduplicating, removing any self-reference.
		merged := make(map[string]struct{}, len(explicit)+len(implicit))
		for _, d := range explicit {
			if d != name {
				merged[d] = struct{}{}
			}
		}
		for _, d := range implicit {
			if d != name {
				merged[d] = struct{}{}
			}
		}
		all := make([]string, 0, len(merged))
		for d := range merged {
			all = append(all, d)
		}
		deps[name] = all
	}

	var result []string
	visited := make(map[string]int) // 0=unvisited, 1=visiting, 2=visited

	var visit func(string) error
	visit = func(name string) error {
		switch visited[name] {
		case 1:
			return fmt.Errorf("cycle at %s", name)
		case 2:
			return nil
		}
		visited[name] = 1
		for _, dep := range deps[name] {
			if _, ok := resources[dep]; !ok {
				continue // ignore missing deps
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		visited[name] = 2
		result = append(result, name)
		return nil
	}

	// Sort keys for deterministic order.
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func parseDependsOn(v any) []string {
	switch val := v.(type) {
	case string:
		if val != "" {
			return []string{val}
		}
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return val
	}
	return nil
}

// ── Resource handlers ──────────────────────────────────────────────────────

// resourceHandler defines how to create and delete a specific AWS resource type.
type resourceHandler interface {
	Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (physicalID string, attrs map[string]string, err error)
	Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error
}

// resourceUpdater is implemented by resource handlers that support in-place
// updates (e.g. Lambda function code/configuration). Handlers that do not
// implement this interface fall back to delete + create on UpdateStack.
type resourceUpdater interface {
	Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (newPhysicalID string, attrs map[string]string, err error)
}

// resourceStabilizer is implemented by resource handlers whose resource is not
// finished when the call that created or changed it returns. Real
// CloudFormation's resource-provider contract has the same split: a create
// reports IN_PROGRESS and is re-invoked until the resource settles, and nothing
// downstream of it runs until it does. An RDS DB instance reaching "available"
// and an ECS service reaching its desired count are what this exists for — in
// both cases the service API is asynchronous by design and answers long before
// the database or the container is usable.
//
// The provisioner calls Stabilize after Create and after a successful in-place
// Update so that a handler cannot implement one and forget the other, and so
// that the three rules a failed stabilization has to obey live in one place
// rather than in each handler:
//
//   - The resource is real. Create named it before the wait began, so the
//     physical ID travels out with the error and rollback deletes it. Dropping
//     it strands a database that nothing is left holding the name of.
//   - A failed update is terminal, never answered with a replacement. The
//     change was applied to the resource that exists; building a second one
//     fixes nothing (see updateFailure).
//   - The wait ends when the provisioner's context does, so a stuck resource
//     cannot outlive a shutdown.
//
// Stabilize must be safe to call on a resource that is already settled: the
// first status read may find that the service completed the work immediately.
type resourceStabilizer interface {
	Stabilize(ctx context.Context, router http.Handler, cfg *config.Config, clk clock.Clock, physicalID string, rCtx *resolveContext) error
}

// errReplacementRequired is returned by an Update implementation when one or
// more changed properties cannot be applied in place (for example renaming an
// SQS queue or changing a DynamoDB key schema). The provisioner reacts by
// taking the replacement path — create the new resource and delete the old
// one — which mirrors how AWS CloudFormation handles "Replacement: Yes"
// property changes.
var errReplacementRequired = errors.New("cfn: replacement required")

// errDeletionBlocked is returned by a Delete implementation when the resource
// itself refuses to be deleted — today only an RDS cluster with
// DeletionProtection enabled. The provisioner reacts by failing the stack
// operation instead of reporting a deletion that did not happen, which is what
// AWS does: the delete fails, the resource survives, and the operator clears
// the block and tries again.
//
// It is a sentinel rather than "any error fails the delete" on purpose. Every
// other handler swallows its own teardown errors so that a resource which is
// already gone cannot wedge a stack teardown, and that is the behaviour worth
// keeping — a resource refusing deletion is a different thing from a resource
// that could not be reached.
var errDeletionBlocked = errors.New("cfn: deletion blocked by the resource")

// updateFailure marks an update error the provisioner must answer by failing
// the resource rather than by replacing it. Its dirty bit distinguishes a
// rejected update from one whose handler could not compensate an already
// applied mutation. The default for an update error is
// replacement, which is right when the update could not be applied — but wrong
// once it has been: a resource that applied its update and then failed to
// settle is not fixed by building a second copy of it, and CloudFormation does
// not try. An ECS service whose new deployment cannot start its tasks is the
// case this exists for; replacing it would create a second service under a
// generated name and delete the one that works.
type updateFailure struct {
	err   error
	dirty bool
}

func (e updateFailure) Error() string { return e.err.Error() }
func (e updateFailure) Unwrap() error { return e.err }

// failUpdate marks err as terminal for the resource — see updateFailure.
func failUpdate(err error) error { return updateFailure{err: err} }

// failDirtyUpdate marks a terminal update whose handler could not compensate
// a mutation it had already applied. Stack rollback must retain that resource
// as failed instead of claiming the previous state was restored.
func failDirtyUpdate(err error) error { return updateFailure{err: err, dirty: true} }

func isDirtyUpdateFailure(err error) bool {
	var failed updateFailure
	return errors.As(err, &failed) && failed.dirty
}

// asBool coerces a CFN property value (which may be a real bool, a string
// "true"/"false", or nil) to a boolean. Used in handler Update methods to
// detect immutable property changes.
func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true")
	default:
		return false
	}
}

// resourceHandlers maps CloudFormation resource types to their handlers.
var resourceHandlers = map[string]resourceHandler{
	// SQS
	"AWS::SQS::Queue":       &sqsQueueHandler{},
	"AWS::SQS::QueuePolicy": &stubResourceHandler{},
	// SNS
	"AWS::SNS::Topic":        &snsTopicHandler{},
	"AWS::SNS::Subscription": &snsSubscriptionHandler{},
	// S3
	"AWS::S3::Bucket":       &s3BucketHandler{},
	"AWS::S3::BucketPolicy": &s3BucketPolicyHandler{},
	// DynamoDB
	"AWS::DynamoDB::Table": &dynamodbTableHandler{},
	// Lambda
	"AWS::Lambda::Function":           &lambdaFunctionHandler{},
	"AWS::Lambda::Alias":              &lambdaAliasHandler{},
	"AWS::Lambda::Url":                &lambdaUrlHandler{},
	"AWS::Lambda::EventSourceMapping": &lambdaEventSourceMappingHandler{},
	"AWS::Lambda::Permission":         &lambdaPermissionHandler{},
	"AWS::Lambda::LayerVersion":       &lambdaLayerVersionHandler{},
	"AWS::Lambda::CodeSigningConfig":  &lambdaCodeSigningConfigHandler{},
	// IAM
	"AWS::IAM::Role":              &iamRoleHandler{},
	"AWS::IAM::Policy":            &iamPolicyHandler{},
	"AWS::IAM::ManagedPolicy":     &iamManagedPolicyHandler{},
	"AWS::IAM::InstanceProfile":   &iamInstanceProfileHandler{},
	"AWS::IAM::ServiceLinkedRole": &iamServiceLinkedRoleHandler{},
	// CloudWatch Logs
	"AWS::Logs::LogGroup":  &logsLogGroupHandler{},
	"AWS::Logs::LogStream": &logsLogStreamHandler{},
	// SSM
	"AWS::SSM::Parameter": &ssmParameterHandler{},
	// Secrets Manager
	"AWS::SecretsManager::Secret": &secretsManagerSecretHandler{},
	// KMS
	"AWS::KMS::Key":   &kmsKeyHandler{},
	"AWS::KMS::Alias": &kmsAliasHandler{},
	// CDK metadata
	"AWS::CDK::Metadata":                       &stubResourceHandler{},
	"AWS::CloudFormation::WaitConditionHandle": &stubResourceHandler{},
	"AWS::CloudFormation::WaitCondition":       &stubResourceHandler{},
	// EC2
	"AWS::EC2::VPC":                         &ec2VPCHandler{},
	"AWS::EC2::Subnet":                      &ec2SubnetHandler{},
	"AWS::EC2::SecurityGroup":               &ec2SecurityGroupHandler{},
	"AWS::EC2::InternetGateway":             &ec2InternetGatewayHandler{},
	"AWS::EC2::VPNGateway":                  &ec2VPNGatewayHandler{},
	"AWS::EC2::VPCGatewayAttachment":        &ec2VPCGatewayAttachmentHandler{},
	"AWS::EC2::RouteTable":                  &ec2RouteTableHandler{},
	"AWS::EC2::Route":                       &ec2RouteHandler{},
	"AWS::EC2::SubnetRouteTableAssociation": &ec2SubnetRouteTableAssociationHandler{},
	"AWS::EC2::NatGateway":                  &ec2NatGatewayHandler{},
	"AWS::EC2::EIP":                         &ec2EIPHandler{},
	// Step Functions
	"AWS::StepFunctions::StateMachine": &sfnStateMachineHandler{},
	// EventBridge
	"AWS::Events::Rule":     &eventsRuleHandler{},
	"AWS::Events::EventBus": &eventsEventBusHandler{},
	// API Gateway
	"AWS::ApiGateway::RestApi":       &apigwRestApiHandler{},
	"AWS::ApiGateway::Resource":      &apigwResourceHandler{},
	"AWS::ApiGateway::Method":        &apigwMethodHandler{},
	"AWS::ApiGateway::Deployment":    &apigwDeploymentHandler{},
	"AWS::ApiGateway::Stage":         &apigwStageHandler{},
	"AWS::ApiGateway::Account":       &stubResourceHandler{},
	"AWS::ApiGateway::ApiKey":        &apigwApiKeyHandler{},
	"AWS::ApiGateway::UsagePlan":     &apigwUsagePlanHandler{},
	"AWS::ApiGateway::UsagePlanKey":  &apigwUsagePlanKeyHandler{},
	"AWS::ApiGatewayV2::Api":         &apigwV2ApiHandler{},
	"AWS::ApiGatewayV2::Stage":       &apigwV2StageHandler{},
	"AWS::ApiGatewayV2::Integration": &apigwV2IntegrationHandler{},
	"AWS::ApiGatewayV2::Route":       &apigwV2RouteHandler{},
	// ECS
	"AWS::ECS::Cluster":        &ecsClusterHandler{},
	"AWS::ECS::TaskDefinition": &ecsTaskDefinitionHandler{},
	"AWS::ECS::Service":        &ecsServiceHandler{},
	// Service Catalog AppRegistry
	"AWS::ServiceCatalogAppRegistry::Application":         &appregistryApplicationHandler{},
	"AWS::ServiceCatalogAppRegistry::ResourceAssociation": &appregistryResourceAssociationHandler{},
	// RDS
	"AWS::RDS::DBInstance":       &rdsDBInstanceHandler{},
	"AWS::RDS::DBCluster":        &rdsDBClusterHandler{},
	"AWS::RDS::DBSubnetGroup":    &rdsDBSubnetGroupHandler{},
	"AWS::RDS::DBParameterGroup": &rdsDBParameterGroupHandler{},
	// Kinesis
	"AWS::Kinesis::Stream": &kinesisStreamHandler{},
	// Cognito
	"AWS::Cognito::UserPool":       &cognitoUserPoolHandler{},
	"AWS::Cognito::UserPoolClient": &cognitoUserPoolClientHandler{},
	// AppSync
	"AWS::AppSync::Api":                      &appsyncEventsApiHandler{},
	"AWS::AppSync::GraphQLApi":               &appsyncGraphQLApiHandler{},
	"AWS::AppSync::GraphQLSchema":            &appsyncGraphQLSchemaHandler{},
	"AWS::AppSync::ChannelNamespace":         &appsyncChannelNamespaceHandler{},
	"AWS::AppSync::ApiKey":                   &appsyncApiKeyHandler{},
	"AWS::AppSync::DataSource":               &appsyncDataSourceHandler{},
	"AWS::AppSync::Resolver":                 &appsyncResolverHandler{},
	"AWS::AppSync::FunctionConfiguration":    &appsyncFunctionConfigurationHandler{},
	"AWS::AppSync::DomainName":               &appsyncDomainNameHandler{},
	"AWS::AppSync::DomainNameApiAssociation": &appsyncDomainNameApiAssociationHandler{},
	"AWS::AppSync::ApiCache":                 &appsyncApiCacheHandler{},
	"AWS::AppSync::SourceApiAssociation":     &appsyncSourceApiAssociationHandler{},
	// ElastiCache
	"AWS::ElastiCache::CacheCluster":     &elastiCacheCacheClusterHandler{},
	"AWS::ElastiCache::ServerlessCache":  &elastiCacheServerlessCacheHandler{},
	"AWS::ElastiCache::ReplicationGroup": &elastiCacheReplicationGroupHandler{},
	"AWS::ElastiCache::SubnetGroup":      &elastiCacheSubnetGroupHandler{},
	"AWS::ElastiCache::ParameterGroup":   &stubResourceHandler{},
	// CloudFront
	"AWS::CloudFront::Distribution": &cloudfrontDistributionHandler{},
	// SES
	"AWS::SES::Template":         &sesTemplateHandler{},
	"AWS::SES::ConfigurationSet": &stubResourceHandler{},
	// Certificate Manager
	"AWS::CertificateManager::Certificate": &acmCertificateHandler{},
	// ECR
	"AWS::ECR::Repository": &ecrRepositoryHandler{},
	// CloudTrail
	"AWS::CloudTrail::Trail": &cloudtrailTrailHandler{},
	// Backup
	"AWS::Backup::BackupVault": &backupBackupVaultHandler{},
	"AWS::Backup::BackupPlan":  &backupBackupPlanHandler{},
	// Transfer
	"AWS::Transfer::Server": &transferServerHandler{},
	"AWS::Transfer::User":   &transferUserHandler{},
	// EFS
	"AWS::EFS::FileSystem":  &efsFileSystemHandler{},
	"AWS::EFS::MountTarget": &efsMountTargetHandler{},
	"AWS::EFS::AccessPoint": &efsAccessPointHandler{},
	// Shield
	"AWS::Shield::Protection": &shieldProtectionHandler{},
	// Firehose
	"AWS::KinesisFirehose::DeliveryStream": &firehoseDeliveryStreamHandler{},
	// Athena
	"AWS::Athena::WorkGroup": &athenaWorkGroupHandler{},
	// Glue
	"AWS::Glue::Database": &glueDatabaseHandler{},
	"AWS::Glue::Table":    &glueTableHandler{},
	// CloudWatch
	"AWS::CloudWatch::Alarm": &cloudwatchAlarmHandler{},
	// EventBridge
	"AWS::Events::Connection": &stubResourceHandler{},
	// Scheduler
	"AWS::Scheduler::Schedule":      &schedulerScheduleHandler{},
	"AWS::Scheduler::ScheduleGroup": &schedulerScheduleGroupHandler{},
	// OpenSearch
	"AWS::OpenSearchService::Domain": &opensearchDomainHandler{},
	// AppConfig
	"AWS::AppConfig::Application":          &appconfigApplicationHandler{},
	"AWS::AppConfig::Environment":          &appconfigEnvironmentHandler{},
	"AWS::AppConfig::ConfigurationProfile": &appconfigConfigurationProfileHandler{},
	// ELBv2
	"AWS::ElasticLoadBalancingV2::LoadBalancer": &elbv2LoadBalancerHandler{},
	"AWS::ElasticLoadBalancingV2::TargetGroup":  &elbv2TargetGroupHandler{},
	"AWS::ElasticLoadBalancingV2::Listener":     &elbv2ListenerHandler{},
	// Auto Scaling
	"AWS::AutoScaling::AutoScalingGroup":    &autoscalingASGHandler{},
	"AWS::AutoScaling::LaunchConfiguration": &autoscalingLaunchConfigHandler{},
	// Route53
	"AWS::Route53::HostedZone":  &route53HostedZoneHandler{},
	"AWS::Route53::RecordSet":   &route53RecordSetHandler{},
	"AWS::Route53::HealthCheck": &route53HealthCheckHandler{},
	// EKS
	"AWS::EKS::Cluster":                &eksClusterHandler{},
	"AWS::EKS::Nodegroup":              &eksNodegroupHandler{},
	"AWS::EKS::FargateProfile":         &eksFargateProfileHandler{},
	"AWS::EKS::Addon":                  &eksAddonHandler{},
	"AWS::EKS::AccessEntry":            &eksAccessEntryHandler{},
	"AWS::EKS::PodIdentityAssociation": &eksPodIdentityAssociationHandler{},
	// MSK
	"AWS::MSK::Cluster":       &mskClusterHandler{},
	"AWS::MSK::Configuration": &mskConfigurationHandler{},
	// Pipes
	"AWS::Pipes::Pipe": &pipesPipeHandler{},
	// IAM (additional)
	"AWS::IAM::User":      &iamUserHandler{},
	"AWS::IAM::AccessKey": &iamAccessKeyHandler{},
	// WAFv2
	"AWS::WAFv2::WebACL": &wafv2WebACLHandler{},
	// API Gateway V2 (additional)
	"AWS::ApiGatewayV2::Deployment": &stubResourceHandler{},
	// DynamoDB (additional)
	"AWS::DynamoDB::GlobalTable": &stubResourceHandler{},
}

// ── Stub resource handler ──────────────────────────────────────────────────

// stubResourceHandler generates a fake physical ID and does nothing on delete.
type stubResourceHandler struct{}

func (h *stubResourceHandler) Create(_ context.Context, _ http.Handler, _ *config.Config, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return fmt.Sprintf("stub-%s-%d", rCtx.StackName, len(rCtx.Resources)), nil, nil
}

func (h *stubResourceHandler) Delete(_ context.Context, _ http.Handler, _ *config.Config, _ string, _ *resolveContext) error {
	return nil
}

// ── Concrete resource handlers ─────────────────────────────────────────────

// internalRequest dispatches an HTTP request to the emulator router.
// The region parameter is forwarded via X-Overcast-Region so that services
// build ARNs in the correct region.
func internalRequest(ctx context.Context, router http.Handler, region, method, path, contentType string, body []byte) (*httptest.ResponseRecorder, error) {
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if region != "" {
		req.Header.Set("X-Overcast-Region", region)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return rec, fmt.Errorf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	return rec, nil
}

// internalJSON dispatches a JSON POST with X-Amz-Target header.
func internalJSON(ctx context.Context, router http.Handler, region, target string, body any) (*httptest.ResponseRecorder, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)
	if region != "" {
		req.Header.Set("X-Overcast-Region", region)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return rec, fmt.Errorf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	return rec, nil
}

// internalQuery dispatches a Query-protocol POST.
func internalQuery(ctx context.Context, router http.Handler, region string, params map[string]string) (*httptest.ResponseRecorder, error) {
	form := make(url.Values, len(params))
	for k, v := range params {
		form.Set(k, v)
	}
	body := form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if region != "" {
		req.Header.Set("X-Overcast-Region", region)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return rec, fmt.Errorf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	return rec, nil
}

// ── SQS Queue handler ─────────────────────────────────────────────────────

type sqsQueueHandler struct{}

// cfnFloatProp reads a numeric CFN property, which may arrive as float64,
// json.Number, or the string form a raw template is free to write.
func cfnFloatProp(props map[string]any, name string) (float64, bool) {
	v, ok := props[name]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func cfnScalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func sqsQueueAttributesFromProps(props map[string]any) map[string]string {
	attrs := make(map[string]string)
	for _, name := range []string{
		"VisibilityTimeout",
		"MessageRetentionPeriod",
		"DelaySeconds",
		"MaximumMessageSize",
		"ReceiveMessageWaitTimeSeconds",
		"ContentBasedDeduplication",
		"DeduplicationScope",
		"FifoQueue",
		"FifoThroughputLimit",
		"KmsDataKeyReusePeriodSeconds",
		"KmsMasterKeyId",
		"SqsManagedSseEnabled",
	} {
		if v, ok := props[name]; ok {
			attrs[name] = cfnScalarString(v)
		}
	}
	if rp, ok := props["RedrivePolicy"]; ok {
		if b, err := json.Marshal(rp); err == nil {
			attrs["RedrivePolicy"] = string(b)
		}
	}
	if raw, ok := props["Attributes"].(map[string]any); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				attrs[k] = s
			}
		}
	}
	return attrs
}

func (h *sqsQueueHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Extract current queue name from ARN (last segment).
	oldName := physicalID
	if parts := strings.Split(physicalID, ":"); len(parts) > 0 {
		oldName = parts[len(parts)-1]
	}
	// QueueName is immutable in AWS — request replacement.
	if n, ok := props["QueueName"].(string); ok && n != "" && n != oldName {
		return "", nil, errReplacementRequired
	}
	// FIFO ↔ standard transitions also require replacement.
	if _, ok := props["FifoQueue"]; ok {
		if strings.HasSuffix(oldName, ".fifo") != asBool(props["FifoQueue"]) {
			return "", nil, errReplacementRequired
		}
	}

	attrs := sqsQueueAttributesFromProps(props)

	if len(attrs) > 0 {
		queueURL := fmt.Sprintf("%s/%s/%s", cfg.ExternalBaseURL(), cfg.AccountID, oldName)
		body := map[string]any{"QueueUrl": queueURL, "Attributes": attrs}
		if _, err := internalJSON(ctx, router, rCtx.Region, "AmazonSQS.SetQueueAttributes", body); err != nil {
			return "", nil, fmt.Errorf("sqs SetQueueAttributes: %w", err)
		}
	}
	arn := protocol.ARN(rCtx.Region, cfg.AccountID, "sqs", oldName)
	queueURL := fmt.Sprintf("%s/%s/%s", cfg.ExternalBaseURL(), cfg.AccountID, oldName)
	return arn, map[string]string{"Ref": queueURL, "QueueName": oldName, "Arn": arn, "QueueUrl": queueURL}, nil
}

func (h *sqsQueueHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	queueName, _ := props["QueueName"].(string)
	if queueName == "" {
		queueName = rCtx.generatedName()
		if asBool(props["FifoQueue"]) {
			queueName += ".fifo"
		}
	}

	body := map[string]any{
		"QueueName": queueName,
	}

	attrs := sqsQueueAttributesFromProps(props)
	if len(attrs) > 0 {
		body["Attributes"] = attrs
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AmazonSQS.CreateQueue", body)
	if err != nil {
		return "", nil, fmt.Errorf("sqs CreateQueue: %w", err)
	}

	// Extract QueueUrl from JSON response.
	var resp struct {
		QueueUrl string `json:"QueueUrl"`
	}
	arn := protocol.ARN(rCtx.Region, cfg.AccountID, "sqs", queueName)
	queueURL := ""
	if json.Unmarshal(rec.Body.Bytes(), &resp) == nil && resp.QueueUrl != "" {
		queueURL = resp.QueueUrl
	}
	cfnAttrs := map[string]string{
		"Ref":       queueURL,
		"QueueName": queueName,
		"Arn":       arn,
		"QueueUrl":  queueURL,
	}
	return arn, cfnAttrs, nil
}

func (h *sqsQueueHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Extract queue name from ARN.
	name := physicalID
	if parts := strings.Split(physicalID, ":"); len(parts) > 0 {
		name = parts[len(parts)-1]
	}
	queueURL := fmt.Sprintf("%s/%s/%s", cfg.ExternalBaseURL(), cfg.AccountID, name)
	body := map[string]any{
		"QueueUrl": queueURL,
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AmazonSQS.DeleteQueue", body)
	return nil
}

// ── SNS Topic handler ──────────────────────────────────────────────────────

type snsAttribute struct {
	name  string
	value string
}

// snsAttributesFromProps translates CloudFormation's typed property values to
// the strings accepted by SNS's Set*Attributes APIs. It owns no SNS behavior:
// validation, defaults, and persistence remain in the SNS service.
func snsAttributesFromProps(props map[string]any, scalarNames, jsonNames []string) ([]snsAttribute, error) {
	attrs := make([]snsAttribute, 0, len(scalarNames)+len(jsonNames))
	for _, name := range scalarNames {
		if value, ok := props[name]; ok && value != nil {
			attrs = append(attrs, snsAttribute{name: name, value: cfnScalarString(value)})
		}
	}
	for _, name := range jsonNames {
		if value, ok := props[name]; ok && value != nil {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("marshal SNS attribute %s: %w", name, err)
			}
			attrs = append(attrs, snsAttribute{name: name, value: string(encoded)})
		}
	}
	return attrs, nil
}

func snsTopicAttributesFromProps(props map[string]any) ([]snsAttribute, error) {
	return snsAttributesFromProps(props,
		[]string{"ContentBasedDeduplication", "DisplayName", "FifoTopic", "FifoThroughputScope", "KmsMasterKeyId", "SignatureVersion", "TracingConfig"},
		[]string{"ArchivePolicy", "DataProtectionPolicy", "DeliveryPolicy"},
	)
}

func snsSubscriptionAttributesFromProps(props map[string]any) ([]snsAttribute, error) {
	return snsAttributesFromProps(props,
		[]string{"FilterPolicyScope", "RawMessageDelivery", "SubscriptionRoleArn"},
		[]string{"DeliveryPolicy", "FilterPolicy", "RedrivePolicy", "ReplayPolicy"},
	)
}

func snsRemovedAttribute(props, oldProps map[string]any, names []string) string {
	for _, name := range names {
		oldValue, hadOldValue := oldProps[name]
		if !hadOldValue || oldValue == nil {
			continue
		}
		newValue, hasNewValue := props[name]
		if !hasNewValue || newValue == nil {
			return name
		}
	}
	return ""
}

func cfnPropertyChanged(props, oldProps map[string]any, name string) (bool, error) {
	oldValue, err := json.Marshal(oldProps[name])
	if err != nil {
		return false, fmt.Errorf("marshal previous CloudFormation property %s: %w", name, err)
	}
	newValue, err := json.Marshal(props[name])
	if err != nil {
		return false, fmt.Errorf("marshal CloudFormation property %s: %w", name, err)
	}
	return !bytes.Equal(oldValue, newValue), nil
}

func applySNSAttributes(ctx context.Context, router http.Handler, region, action, resourceParam, resourceARN string, attrs []snsAttribute) error {
	for _, attr := range attrs {
		params := map[string]string{
			"Action":         action,
			resourceParam:    resourceARN,
			"AttributeName":  attr.name,
			"AttributeValue": attr.value,
			"Version":        "2010-03-31",
		}
		if _, err := internalQuery(ctx, router, region, params); err != nil {
			return fmt.Errorf("sns %s(%s): %w", action, attr.name, err)
		}
	}
	return nil
}

// applySNSTopicTags deliberately dispatches to SNS rather than treating tags
// as CloudFormation metadata. TagResource is not implemented by SNS yet, so a
// template that uses non-empty Tags fails through the service instead of
// deploying a topic with silently discarded configuration.
func applySNSTopicTags(ctx context.Context, router http.Handler, region, topicARN string, props map[string]any) error {
	rawTags, ok := props["Tags"]
	if !ok || rawTags == nil {
		return nil
	}
	params, err := snsTopicTagParams(topicARN, rawTags)
	if err != nil {
		return err
	}
	if len(params) == 3 {
		return nil
	}
	if _, err := internalQuery(ctx, router, region, params); err != nil {
		return fmt.Errorf("sns TagResource: %w", err)
	}
	return nil
}

func snsTopicTagParams(topicARN string, rawTags any) (map[string]string, error) {
	tags, ok := rawTags.([]any)
	if !ok {
		return nil, fmt.Errorf("SNS Topic Tags must be an array")
	}
	params := map[string]string{
		"Action":      "TagResource",
		"ResourceArn": topicARN,
		"Version":     "2010-03-31",
	}
	for i, rawTag := range tags {
		tag, ok := rawTag.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("SNS Topic Tags entry must be an object")
		}
		key, ok := tag["Key"]
		if !ok || key == nil {
			return nil, fmt.Errorf("SNS Topic Tags entry must contain Key")
		}
		prefix := fmt.Sprintf("Tags.member.%d", i+1)
		params[prefix+".Key"] = cfnScalarString(key)
		if value, ok := tag["Value"]; ok && value != nil {
			params[prefix+".Value"] = cfnScalarString(value)
		}
	}
	return params, nil
}

func snsSubscriptionRequestRegion(props map[string]any, stackRegion string) (string, error) {
	rawRegion, ok := props["Region"]
	if !ok || rawRegion == nil {
		return stackRegion, nil
	}
	region := cfnScalarString(rawRegion)
	if region == "" || region == stackRegion {
		return stackRegion, nil
	}
	return "", fmt.Errorf("AWS::SNS::Subscription Region %q is not implemented for cross-region subscriptions", region)
}

func subscribeSNSInline(ctx context.Context, router http.Handler, region, topicARN string, props map[string]any) error {
	rawSubscriptions, ok := props["Subscription"]
	if !ok || rawSubscriptions == nil {
		return nil
	}
	subscriptions, ok := rawSubscriptions.([]any)
	if !ok {
		return fmt.Errorf("SNS Topic Subscription must be an array")
	}
	for _, rawSubscription := range subscriptions {
		subscription, ok := rawSubscription.(map[string]any)
		if !ok {
			return fmt.Errorf("SNS Topic Subscription entry must be an object")
		}
		params := map[string]string{
			"Action":   "Subscribe",
			"TopicArn": topicARN,
			"Protocol": cfnScalarString(subscription["Protocol"]),
			"Endpoint": cfnScalarString(subscription["Endpoint"]),
			"Version":  "2010-03-31",
		}
		if _, err := internalQuery(ctx, router, region, params); err != nil {
			return fmt.Errorf("sns Subscribe inline subscription: %w", err)
		}
	}
	return nil
}

type snsTopicHandler struct{}

func (h *snsTopicHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// physicalID is the topic ARN; last segment is the topic name.
	oldName := physicalID
	if i := strings.LastIndex(physicalID, ":"); i >= 0 {
		oldName = physicalID[i+1:]
	}
	if n, ok := props["TopicName"].(string); ok && n != "" && n != oldName {
		return "", nil, errReplacementRequired
	}
	if asBool(props["FifoTopic"]) != asBool(oldProps["FifoTopic"]) {
		return "", nil, errReplacementRequired
	}
	if changed, err := cfnPropertyChanged(props, oldProps, "Subscription"); err != nil {
		return "", nil, err
	} else if changed {
		return "", nil, failUpdate(fmt.Errorf("AWS::SNS::Topic Subscription updates are not implemented"))
	}
	if name := snsRemovedAttribute(props, oldProps, []string{"ArchivePolicy", "ContentBasedDeduplication", "DataProtectionPolicy", "DeliveryPolicy", "DisplayName", "FifoThroughputScope", "KmsMasterKeyId", "SignatureVersion", "TracingConfig"}); name != "" {
		return "", nil, failUpdate(fmt.Errorf("removing SNS topic attribute %s is not implemented", name))
	}

	attrs, err := snsTopicAttributesFromProps(props)
	if err != nil {
		return "", nil, err
	}
	if err := applySNSAttributes(ctx, router, rCtx.Region, "SetTopicAttributes", "TopicArn", physicalID, attrs); err != nil {
		return "", nil, err
	}
	if err := applySNSTopicTags(ctx, router, rCtx.Region, physicalID, props); err != nil {
		return "", nil, err
	}
	return physicalID, map[string]string{"TopicName": oldName, "TopicArn": physicalID}, nil
}

func (h *snsTopicHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	topicName, _ := props["TopicName"].(string)
	if topicName == "" {
		topicName = rCtx.generatedName()
	}

	params := map[string]string{
		"Action":  "CreateTopic",
		"Name":    topicName,
		"Version": "2010-03-31",
	}
	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("sns CreateTopic: %w", err)
	}
	arn := extractXMLValue(rec.Body.String(), "TopicArn")
	if arn == "" {
		arn = protocol.ARN(rCtx.Region, cfg.AccountID, "sns", topicName)
	}
	topicAttributes, err := snsTopicAttributesFromProps(props)
	if err != nil {
		return "", nil, err
	}
	if err := applySNSAttributes(ctx, router, rCtx.Region, "SetTopicAttributes", "TopicArn", arn, topicAttributes); err != nil {
		return "", nil, err
	}
	if err := applySNSTopicTags(ctx, router, rCtx.Region, arn, props); err != nil {
		return "", nil, err
	}
	if err := subscribeSNSInline(ctx, router, rCtx.Region, arn, props); err != nil {
		return "", nil, err
	}
	attrs := map[string]string{
		"TopicName": topicName,
		"TopicArn":  arn,
	}
	return arn, attrs, nil
}

func (h *snsTopicHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":   "DeleteTopic",
		"TopicArn": physicalID,
		"Version":  "2010-03-31",
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

// ── SNS Subscription handler ───────────────────────────────────────────────

type snsSubscriptionHandler struct{}

func (h *snsSubscriptionHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	topicArn, _ := props["TopicArn"].(string)
	proto, _ := props["Protocol"].(string)
	endpoint, _ := props["Endpoint"].(string)

	params := map[string]string{
		"Action":   "Subscribe",
		"TopicArn": topicArn,
		"Protocol": proto,
		"Endpoint": endpoint,
		"Version":  "2010-03-31",
	}
	subscriptionRegion, err := snsSubscriptionRequestRegion(props, rCtx.Region)
	if err != nil {
		return "", nil, err
	}
	rec, err := internalQuery(ctx, router, subscriptionRegion, params)
	if err != nil {
		return "", nil, fmt.Errorf("sns Subscribe: %w", err)
	}
	arn := extractXMLValue(rec.Body.String(), "SubscriptionArn")
	if arn == "" {
		arn = fmt.Sprintf("stub-sub-%s-%d", rCtx.StackName, len(rCtx.Resources))
	}
	attributes, err := snsSubscriptionAttributesFromProps(props)
	if err != nil {
		return "", nil, err
	}
	if err := applySNSAttributes(ctx, router, subscriptionRegion, "SetSubscriptionAttributes", "SubscriptionArn", arn, attributes); err != nil {
		return "", nil, err
	}
	attrs := map[string]string{
		"Arn":      arn,
		"TopicArn": topicArn,
		"Protocol": proto,
		"Endpoint": endpoint,
	}
	return arn, attrs, nil
}

func (h *snsSubscriptionHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":          "Unsubscribe",
		"SubscriptionArn": physicalID,
		"Version":         "2010-03-31",
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *snsSubscriptionHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	for _, name := range []string{"TopicArn", "Protocol", "Endpoint", "Region"} {
		if cfnScalarString(props[name]) != cfnScalarString(oldProps[name]) {
			return "", nil, errReplacementRequired
		}
	}
	if name := snsRemovedAttribute(props, oldProps, []string{"DeliveryPolicy", "FilterPolicy", "FilterPolicyScope", "RawMessageDelivery", "RedrivePolicy", "ReplayPolicy", "SubscriptionRoleArn"}); name != "" {
		return "", nil, failUpdate(fmt.Errorf("removing SNS subscription attribute %s is not implemented", name))
	}
	attributes, err := snsSubscriptionAttributesFromProps(props)
	if err != nil {
		return "", nil, err
	}
	if err := applySNSAttributes(ctx, router, rCtx.Region, "SetSubscriptionAttributes", "SubscriptionArn", physicalID, attributes); err != nil {
		return "", nil, err
	}
	return physicalID, map[string]string{
		"Arn":      physicalID,
		"TopicArn": cfnScalarString(props["TopicArn"]),
		"Protocol": cfnScalarString(props["Protocol"]),
		"Endpoint": cfnScalarString(props["Endpoint"]),
	}, nil
}

// ── S3 Bucket handler ──────────────────────────────────────────────────────

type s3BucketHandler struct{}

func (h *s3BucketHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	decoded, err := decodeS3BucketProperties(props)
	if err != nil {
		return "", nil, failUpdate(err)
	}
	oldDecoded, err := decodeS3BucketProperties(oldProps)
	if err != nil {
		return "", nil, failUpdate(err)
	}
	// BucketName is immutable.
	if decoded.BucketName != oldDecoded.BucketName {
		return "", nil, errReplacementRequired
	}
	operations, err := planS3BucketOperations(decoded, oldDecoded)
	if err != nil {
		return "", nil, failUpdate(err)
	}
	compensations, err := planS3BucketOperations(oldDecoded, decoded)
	if err != nil {
		return "", nil, failUpdate(err)
	}
	applied, err := applyS3BucketOperations(ctx, router, rCtx.Region, physicalID, operations)
	if err != nil {
		compensationErr := compensateS3BucketOperations(ctx, router, rCtx.Region, physicalID, operations[:applied], compensations)
		return "", nil, failUpdate(errors.Join(err, compensationErr))
	}
	return physicalID, s3BucketAttrs(cfg, physicalID, rCtx.Region), nil
}

func (h *s3BucketHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	decoded, err := decodeS3BucketProperties(props)
	if err != nil {
		return "", nil, err
	}
	operations, err := planS3BucketOperations(decoded, nil)
	if err != nil {
		return "", nil, err
	}
	bucketName := decoded.BucketName
	if bucketName == "" {
		bucketName = strings.ToLower(rCtx.generatedName())
	}

	_, err = internalRequest(ctx, router, rCtx.Region, http.MethodPut, "/"+bucketName, "", nil)
	if err != nil {
		return "", nil, fmt.Errorf("s3 CreateBucket: %w", err)
	}
	if _, err := applyS3BucketOperations(ctx, router, rCtx.Region, bucketName, operations); err != nil {
		return bucketName, nil, err
	}
	return bucketName, s3BucketAttrs(cfg, bucketName, rCtx.Region), nil
}

// s3BucketAttrs builds the Fn::GetAtt attributes for an AWS::S3::Bucket.
//
// DomainName and RegionalDomainName are minted on the hostname THIS emulator
// answers on, not the literal "amazonaws.com" AWS returns. They are not
// decoration: CDK wires an S3 origin into a CloudFront distribution by
// Fn::GetAtt-ing RegionalDomainName, so an amazonaws.com value made the
// distribution front the real AWS bucket of that name — or nothing at all —
// rather than the bucket the same stack had just created here.
//
// Minted through the shared helper every other client-facing hostname uses, so
// the grammar cannot drift from what the router resolves: the result is
// "{bucket}.s3[.{region}].{host}", which HostClassifier tier A recognises by
// its ".s3." separator on any base, including OVERCAST_HOSTNAME and the
// wildcard-DNS domains.
func s3BucketAttrs(cfg *config.Config, bucket, region string) map[string]string {
	base := "http://localhost:4566"
	if cfg != nil {
		base = cfg.ExternalBaseURL()
	}
	return map[string]string{
		"Arn":                 fmt.Sprintf("arn:aws:s3:::%s", bucket),
		"BucketName":          bucket,
		"DomainName":          serviceutil.HostRoutedHostnameFromBase(base, "s3", bucket, ""),
		"DualStackDomainName": serviceutil.HostRoutedHostnameFromBase(base, "s3.dualstack", bucket, region),
		"RegionalDomainName":  serviceutil.HostRoutedHostnameFromBase(base, "s3", bucket, region),
		"WebsiteURL":          serviceutil.HostRoutedURLFromBase(base, "s3-website", bucket, region, ""),
	}
}

func (h *s3BucketHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/"+physicalID, "", nil)
	if err == nil || (rec != nil && rec.Code == http.StatusNotFound) {
		return nil
	}
	return fmt.Errorf("%w: s3 DeleteBucket: %v", errDeletionBlocked, err)
}

// ── DynamoDB Table handler ─────────────────────────────────────────────────

type dynamodbTableHandler struct{}

type dynamodbTTLConfig struct {
	specified     bool
	enabled       bool
	attributeName string
}

func (h *dynamodbTableHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// TableName and KeySchema are immutable.
	if n, ok := props["TableName"].(string); ok && n != "" && n != physicalID {
		return "", nil, errReplacementRequired
	}
	newTTL, err := parseDynamoDBTTLConfig(props, rCtx.LogicalID)
	if err != nil {
		return "", nil, failUpdate(err)
	}
	oldTTL, err := parseDynamoDBTTLConfig(oldProps, rCtx.LogicalID)
	if err != nil {
		return "", nil, failUpdate(err)
	}
	if changed, err := cfnPropertyChanged(props, oldProps, "LocalSecondaryIndexes"); err != nil {
		return "", nil, failUpdate(err)
	} else if changed {
		return "", nil, failUpdate(fmt.Errorf("AWS::DynamoDB::Table LocalSecondaryIndexes updates are not supported"))
	}

	reqBody := map[string]any{"TableName": physicalID}
	haveMutable := false
	if v, ok := props["BillingMode"]; ok {
		reqBody["BillingMode"] = v
		haveMutable = true
	}
	if v, ok := props["ProvisionedThroughput"]; ok {
		reqBody["ProvisionedThroughput"] = v
		haveMutable = true
	}
	if v, ok := props["AttributeDefinitions"]; ok {
		reqBody["AttributeDefinitions"] = v
		haveMutable = true
	}
	if v, ok := props["StreamSpecification"].(map[string]any); ok {
		if _, hasViewType := v["StreamViewType"]; hasViewType {
			v["StreamEnabled"] = true
		}
		reqBody["StreamSpecification"] = v
		haveMutable = true
	}
	// TimeToLiveSpecification is a separate API call. Apply it first so a
	// later UpdateTable failure can restore the previous TTL configuration.
	restoreTTL, ttlDirty, err := reconcileDynamoDBTableTTL(ctx, router, rCtx.Region, physicalID, newTTL, oldTTL)
	if err != nil {
		if ttlDirty {
			return "", nil, failDirtyUpdate(fmt.Errorf("dynamodb UpdateTimeToLive: %w", err))
		}
		return "", nil, failUpdate(fmt.Errorf("dynamodb UpdateTimeToLive: %w", err))
	}
	if haveMutable {
		if _, err := internalJSON(ctx, router, rCtx.Region, "DynamoDB_20120810.UpdateTable", reqBody); err != nil {
			updateErr := fmt.Errorf("dynamodb UpdateTable: %w", err)
			if restoreTTL == nil {
				return "", nil, updateErr
			}
			if restoreErr := restoreTTL(); restoreErr != nil {
				return "", nil, failDirtyUpdate(errors.Join(updateErr, fmt.Errorf("restore DynamoDB TTL after failed table update: %w", restoreErr)))
			}
			return "", nil, failUpdate(updateErr)
		}
	}
	return physicalID, map[string]string{"TableName": physicalID}, nil
}

func (h *dynamodbTableHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	ttl, err := parseDynamoDBTTLConfig(props, rCtx.LogicalID)
	if err != nil {
		return "", nil, err
	}
	tableName, _ := props["TableName"].(string)
	if tableName == "" {
		tableName = rCtx.generatedName()
	}

	// Build the CreateTable request.
	reqBody := map[string]any{
		"TableName": tableName,
	}
	// Copy key schema and attribute definitions.
	if ks, ok := props["KeySchema"]; ok {
		reqBody["KeySchema"] = ks
	}
	if ad, ok := props["AttributeDefinitions"]; ok {
		reqBody["AttributeDefinitions"] = ad
	}
	if bt, ok := props["BillingMode"]; ok {
		reqBody["BillingMode"] = bt
	}
	if pt, ok := props["ProvisionedThroughput"]; ok {
		reqBody["ProvisionedThroughput"] = pt
	} else {
		reqBody["BillingMode"] = "PAY_PER_REQUEST"
	}
	if gsi, ok := props["GlobalSecondaryIndexes"]; ok {
		reqBody["GlobalSecondaryIndexes"] = gsi
	}
	if lsi, ok := props["LocalSecondaryIndexes"]; ok {
		reqBody["LocalSecondaryIndexes"] = lsi
	}
	if ss, ok := props["StreamSpecification"].(map[string]any); ok {
		// CloudFormation templates set StreamViewType without an explicit StreamEnabled.
		// Enable the stream whenever StreamViewType is provided.
		if _, hasViewType := ss["StreamViewType"]; hasViewType {
			ss["StreamEnabled"] = true
		}
		reqBody["StreamSpecification"] = ss
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "DynamoDB_20120810.CreateTable", reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("dynamodb CreateTable: %w", err)
	}
	if ttl.enabled {
		if err := updateDynamoDBTableTTL(ctx, router, rCtx.Region, tableName, dynamodbTTLSpec(ttl)); err != nil {
			ttlErr := fmt.Errorf("dynamodb UpdateTimeToLive: %w", err)
			if _, deleteErr := internalJSON(ctx, router, rCtx.Region, "DynamoDB_20120810.DeleteTable", map[string]any{"TableName": tableName}); deleteErr != nil {
				return tableName, nil, errors.Join(ttlErr, fmt.Errorf("delete DynamoDB table after TTL failure: %w", deleteErr))
			}
			return "", nil, ttlErr
		}
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if td, ok := resp["TableDescription"].(map[string]any); ok {
			if arn, ok := td["TableArn"].(string); ok {
				attrs := map[string]string{
					"Arn":       arn,
					"TableName": tableName,
				}
				if sa, ok := td["LatestStreamArn"].(string); ok {
					attrs["StreamArn"] = sa
				}
				return tableName, attrs, nil
			}
		}
	}
	return tableName, map[string]string{"TableName": tableName}, nil
}

// reconcileDynamoDBTableTTL applies the desired CloudFormation TTL state and
// returns a compensating function when it changed DynamoDB. Attribute names
// cannot be swapped while TTL is enabled, so the AWS-required transition is
// disable the old attribute then enable the new one.
//
// The bool result is true only when a failed transition could not restore its
// previous configuration; callers must retain that dirty resource for stack
// rollback rather than claiming it is unchanged.
func reconcileDynamoDBTableTTL(ctx context.Context, router http.Handler, region, tableName string, newConfig, oldConfig dynamodbTTLConfig) (func() error, bool, error) {
	if reflect.DeepEqual(newConfig, oldConfig) {
		return nil, false, nil
	}

	if oldConfig.enabled && newConfig.enabled && oldConfig.attributeName != newConfig.attributeName {
		if err := updateDynamoDBTableTTL(ctx, router, region, tableName, dynamodbTTLDisableSpec(oldConfig)); err != nil {
			return nil, false, err
		}
		if err := updateDynamoDBTableTTL(ctx, router, region, tableName, dynamodbTTLSpec(newConfig)); err != nil {
			restoreErr := updateDynamoDBTableTTL(ctx, router, region, tableName, dynamodbTTLSpec(oldConfig))
			if restoreErr != nil {
				return nil, true, errors.Join(err, fmt.Errorf("restore previous DynamoDB TTL configuration: %w", restoreErr))
			}
			return nil, false, err
		}
		return func() error {
			return restoreDynamoDBTableTTL(ctx, router, region, tableName, newConfig, oldConfig)
		}, false, nil
	}

	appliedConfig, changed, err := applyDynamoDBTableTTL(ctx, router, region, tableName, oldConfig, newConfig)
	if err != nil || !changed {
		return nil, false, err
	}
	return func() error {
		return restoreDynamoDBTableTTL(ctx, router, region, tableName, appliedConfig, oldConfig)
	}, false, nil
}

func restoreDynamoDBTableTTL(ctx context.Context, router http.Handler, region, tableName string, current, desired dynamodbTTLConfig) error {
	if current.enabled && desired.enabled && current.attributeName != desired.attributeName {
		if err := updateDynamoDBTableTTL(ctx, router, region, tableName, dynamodbTTLDisableSpec(current)); err != nil {
			return err
		}
	}
	_, _, err := applyDynamoDBTableTTL(ctx, router, region, tableName, current, desired)
	return err
}

func applyDynamoDBTableTTL(ctx context.Context, router http.Handler, region, tableName string, current, desired dynamodbTTLConfig) (dynamodbTTLConfig, bool, error) {
	if reflect.DeepEqual(current, desired) {
		return current, false, nil
	}
	if !current.enabled && !desired.enabled {
		return current, false, nil
	}
	if !desired.specified {
		if !current.enabled {
			return current, false, nil
		}
		if err := updateDynamoDBTableTTL(ctx, router, region, tableName, dynamodbTTLDisableSpec(current)); err != nil {
			return current, false, err
		}
		return dynamodbTTLConfig{specified: true, attributeName: current.attributeName}, true, nil
	}
	if current.enabled && !desired.enabled {
		if err := updateDynamoDBTableTTL(ctx, router, region, tableName, dynamodbTTLDisableSpec(current)); err != nil {
			return current, false, err
		}
		return dynamodbTTLConfig{specified: true, attributeName: current.attributeName}, true, nil
	}
	if err := updateDynamoDBTableTTL(ctx, router, region, tableName, dynamodbTTLSpec(desired)); err != nil {
		return current, false, err
	}
	return desired, true, nil
}

func parseDynamoDBTTLConfig(props map[string]any, logicalID string) (dynamodbTTLConfig, error) {
	raw, specified := props["TimeToLiveSpecification"]
	if !specified || raw == nil {
		return dynamodbTTLConfig{}, nil
	}
	spec, ok := raw.(map[string]any)
	if !ok {
		return dynamodbTTLConfig{}, dynamodbTTLValidationError(logicalID, "#/TimeToLiveSpecification: expected type: JSONObject")
	}
	rawEnabled, hasEnabled := spec["Enabled"]
	if !hasEnabled || rawEnabled == nil {
		return dynamodbTTLConfig{}, dynamodbTTLValidationError(logicalID, "#/TimeToLiveSpecification: required key [Enabled] not found")
	}
	enabled, validEnabled := rawEnabled.(bool)
	if !validEnabled {
		if value, ok := rawEnabled.(string); ok && (strings.EqualFold(value, "true") || strings.EqualFold(value, "false")) {
			enabled = strings.EqualFold(value, "true")
			validEnabled = true
		}
	}
	if !validEnabled {
		return dynamodbTTLConfig{}, dynamodbTTLValidationError(logicalID, "#/TimeToLiveSpecification/Enabled: expected type: Boolean")
	}

	attributeName := ""
	if rawAttribute, hasAttribute := spec["AttributeName"]; hasAttribute && rawAttribute != nil {
		var validAttribute bool
		attributeName, validAttribute = rawAttribute.(string)
		if !validAttribute {
			return dynamodbTTLConfig{}, dynamodbTTLValidationError(logicalID, "#/TimeToLiveSpecification/AttributeName: expected type: String")
		}
		length := utf8.RuneCountInString(attributeName)
		if length < 1 {
			return dynamodbTTLConfig{}, dynamodbTTLValidationError(logicalID, fmt.Sprintf("#/TimeToLiveSpecification/AttributeName: expected minLength: 1, actual: %d", length))
		}
		if length > 255 {
			return dynamodbTTLConfig{}, dynamodbTTLValidationError(logicalID, fmt.Sprintf("#/TimeToLiveSpecification/AttributeName: expected maxLength: 255, actual: %d", length))
		}
	}
	if enabled && attributeName == "" {
		return dynamodbTTLConfig{}, dynamodbTTLValidationError(logicalID, "#/TimeToLiveSpecification: required key [AttributeName] not found")
	}
	return dynamodbTTLConfig{specified: true, enabled: enabled, attributeName: attributeName}, nil
}

func dynamodbTTLValidationError(logicalID, detail string) error {
	resource := logicalID
	if resource == "" {
		resource = "AWS::DynamoDB::Table"
	}
	return fmt.Errorf("Properties validation failed for resource %s with message: %s", resource, detail)
}

func dynamodbTTLSpec(config dynamodbTTLConfig) map[string]any {
	return map[string]any{
		"Enabled":       config.enabled,
		"AttributeName": config.attributeName,
	}
}

func dynamodbTTLDisableSpec(config dynamodbTTLConfig) map[string]any {
	return map[string]any{
		"Enabled":       false,
		"AttributeName": config.attributeName,
	}
}

func updateDynamoDBTableTTL(ctx context.Context, router http.Handler, region, tableName string, spec map[string]any) error {
	_, err := internalJSON(ctx, router, region, "DynamoDB_20120810.UpdateTimeToLive", map[string]any{
		"TableName":               tableName,
		"TimeToLiveSpecification": spec,
	})
	return err
}

func (h *dynamodbTableHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	name := physicalID
	if i := strings.LastIndex(physicalID, "/"); i >= 0 {
		name = physicalID[i+1:]
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "DynamoDB_20120810.DeleteTable", map[string]any{
		"TableName": name,
	})
	return nil
}

// ── Lambda Function handler ────────────────────────────────────────────────

type lambdaFunctionHandler struct{}

func (h *lambdaFunctionHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	funcName, _ := props["FunctionName"].(string)
	if funcName == "" {
		funcName = rCtx.generatedName()
	}
	if err := checkLambdaFunctionAuxiliaryPropertySupport(props, nil); err != nil {
		return "", nil, err
	}

	// A template's Code.ZipFile is inline source text; the Lambda API's is
	// base64 of a zip archive. Package it rather than just encoding it — see
	// inlineCodeZip.
	runtime, _ := props["Runtime"].(string)
	handler, _ := props["Handler"].(string)
	code, _ := props["Code"].(map[string]any)
	if zf, ok := code["ZipFile"].(string); ok {
		packaged, err := inlineCodeZip(zf, runtime, handler)
		if err != nil {
			return "", nil, fmt.Errorf("package inline function code: %w", err)
		}
		code["ZipFile"] = base64.StdEncoding.EncodeToString(packaged)
	}

	body := map[string]any{
		"FunctionName": funcName,
		"Runtime":      props["Runtime"],
		"Handler":      props["Handler"],
		"Role":         props["Role"],
		"Code":         code,
	}
	for _, property := range []string{
		"Architectures", "VpcConfig", "FileSystemConfigs", "ImageConfig", "PackageType",
		"DeadLetterConfig", "TracingConfig", "EphemeralStorage", "SnapStart",
		"CapacityProviderConfig", "DurableConfig", "TenancyConfig",
	} {
		copyAnyProp(body, props, property, property)
	}
	copyAnyProp(body, props, "KmsKeyArn", "KMSKeyArn")
	if desc, ok := props["Description"]; ok {
		body["Description"] = desc
	}
	if env, ok := props["Environment"]; ok {
		body["Environment"] = env
	}
	if timeout, ok := props["Timeout"]; ok {
		body["Timeout"] = timeout
	}
	if mem, ok := props["MemorySize"]; ok {
		body["MemorySize"] = mem
	}
	if lc, ok := props["LoggingConfig"]; ok {
		body["LoggingConfig"] = lc
	}
	if layers, ok := props["Layers"]; ok {
		body["Layers"] = layers
	}
	// Optional; set by CDK's Function `codeSigningConfig` prop. Passed through
	// so the association survives a deploy — Lambda stores it without enforcing
	// signature validation.
	if csc, ok := props["CodeSigningConfigArn"]; ok {
		body["CodeSigningConfigArn"] = csc
	}
	if tagMap := mergeLambdaTags(rCtx.StackTags, props["Tags"]); len(tagMap) > 0 {
		body["Tags"] = tagMap
	}
	if publish, _ := props["PublishToLatestPublished"].(bool); publish {
		body["PublishTo"] = "LATEST_PUBLISHED"
	}

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/2015-03-31/functions", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("lambda CreateFunction: %w", err)
	}
	if reserved, ok := props["ReservedConcurrentExecutions"]; ok {
		if err := putLambdaReservedConcurrency(ctx, router, rCtx.Region, funcName, reserved); err != nil {
			_, cleanupErr := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/2015-03-31/functions/"+url.PathEscape(funcName), "", nil)
			if cleanupErr != nil {
				return funcName, nil, errors.Join(err, fmt.Errorf("lambda cleanup DeleteFunction: %w", cleanupErr))
			}
			return "", nil, err
		}
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if arn, ok := resp["FunctionArn"].(string); ok {
			attrs := map[string]string{
				"Arn":          arn,
				"FunctionName": funcName,
			}
			return funcName, attrs, nil
		}
	}
	return funcName, map[string]string{"FunctionName": funcName}, nil
}

func (h *lambdaFunctionHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	name := physicalID
	if i := strings.LastIndex(physicalID, ":"); i >= 0 {
		name = physicalID[i+1:]
	}
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/2015-03-31/functions/"+url.PathEscape(name), "", nil)
	if err != nil && (rec == nil || rec.Code != http.StatusNotFound) {
		return fmt.Errorf("lambda DeleteFunction: %w", err)
	}
	return nil
}

// Update implements in-place updates for AWS::Lambda::Function. Code changes
// are dispatched to UpdateFunctionCode (PUT /functions/{name}/code) and the
// remaining mutable configuration is dispatched to UpdateFunctionConfiguration
// (PUT /functions/{name}/configuration). The function name is immutable on
// real AWS, so when it changes the provisioner falls back to replacement.
func (h *lambdaFunctionHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name := physicalID
	if i := strings.LastIndex(physicalID, ":"); i >= 0 {
		name = physicalID[i+1:]
	}
	// FunctionName is immutable. Adding, changing, or explicitly removing it
	// all change the physical-name contract and therefore require replacement.
	if !reflect.DeepEqual(props["FunctionName"], oldProps["FunctionName"]) {
		return "", nil, errReplacementRequired
	}
	packageType, _ := props["PackageType"].(string)
	if packageType == "" {
		packageType = "Zip"
	}
	oldPackageType, _ := oldProps["PackageType"].(string)
	if oldPackageType == "" {
		oldPackageType = "Zip"
	}
	if packageType != oldPackageType {
		return "", nil, errReplacementRequired
	}

	for _, property := range []string{"DurableConfig", "TenancyConfig"} {
		if !reflect.DeepEqual(props[property], oldProps[property]) {
			return "", nil, errReplacementRequired
		}
	}
	if err := validateLambdaFunctionRequiredPropertyRemovals(props, oldProps, packageType, rCtx.LogicalID); err != nil {
		return "", nil, failUpdate(err)
	}
	if err := checkLambdaFunctionAuxiliaryPropertySupport(props, oldProps); err != nil {
		return "", nil, failUpdate(err)
	}

	arn, completed, err := h.applyUpdate(ctx, router, name, props, oldProps, rCtx)
	if err != nil {
		// A Lambda function update spans several APIs. Restore only mutations
		// that completed, in reverse order, so a rejected phase cannot change
		// the function's revision or LastModified during compensation.
		compensationErr := h.compensateUpdate(ctx, router, name, oldProps, props, completed, rCtx)
		if compensationErr == nil {
			return "", nil, failUpdate(err)
		}
		compensationErr = fmt.Errorf("restore Lambda function after failed update: %w", compensationErr)
		return "", nil, failDirtyUpdate(errors.Join(err, compensationErr))
	}
	attrs := map[string]string{"FunctionName": name, "Arn": protocol.LambdaARN(rCtx.Region, rCtx.AccountID, name)}
	if arn != "" {
		attrs["Arn"] = arn
	}
	return name, attrs, nil
}

type lambdaUpdateProgress struct {
	tags          bool
	configuration bool
	code          bool
	codeSigning   bool
}

// applyUpdate tracks tags first, then follows AWS's documented
// configuration-before-code ordering and CloudFormation's separate association
// and concurrency operations.
func (h *lambdaFunctionHandler) applyUpdate(ctx context.Context, router http.Handler, name string, props, prior map[string]any, rCtx *resolveContext) (string, lambdaUpdateProgress, error) {
	var completed lambdaUpdateProgress
	applied, err := updateLambdaTags(ctx, router, rCtx.Region, protocol.LambdaARN(rCtx.Region, rCtx.AccountID, name), rCtx.StackTags, rCtx.PreviousStackTags, props["Tags"], prior["Tags"])
	completed.tags = applied
	if err != nil {
		return "", completed, err
	}
	arn, applied, err := h.updateConfiguration(ctx, router, name, props, prior, rCtx)
	if err != nil {
		return "", completed, err
	}
	completed.configuration = applied
	if applied, err = h.updateCode(ctx, router, name, props, prior, rCtx); err != nil {
		return "", completed, err
	}
	completed.code = applied
	if applied, err = h.updateCodeSigningConfig(ctx, router, name, props, prior, rCtx); err != nil {
		return "", completed, err
	}
	completed.codeSigning = applied
	if err = h.updateConcurrency(ctx, router, name, props, prior, rCtx); err != nil {
		return "", completed, err
	}
	return arn, completed, nil
}

func (h *lambdaFunctionHandler) compensateUpdate(ctx context.Context, router http.Handler, name string, props, prior map[string]any, completed lambdaUpdateProgress, rCtx *resolveContext) error {
	var compensationErr error
	if completed.codeSigning {
		if _, err := h.updateCodeSigningConfig(ctx, router, name, props, prior, rCtx); err != nil {
			compensationErr = errors.Join(compensationErr, err)
		}
	}
	if completed.code {
		if _, err := h.updateCode(ctx, router, name, props, prior, rCtx); err != nil {
			compensationErr = errors.Join(compensationErr, err)
		}
	}
	if completed.configuration {
		if _, _, err := h.updateConfiguration(ctx, router, name, props, prior, rCtx); err != nil {
			compensationErr = errors.Join(compensationErr, err)
		}
	}
	if completed.tags {
		if _, err := updateLambdaTags(ctx, router, rCtx.Region, protocol.LambdaARN(rCtx.Region, rCtx.AccountID, name), rCtx.PreviousStackTags, rCtx.StackTags, props["Tags"], prior["Tags"]); err != nil {
			compensationErr = errors.Join(compensationErr, err)
		}
	}
	return compensationErr
}

func (h *lambdaFunctionHandler) updateConfiguration(ctx context.Context, router http.Handler, name string, props, prior map[string]any, rCtx *resolveContext) (string, bool, error) {
	cfgBody := map[string]any{}
	for _, k := range []string{
		"Runtime", "Handler", "Role", "Description", "Environment", "Timeout", "MemorySize", "Layers", "LoggingConfig",
		"VpcConfig", "FileSystemConfigs", "ImageConfig", "DeadLetterConfig", "TracingConfig", "EphemeralStorage",
		"SnapStart", "CapacityProviderConfig",
	} {
		if v, ok := props[k]; ok && !reflect.DeepEqual(v, prior[k]) {
			cfgBody[k] = v
		}
	}
	if !reflect.DeepEqual(props["KmsKeyArn"], prior["KmsKeyArn"]) {
		copyAnyProp(cfgBody, props, "KmsKeyArn", "KMSKeyArn")
	}
	clearValues := map[string]any{
		"Description": "", "Environment": map[string]any{"Variables": map[string]any{}}, "Layers": []any{},
		"LoggingConfig": map[string]any{}, "VpcConfig": map[string]any{}, "FileSystemConfigs": []any{}, "ImageConfig": map[string]any{},
		"Timeout": 3, "MemorySize": 128,
	}
	for key, empty := range clearValues {
		if _, present := props[key]; !present {
			if _, existed := prior[key]; existed {
				cfgBody[key] = empty
			}
		}
	}
	var arn string
	if len(cfgBody) == 0 {
		return "", false, nil
	}
	data, _ := json.Marshal(cfgBody)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, "/2015-03-31/functions/"+url.PathEscape(name)+"/configuration", "application/json", data)
	if err != nil {
		return "", false, fmt.Errorf("lambda UpdateFunctionConfiguration: %w", err)
	}
	var resp map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &resp) == nil {
		arn, _ = resp["FunctionArn"].(string)
	}
	return arn, true, nil
}

func (h *lambdaFunctionHandler) updateCode(ctx context.Context, router http.Handler, name string, props, prior map[string]any, rCtx *resolveContext) (bool, error) {
	publish, _ := props["PublishToLatestPublished"].(bool)
	priorPublish, _ := prior["PublishToLatestPublished"].(bool)
	if reflect.DeepEqual(props["Code"], prior["Code"]) &&
		reflect.DeepEqual(props["Architectures"], prior["Architectures"]) &&
		(!publish || priorPublish) {
		return false, nil
	}
	// CFN templates supply ZipFile as inline source; UpdateFunctionCode expects
	// a base64 zip archive.
	if code, ok := props["Code"].(map[string]any); ok {
		body := map[string]any{}
		if zf, ok := code["ZipFile"].(string); ok {
			runtime, _ := props["Runtime"].(string)
			handler, _ := props["Handler"].(string)
			packaged, err := inlineCodeZip(zf, runtime, handler)
			if err != nil {
				return false, fmt.Errorf("package inline function code: %w", err)
			}
			body["ZipFile"] = base64.StdEncoding.EncodeToString(packaged)
		}
		if v, ok := code["S3Bucket"]; ok {
			body["S3Bucket"] = v
		}
		if v, ok := code["S3Key"]; ok {
			body["S3Key"] = v
		}
		if v, ok := code["S3ObjectVersion"]; ok {
			body["S3ObjectVersion"] = v
		}
		if v, ok := code["ImageUri"]; ok {
			body["ImageUri"] = v
		}
		for _, property := range []string{"S3ObjectStorageMode", "SourceKMSKeyArn"} {
			if v, ok := code[property]; ok {
				body[property] = v
			}
		}
		if v, ok := props["Architectures"]; ok {
			body["Architectures"] = v
		} else if _, existed := prior["Architectures"]; existed {
			body["Architectures"] = []string{"x86_64"}
		}
		if publish, _ := props["PublishToLatestPublished"].(bool); publish {
			body["PublishTo"] = "LATEST_PUBLISHED"
		}
		data, _ := json.Marshal(body)
		_, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, "/2015-03-31/functions/"+url.PathEscape(name)+"/code", "application/json", data)
		if err != nil {
			return false, fmt.Errorf("lambda UpdateFunctionCode: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func (h *lambdaFunctionHandler) updateCodeSigningConfig(ctx context.Context, router http.Handler, name string, props, prior map[string]any, rCtx *resolveContext) (bool, error) {
	if reflect.DeepEqual(props["CodeSigningConfigArn"], prior["CodeSigningConfigArn"]) {
		return false, nil
	}
	path := "/2020-06-30/functions/" + url.PathEscape(name) + "/code-signing-config"
	if arn, _ := props["CodeSigningConfigArn"].(string); arn != "" {
		data, err := json.Marshal(map[string]any{"CodeSigningConfigArn": arn})
		if err != nil {
			return false, fmt.Errorf("lambda PutFunctionCodeSigningConfig: marshal request: %w", err)
		}
		if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, path, "application/json", data); err != nil {
			return false, fmt.Errorf("lambda PutFunctionCodeSigningConfig: %w", err)
		}
		return true, nil
	}
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil); err != nil {
		return false, fmt.Errorf("lambda DeleteFunctionCodeSigningConfig: %w", err)
	}
	return true, nil
}

func checkLambdaFunctionAuxiliaryPropertySupport(props, prior map[string]any) error {
	for _, property := range []string{"RuntimeManagementConfig", "RecursiveLoop", "FunctionScalingConfig"} {
		if reflect.DeepEqual(props[property], prior[property]) {
			continue
		}
		return fmt.Errorf("AWS::Lambda::Function property %s is not supported by CloudFormation provisioning", property)
	}
	return nil
}

func validateLambdaFunctionRequiredPropertyRemovals(props, prior map[string]any, packageType, logicalID string) error {
	required := []string{"Role", "Code"}
	if packageType == "Zip" {
		required = append(required, "Runtime", "Handler")
	}
	return validateRequiredPropertyRemovals(props, prior, logicalID, "AWS::Lambda::Function", required...)
}

func validateRequiredPropertyRemovals(props, prior map[string]any, logicalID, resourceType string, required ...string) error {
	for _, property := range required {
		if _, existed := prior[property]; !existed {
			continue
		}
		if value, present := props[property]; present && value != nil && fmt.Sprint(value) != "" {
			continue
		}
		resource := logicalID
		if resource == "" {
			resource = resourceType
		}
		return fmt.Errorf("Properties validation failed for resource %s with message: #: required key [%s] not found", resource, property)
	}
	return nil
}

func updateLambdaTags(ctx context.Context, router http.Handler, region, resourceARN string, stackTags, priorStackTags []Tag, rawTags, rawPrior any) (bool, error) {
	tags := mergeLambdaTags(stackTags, rawTags)
	prior := mergeLambdaTags(priorStackTags, rawPrior)
	added := make(map[string]string)
	for key, value := range tags {
		if prior[key] != value {
			added[key] = value
		}
	}
	removed := make([]string, 0)
	for key := range prior {
		if _, ok := tags[key]; !ok {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	path := "/2017-03-31/tags/" + url.PathEscape(resourceARN)
	applied := false
	if len(added) > 0 {
		data, err := json.Marshal(map[string]any{"Tags": added})
		if err != nil {
			return false, fmt.Errorf("lambda TagResource: marshal request: %w", err)
		}
		if _, err := internalRequest(ctx, router, region, http.MethodPost, path, "application/json", data); err != nil {
			return false, fmt.Errorf("lambda TagResource: %w", err)
		}
		applied = true
	}
	if len(removed) > 0 {
		query := url.Values{"tagKeys": removed}.Encode()
		if _, err := internalRequest(ctx, router, region, http.MethodDelete, path+"?"+query, "", nil); err != nil {
			return applied, fmt.Errorf("lambda UntagResource: %w", err)
		}
		applied = true
	}
	return applied, nil
}

func (h *lambdaFunctionHandler) updateConcurrency(ctx context.Context, router http.Handler, name string, props, prior map[string]any, rCtx *resolveContext) error {
	if reflect.DeepEqual(props["ReservedConcurrentExecutions"], prior["ReservedConcurrentExecutions"]) {
		return nil
	}
	if reserved, ok := props["ReservedConcurrentExecutions"]; ok {
		if err := putLambdaReservedConcurrency(ctx, router, rCtx.Region, name, reserved); err != nil {
			return err
		}
		return nil
	} else if _, existed := prior["ReservedConcurrentExecutions"]; existed {
		path := "/2017-10-31/functions/" + url.PathEscape(name) + "/concurrency"
		if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil); err != nil {
			return fmt.Errorf("lambda DeleteFunctionConcurrency: %w", err)
		}
	}
	return nil
}

// ── Lambda reserved concurrency and permission handlers ──────────────────

// putLambdaReservedConcurrency dispatches CloudFormation's function property
// through Lambda's separate reserved-concurrency API.
func putLambdaReservedConcurrency(ctx context.Context, router http.Handler, region, functionName string, reserved any) error {
	body, err := json.Marshal(map[string]any{"ReservedConcurrentExecutions": reserved})
	if err != nil {
		return fmt.Errorf("lambda PutFunctionConcurrency: marshal request: %w", err)
	}
	path := "/2017-10-31/functions/" + url.PathEscape(functionName) + "/concurrency"
	if _, err := internalRequest(ctx, router, region, http.MethodPut, path, "application/json", body); err != nil {
		return fmt.Errorf("lambda PutFunctionConcurrency: %w", err)
	}
	return nil
}

// lambdaPermissionHandler keeps CloudFormation thin: Lambda validates and
// stores the resource-policy statement, while CloudFormation owns only the
// generated statement ID needed to remove it later.
type lambdaPermissionHandler struct{}

func (h *lambdaPermissionHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	functionName, _ := props["FunctionName"].(string)
	statementID := rCtx.generatedNameWithin(100)
	body := map[string]any{"StatementId": statementID}
	for _, property := range []string{"Action", "EventSourceToken", "FunctionUrlAuthType", "InvokedViaFunctionUrl", "Principal", "PrincipalOrgID", "SourceAccount", "SourceArn"} {
		copyAnyProp(body, props, property, property)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("lambda AddPermission: marshal request: %w", err)
	}
	path := "/2015-03-31/functions/" + url.PathEscape(functionName) + "/policy"
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("lambda AddPermission: %w", err)
	}
	physicalID := functionName + "|" + statementID
	return physicalID, map[string]string{"Ref": statementID}, nil
}

func (h *lambdaPermissionHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "|", 2)
	if len(parts) != 2 {
		return nil
	}
	path := "/2015-03-31/functions/" + url.PathEscape(parts[0]) + "/policy/" + url.PathEscape(parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	if err != nil && (rec == nil || rec.Code != http.StatusNotFound) {
		return fmt.Errorf("lambda RemovePermission: %w", err)
	}
	return nil
}

// ── Lambda Alias handler ──────────────────────────────────────────────────

type lambdaAliasHandler struct{}

func (h *lambdaAliasHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	functionName, _ := props["FunctionName"].(string)
	aliasName, _ := props["Name"].(string)
	functionVersion, _ := props["FunctionVersion"].(string)
	if functionName == "" || aliasName == "" || functionVersion == "" {
		return "", nil, fmt.Errorf("Lambda Alias: FunctionName, Name, and FunctionVersion are required")
	}
	functionName = cfnLambdaFunctionName(functionName)
	body := map[string]any{"Name": aliasName, "FunctionVersion": functionVersion}
	if desc, ok := props["Description"]; ok {
		body["Description"] = desc
	}
	data, _ := json.Marshal(body)
	path := "/2015-03-31/functions/" + url.PathEscape(functionName) + "/aliases"
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("lambda CreateAlias: %w", err)
	}
	var resp map[string]any
	attrs := map[string]string{"Name": aliasName, "FunctionVersion": functionVersion}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if arn, ok := resp["AliasArn"].(string); ok {
			attrs["Ref"] = arn
			attrs["AliasArn"] = arn
		}
	}
	return functionName + ":" + aliasName, attrs, nil
}

func cfnLambdaFunctionName(identifier string) string {
	if idx := strings.Index(identifier, ":function:"); idx >= 0 {
		name := identifier[idx+len(":function:"):]
		if colonIdx := strings.IndexByte(name, ':'); colonIdx >= 0 {
			name = name[:colonIdx]
		}
		return name
	}
	return identifier
}

func (h *lambdaAliasHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, ":", 2)
	if len(parts) != 2 {
		return nil
	}
	path := "/2015-03-31/functions/" + url.PathEscape(parts[0]) + "/aliases/" + url.PathEscape(parts[1])
	_, _ = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return nil
}

// ── Lambda Function URL handler ───────────────────────────────────────────
//
// Thin translation to the internal CreateFunctionUrlConfig/DeleteFunctionUrlConfig
// REST endpoints (internal/services/lambda/handler_url.go) — no protocol
// logic duplicated here, per CONTRIBUTING's CloudFormation integration
// guidance.

type lambdaUrlHandler struct{}

func (h *lambdaUrlHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	targetArn, _ := props["TargetFunctionArn"].(string)
	authType, _ := props["AuthType"].(string)
	if targetArn == "" || authType == "" {
		return "", nil, fmt.Errorf("Lambda Url: TargetFunctionArn and AuthType are required")
	}
	functionName := cfnLambdaFunctionName(targetArn)
	qualifier, _ := props["Qualifier"].(string)

	body := map[string]any{"AuthType": authType}
	if cors, ok := props["Cors"]; ok {
		body["Cors"] = cors
	}
	if invokeMode, ok := props["InvokeMode"].(string); ok && invokeMode != "" {
		body["InvokeMode"] = invokeMode
	}
	data, _ := json.Marshal(body)
	path := "/2021-10-31/functions/" + url.PathEscape(functionName) + "/url"
	if qualifier != "" {
		path += "?Qualifier=" + url.QueryEscape(qualifier)
	}
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("lambda CreateFunctionUrlConfig: %w", err)
	}
	attrs := map[string]string{}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if functionArn, ok := resp["FunctionArn"].(string); ok {
			attrs["FunctionArn"] = functionArn
		}
		if functionURL, ok := resp["FunctionUrl"].(string); ok {
			// Real AWS templates consume this via Fn::GetAtt ... FunctionUrl;
			// used as Ref too since it's the only value most callers want.
			attrs["Ref"] = functionURL
			attrs["FunctionUrl"] = functionURL
		}
	}
	return functionName + ":" + qualifier, attrs, nil
}

func (h *lambdaUrlHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	functionName, qualifier, _ := strings.Cut(physicalID, ":")
	if functionName == "" {
		return nil
	}
	path := "/2021-10-31/functions/" + url.PathEscape(functionName) + "/url"
	if qualifier != "" {
		path += "?Qualifier=" + url.QueryEscape(qualifier)
	}
	_, _ = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return nil
}

func mergeLambdaTags(stackTags []Tag, rawResourceTags any) map[string]string {
	if len(stackTags) == 0 && rawResourceTags == nil {
		return nil
	}
	out := make(map[string]string, len(stackTags))
	for _, t := range stackTags {
		if strings.TrimSpace(t.Key) == "" {
			continue
		}
		out[t.Key] = t.Value
	}
	tags, ok := rawResourceTags.([]any)
	if !ok {
		if len(out) == 0 {
			return nil
		}
		return out
	}
	for _, item := range tags {
		kv, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key, _ := kv["Key"].(string)
		if strings.TrimSpace(key) == "" {
			continue
		}
		val, _ := kv["Value"].(string)
		// Resource-level tags override stack-level tags on key collision.
		out[key] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ── IAM Role handler ───────────────────────────────────────────────────────

type iamRoleHandler struct{}

func (h *iamRoleHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	roleName, _ := props["RoleName"].(string)
	if roleName == "" {
		roleName = rCtx.generatedName()
	}
	assumePolicy := "{}"
	if ap, ok := props["AssumeRolePolicyDocument"]; ok {
		b, _ := json.Marshal(ap)
		assumePolicy = string(b)
	}

	params := map[string]string{
		"Action":                   "CreateRole",
		"RoleName":                 roleName,
		"AssumeRolePolicyDocument": assumePolicy,
		"Version":                  "2010-05-08",
	}
	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("iam CreateRole: %w", err)
	}
	arn := extractXMLValue(rec.Body.String(), "Arn")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:iam::%s:role/%s", rCtx.AccountID, roleName)
	}
	roleID := extractXMLValue(rec.Body.String(), "RoleId")
	attrs := map[string]string{
		"Arn":      arn,
		"RoleId":   roleID,
		"RoleName": roleName,
	}
	// CloudFormation Ref on AWS::IAM::Role returns the role name, not the ARN.
	return roleName, attrs, nil
}

func (h *iamRoleHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	name := physicalID
	if i := strings.LastIndex(physicalID, "/"); i >= 0 {
		name = physicalID[i+1:]
	}
	params := map[string]string{
		"Action":   "DeleteRole",
		"RoleName": name,
		"Version":  "2010-05-08",
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *iamRoleHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name := physicalID
	if i := strings.LastIndex(physicalID, "/"); i >= 0 {
		name = physicalID[i+1:]
	}
	// RoleName is immutable in AWS.
	if n, ok := props["RoleName"].(string); ok && n != "" && n != name {
		return "", nil, errReplacementRequired
	}
	// AssumeRolePolicyDocument is the most commonly changed property in dev.
	if ap, ok := props["AssumeRolePolicyDocument"]; ok && ap != nil {
		b, _ := json.Marshal(ap)
		params := map[string]string{
			"Action":         "UpdateAssumeRolePolicy",
			"RoleName":       name,
			"PolicyDocument": string(b),
			"Version":        "2010-05-08",
		}
		if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
			return "", nil, fmt.Errorf("iam UpdateAssumeRolePolicy: %w", err)
		}
	}
	// Description is in-place via UpdateRole.
	if d, ok := props["Description"].(string); ok {
		params := map[string]string{
			"Action":      "UpdateRole",
			"RoleName":    name,
			"Description": d,
			"Version":     "2010-05-08",
		}
		_, _ = internalQuery(ctx, router, rCtx.Region, params)
	}
	arn := fmt.Sprintf("arn:aws:iam::%s:role/%s", rCtx.AccountID, name)
	return name, map[string]string{"Arn": arn, "RoleName": name}, nil
}

// ── CloudWatch Logs LogGroup handler ───────────────────────────────────────

type logsLogGroupHandler struct{}

func (h *logsLogGroupHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["LogGroupName"].(string)
	if name == "" {
		name = "/aws/cloudformation/" + rCtx.generatedName()
	}

	body := map[string]any{"logGroupName": name}
	_, err := internalJSON(ctx, router, rCtx.Region, "Logs_20140328.CreateLogGroup", body)
	if err != nil {
		return "", nil, fmt.Errorf("logs CreateLogGroup: %w", err)
	}
	if rd, ok := props["RetentionInDays"]; ok && rd != nil {
		body := map[string]any{
			"logGroupName":    name,
			"retentionInDays": rd,
		}
		if _, err := internalJSON(ctx, router, rCtx.Region, "Logs_20140328.PutRetentionPolicy", body); err != nil {
			cleanupBody := map[string]any{"logGroupName": name}
			if _, cleanupErr := internalJSON(ctx, router, rCtx.Region, "Logs_20140328.DeleteLogGroup", cleanupBody); cleanupErr != nil {
				return "", nil, fmt.Errorf("logs PutRetentionPolicy: %w; cleanup DeleteLogGroup: %v", err, cleanupErr)
			}
			return "", nil, fmt.Errorf("logs PutRetentionPolicy: %w", err)
		}
	}
	arn := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:*", rCtx.Region, rCtx.AccountID, name)
	attrs := map[string]string{
		"Arn":          arn,
		"LogGroupName": name,
	}
	return name, attrs, nil
}

func (h *logsLogGroupHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"logGroupName": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "Logs_20140328.DeleteLogGroup", body)
	return nil
}

func (h *logsLogGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// LogGroupName is immutable in AWS — a rename forces replacement.
	if n, ok := props["LogGroupName"].(string); ok && n != "" && n != physicalID {
		return "", nil, errReplacementRequired
	}
	// Apply RetentionInDays in place. Logs themselves are preserved.
	if rd, ok := props["RetentionInDays"]; ok && rd != nil {
		body := map[string]any{
			"logGroupName":    physicalID,
			"retentionInDays": rd,
		}
		if _, err := internalJSON(ctx, router, rCtx.Region, "Logs_20140328.PutRetentionPolicy", body); err != nil {
			return "", nil, fmt.Errorf("logs PutRetentionPolicy: %w", err)
		}
	} else if oldRetention, hadRetention := oldProps["RetentionInDays"]; hadRetention && oldRetention != nil {
		body := map[string]any{"logGroupName": physicalID}
		if _, err := internalJSON(ctx, router, rCtx.Region, "Logs_20140328.DeleteRetentionPolicy", body); err != nil {
			return "", nil, fmt.Errorf("logs DeleteRetentionPolicy: %w", err)
		}
	}
	arn := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:*", rCtx.Region, rCtx.AccountID, physicalID)
	return physicalID, map[string]string{"Arn": arn, "LogGroupName": physicalID}, nil
}

// ── SSM Parameter handler ──────────────────────────────────────────────────

type ssmParameterHandler struct{}

func (h *ssmParameterHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = "/" + rCtx.generatedName()
	}
	paramType, _ := props["Type"].(string)
	if paramType == "" {
		paramType = "String"
	}
	value, _ := props["Value"].(string)

	body := map[string]any{
		"Name":  name,
		"Type":  paramType,
		"Value": value,
	}
	_, err := internalJSON(ctx, router, rCtx.Region, "AmazonSSM.PutParameter", body)
	if err != nil {
		return "", nil, fmt.Errorf("ssm PutParameter: %w", err)
	}
	attrs := map[string]string{
		"Type":  paramType,
		"Value": value,
	}
	return name, attrs, nil
}

func (h *ssmParameterHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"Name": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AmazonSSM.DeleteParameter", body)
	return nil
}

func (h *ssmParameterHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Name is immutable in AWS — changing it forces replacement.
	if n, ok := props["Name"].(string); ok && n != "" && n != physicalID {
		return "", nil, errReplacementRequired
	}
	name := physicalID
	paramType, _ := props["Type"].(string)
	if paramType == "" {
		paramType = "String"
	}
	value, _ := props["Value"].(string)
	body := map[string]any{
		"Name":      name,
		"Type":      paramType,
		"Value":     value,
		"Overwrite": true,
	}
	if _, err := internalJSON(ctx, router, rCtx.Region, "AmazonSSM.PutParameter", body); err != nil {
		return "", nil, fmt.Errorf("ssm PutParameter (overwrite): %w", err)
	}
	return name, map[string]string{"Type": paramType, "Value": value}, nil
}

// ── Secrets Manager Secret handler ─────────────────────────────────────────

type secretsManagerSecretHandler struct{}

func (h *secretsManagerSecretHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Name is immutable. physicalID is the ARN; last segment after final ':'
	// is `<name>-<suffix>`, but for emulator purposes the user-supplied Name
	// is what we compare against. If it changed, replace.
	if n, ok := props["Name"].(string); ok && n != "" {
		// Extract the secret name embedded in the ARN.
		if i := strings.LastIndex(physicalID, ":"); i >= 0 {
			tail := physicalID[i+1:]
			// ARN tail is `<name>-<6 random>`; strip suffix when present.
			if j := strings.LastIndex(tail, "-"); j >= 0 && len(tail)-j == 7 {
				tail = tail[:j]
			}
			if tail != "" && tail != n {
				return "", nil, errReplacementRequired
			}
		}
	}

	// GenerateSecretString is deliberately absent here: AWS generates the value
	// once, at create, and an update that changes the generation settings does
	// not re-roll the live secret out from under whatever is already using it.
	body := map[string]any{"SecretId": physicalID}
	haveMutable := false
	if v, ok := props["Description"]; ok {
		body["Description"] = v
		haveMutable = true
	}
	if v, ok := props["SecretString"]; ok {
		body["SecretString"] = v
		haveMutable = true
	}
	if v, ok := props["KmsKeyId"]; ok {
		body["KmsKeyId"] = v
		haveMutable = true
	}
	if haveMutable {
		if _, err := internalJSON(ctx, router, rCtx.Region, "secretsmanager.UpdateSecret", body); err != nil {
			return "", nil, fmt.Errorf("secretsmanager UpdateSecret: %w", err)
		}
	}
	name, _ := props["Name"].(string)
	attrs := map[string]string{"Arn": physicalID}
	if name != "" {
		attrs["Name"] = name
	}
	return physicalID, attrs, nil
}

func (h *secretsManagerSecretHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = rCtx.generatedName()
	}
	body := map[string]any{"Name": name}
	sv, haveLiteral := props["SecretString"]
	haveLiteral = haveLiteral && sv != nil
	genRaw, haveGenerated := props["GenerateSecretString"]
	haveGenerated = haveGenerated && genRaw != nil
	switch {
	case haveLiteral && haveGenerated:
		return "", nil, fmt.Errorf("you can't specify both the SecretString and GenerateSecretString properties")
	case haveLiteral:
		body["SecretString"] = sv
	case haveGenerated:
		// Without this the secret is created with an AWSCURRENT version holding
		// nothing, and every GetSecretValue against it answers
		// ResourceNotFoundException — a value-less staged version is what an
		// in-flight rotation looks like. Failing the resource on a malformed
		// property beats falling back to that.
		gen, ok := genRaw.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("GenerateSecretString must be an object, got %T", genRaw)
		}
		value, err := generatedSecretString(ctx, router, rCtx, gen)
		if err != nil {
			return "", nil, err
		}
		body["SecretString"] = value
	}
	if desc, ok := props["Description"]; ok {
		body["Description"] = desc
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "secretsmanager.CreateSecret", body)
	if err != nil {
		return "", nil, fmt.Errorf("secretsmanager CreateSecret: %w", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if arn, ok := resp["ARN"].(string); ok {
			return arn, map[string]string{"Arn": arn, "Name": name}, nil
		}
	}
	return name, map[string]string{"Name": name}, nil
}

// generatedSecretString resolves AWS::SecretsManager::Secret's
// GenerateSecretString into the value CloudFormation stores as the secret's
// first version. The password itself comes from the service's own
// GetRandomPassword, so the exclusion rules live in one place.
func generatedSecretString(ctx context.Context, router http.Handler, rCtx *resolveContext, gen map[string]any) (string, error) {
	tmpl, _ := gen["SecretStringTemplate"].(string)
	key, _ := gen["GenerateStringKey"].(string)
	if (tmpl == "") != (key == "") {
		return "", fmt.Errorf("GenerateSecretString: SecretStringTemplate and GenerateStringKey must be specified together")
	}

	body := map[string]any{}
	if length, ok := cfnFloatProp(gen, "PasswordLength"); ok {
		body["PasswordLength"] = int64(length)
	}
	if excluded, ok := gen["ExcludeCharacters"].(string); ok {
		body["ExcludeCharacters"] = excluded
	}
	// Template values may arrive as JSON booleans or as the strings a raw
	// template writes, which is what asBool exists to smooth over.
	for _, flag := range []string{
		"ExcludeNumbers",
		"ExcludePunctuation",
		"ExcludeUppercase",
		"ExcludeLowercase",
		"IncludeSpace",
		"RequireEachIncludedType",
	} {
		if v, ok := gen[flag]; ok {
			body[flag] = asBool(v)
		}
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "secretsmanager.GetRandomPassword", body)
	if err != nil {
		return "", fmt.Errorf("secretsmanager GetRandomPassword: %w", err)
	}
	var resp struct {
		RandomPassword string `json:"RandomPassword"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", fmt.Errorf("secretsmanager GetRandomPassword: %w", err)
	}
	if resp.RandomPassword == "" {
		return "", fmt.Errorf("secretsmanager GetRandomPassword returned no password")
	}
	if tmpl == "" {
		return resp.RandomPassword, nil
	}

	// With a template the secret is that JSON object with the generated
	// password added under GenerateStringKey, which is how the CDK's
	// `{ username, password }` database credentials are built.
	var fields map[string]any
	if err := json.Unmarshal([]byte(tmpl), &fields); err != nil {
		return "", fmt.Errorf("GenerateSecretString: SecretStringTemplate is not a JSON object: %w", err)
	}
	fields[key] = resp.RandomPassword
	out, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("GenerateSecretString: %w", err)
	}
	return string(out), nil
}

func (h *secretsManagerSecretHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{
		"SecretId":                   physicalID,
		"ForceDeleteWithoutRecovery": true,
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "secretsmanager.DeleteSecret", body)
	return nil
}

// ── Custom resource handler (Lambda-backed) ────────────────────────────────

// customResourceHandler invokes a Lambda function using the CloudFormation
// custom resource protocol. It supports both Custom::* types and
// AWS::CloudFormation::CustomResource.
type customResourceHandler struct {
	p *provisioner
}

// cfnCustomResourceRequest is the payload sent to the Lambda backing function.
// See https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/crpg-ref-requesttypes.html
type cfnCustomResourceRequest struct {
	RequestType           string         `json:"RequestType"`
	ResponseURL           string         `json:"ResponseURL,omitempty"`
	StackId               string         `json:"StackId"`
	RequestId             string         `json:"RequestId"`
	PhysicalResourceId    string         `json:"PhysicalResourceId,omitempty"`
	ResourceProperties    map[string]any `json:"ResourceProperties"`
	OldResourceProperties map[string]any `json:"OldResourceProperties,omitempty"`
}

// cfnCustomResourceResponse is the expected response from the Lambda function.
type cfnCustomResourceResponse struct {
	Status             string            `json:"Status"`
	PhysicalResourceId string            `json:"PhysicalResourceId"`
	Data               map[string]string `json:"Data"`
	Reason             string            `json:"Reason"`
}

func (h *customResourceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return h.invoke(ctx, router, cfg, "Create", "", props, nil, rCtx)
}

func (h *customResourceHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, _, err := h.invoke(ctx, router, cfg, "Delete", physicalID, nil, nil, rCtx)
	return err
}

func (h *customResourceHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return h.invoke(ctx, router, cfg, "Update", physicalID, props, map[string]any{}, rCtx)
}

func (h *customResourceHandler) invoke(ctx context.Context, router http.Handler, _ *config.Config, reqType, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	serviceToken, _ := props["ServiceToken"].(string)
	if serviceToken == "" {
		return "", nil, fmt.Errorf("custom resource missing ServiceToken")
	}

	// Extract function name from ARN (arn:aws:lambda:region:account:function:name).
	funcName := serviceToken
	if parts := strings.Split(serviceToken, ":"); len(parts) >= 7 {
		funcName = parts[6]
	}

	// stubResult returns a synthetic physical ID when Lambda invocation cannot
	// complete (Docker unavailable, function not found, etc.).
	stubResult := func() (string, map[string]string, error) {
		id := fmt.Sprintf("custom-resource-%s-%d", rCtx.StackName, len(rCtx.Resources))
		return id, nil, nil
	}

	payload := cfnCustomResourceRequest{
		RequestType:           reqType,
		StackId:               rCtx.StackID,
		RequestId:             uuid.New().String(),
		PhysicalResourceId:    physicalID,
		ResourceProperties:    props,
		OldResourceProperties: oldProps,
		// ResponseURL is empty — the emulator does not use the S3 callback
		// protocol; it reads the Lambda return value directly instead.
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("custom resource marshal: %w", err)
	}

	path := "/2015-03-31/functions/" + url.PathEscape(funcName) + "/invocations"
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		// Lambda function not found or invocation failed — degrade to stub.
		h.p.log.Warn("cfn: custom resource invoke failed, creating stub",
			zap.String("serviceToken", serviceToken),
			zap.Error(err))
		return stubResult()
	}

	// If the Lambda runtime returned a function error (e.g. Docker unavailable),
	// degrade gracefully — treat as a no-op stub so the stack can still deploy.
	if funcErr := rec.Header().Get("X-Amz-Function-Error"); funcErr != "" {
		h.p.log.Warn("cfn: custom resource Lambda returned function error, creating stub",
			zap.String("serviceToken", serviceToken),
			zap.String("functionError", funcErr))
		return stubResult()
	}

	var resp cfnCustomResourceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("custom resource response parse: %w", err)
	}
	if resp.Status != "SUCCESS" {
		reason := resp.Reason
		if reason == "" {
			reason = "custom resource returned FAILED"
		}
		return "", nil, fmt.Errorf("custom resource failed: %s", reason)
	}

	return resp.PhysicalResourceId, resp.Data, nil
}

// ── Nested stack handler ───────────────────────────────────────────────────

// nestedStackHandler provisions an AWS::CloudFormation::Stack resource by
// fetching the child template, creating a child Stack, and provisioning its
// resources synchronously within the parent's goroutine. No additional
// goroutines are spawned — the parent blocks until the child completes.
type nestedStackHandler struct {
	p *provisioner
}

func (h *nestedStackHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	templateURL, _ := props["TemplateURL"].(string)
	if templateURL == "" {
		return "", nil, fmt.Errorf("nested stack missing TemplateURL")
	}

	// Fetch the child template via internal HTTP (supports S3 URLs served by
	// our own router or any reachable URL).
	tmplBody, err := h.fetchTemplate(ctx, router, rCtx.Region, templateURL)
	if err != nil {
		return "", nil, fmt.Errorf("nested stack fetch template: %w", err)
	}

	tmpl, err := parseTemplate(tmplBody)
	if err != nil {
		return "", nil, fmt.Errorf("nested stack parse template: %w", err)
	}

	// Build a unique child stack name from the parent name and a short UUID.
	childName := rCtx.StackName + "-NestedStack-" + uuid.New().String()[:8]

	childStackID := fmt.Sprintf("arn:aws:cloudformation:%s:%s:stack/%s/%s",
		rCtx.Region, cfg.AccountID, childName, uuid.NewString())

	// Build child parameters from Properties.Parameters map.
	var childParams []Parameter
	if paramMap, ok := props["Parameters"].(map[string]any); ok {
		for k, v := range paramMap {
			childParams = append(childParams, Parameter{
				Key:   k,
				Value: cfnScalarString(v),
			})
		}
		// Sort for deterministic ordering.
		sort.Slice(childParams, func(i, j int) bool {
			return childParams[i].Key < childParams[j].Key
		})
	}

	// Determine the root stack: if the parent is itself nested, inherit its root;
	// otherwise the parent is the root.
	rootID := rCtx.StackID
	if parentStack, _ := h.p.store.getStack(h.p.regionCtx(rCtx.Region), rCtx.StackName); parentStack != nil && parentStack.RootID != "" {
		rootID = parentStack.RootID
	}

	now := h.p.clk.Now()
	childStack := &Stack{
		StackName:     childName,
		StackID:       childStackID,
		ParentStackID: rCtx.StackID,
		RootID:        rootID,
		Status:        StatusCreateInProgress,
		Parameters:    childParams,
		CreatedAt:     now,
		Region:        rCtx.Region,
		TemplateBody:  tmplBody,
		Tags:          mergeNestedStackTags(rCtx.StackTags, props["Tags"]),
	}

	// Store the child stack so it appears in ListStacks/DescribeStacks.
	storeCtx := h.p.regionCtx(rCtx.Region)
	if err := h.p.store.putStack(storeCtx, childStack); err != nil {
		return "", nil, fmt.Errorf("nested stack store: %w", err)
	}

	// Provision child resources synchronously — no new goroutine.
	h.p.provisionStackResources(childStack, tmpl)

	if childStack.Status != StatusCreateComplete {
		return "", nil, fmt.Errorf("nested stack %s failed: %s", childName, childStack.StatusReason)
	}

	// Build attributes from child outputs (Fn::GetAtt on nested stacks
	// uses "Outputs.<OutputKey>" as the attribute name).
	attrs := make(map[string]string)
	for _, out := range childStack.Outputs {
		attrs["Outputs."+out.Key] = out.Value
	}

	return childStackID, attrs, nil
}

func (h *nestedStackHandler) Delete(ctx context.Context, _ http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	// physicalID is the child stack's ARN. Extract the stack name from it:
	// format: arn:aws:cloudformation:<region>:<account>:stack/<name>/<uuid>
	childName := stackNameFromARN(physicalID)
	if childName == "" {
		return nil
	}

	storeCtx := h.p.regionCtx(rCtx.Region)
	childStack, _ := h.p.store.getStack(storeCtx, childName)
	if childStack == nil {
		return nil // already deleted or not found
	}

	childStack.Status = StatusDeleteInProgress
	h.p.deleteStackResources(childStack)
	return nil
}

func (h *nestedStackHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	childName := stackNameFromARN(physicalID)
	if childName == "" {
		return physicalID, nil, nil
	}

	storeCtx := h.p.regionCtx(rCtx.Region)
	childStack, _ := h.p.store.getStack(storeCtx, childName)
	if childStack == nil {
		return "", nil, fmt.Errorf("nested stack %s not found", childName)
	}

	templateURL, _ := props["TemplateURL"].(string)
	if templateURL == "" {
		return "", nil, fmt.Errorf("nested stack missing TemplateURL")
	}

	tmplBody, err := h.fetchTemplate(ctx, router, rCtx.Region, templateURL)
	if err != nil {
		return "", nil, fmt.Errorf("nested stack fetch template: %w", err)
	}

	tmpl, err := parseTemplate(tmplBody)
	if err != nil {
		return "", nil, fmt.Errorf("nested stack parse template: %w", err)
	}

	childStack.Parameters = nil
	if paramMap, ok := props["Parameters"].(map[string]any); ok {
		for k, v := range paramMap {
			childStack.Parameters = append(childStack.Parameters, Parameter{Key: k, Value: cfnScalarString(v)})
		}
		sort.Slice(childStack.Parameters, func(i, j int) bool {
			return childStack.Parameters[i].Key < childStack.Parameters[j].Key
		})
	}

	childStack.TemplateBody = tmplBody
	childStack.Status = StatusUpdateInProgress
	previousChildTags := append([]Tag(nil), childStack.Tags...)
	if previousChildTags == nil {
		previousChildTags = mergeNestedStackTags(rCtx.PreviousStackTags, oldProps["Tags"])
	}
	childStack.Tags = mergeNestedStackTags(rCtx.StackTags, props["Tags"])

	if err := h.p.store.putStack(storeCtx, childStack); err != nil {
		return "", nil, fmt.Errorf("nested stack update store: %w", err)
	}

	h.p.updateStackResources(childStack, tmpl, previousChildTags)

	if childStack.Status != StatusUpdateComplete {
		return "", nil, fmt.Errorf("nested stack %s update failed: %s", childName, childStack.StatusReason)
	}

	attrs := make(map[string]string)
	for _, out := range childStack.Outputs {
		attrs["Outputs."+out.Key] = out.Value
	}

	return childStack.StackID, attrs, nil
}

func mergeNestedStackTags(parent []Tag, raw any) []Tag {
	merged := mergeLambdaTags(parent, raw)
	out := make([]Tag, 0, len(merged))
	for key, value := range merged {
		out = append(out, Tag{Key: key, Value: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// stackNameFromARN extracts the stack name from a CloudFormation stack ARN.
// ARN format: arn:aws:cloudformation:<region>:<account>:stack/<name>/<uuid>.
func stackNameFromARN(arn string) string {
	const prefix = ":stack/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	rest := arn[i+len(prefix):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// fetchTemplate retrieves a template body from a URL. If the URL points at our
// own emulator (same host), it dispatches an internal request to avoid a real
// network round-trip. Otherwise it performs a standard HTTP GET.
func (h *nestedStackHandler) fetchTemplate(ctx context.Context, router http.Handler, region, templateURL string) (string, error) {
	// Parse the URL to extract the path for internal dispatch.
	u, err := url.Parse(templateURL)
	if err != nil {
		return "", err
	}

	// Always try internal dispatch first — in tests the URL host points at
	// the httptest server which is backed by the same router.
	rec, err := internalRequest(ctx, router, region, http.MethodGet, u.Path, "", nil)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", templateURL, err)
	}
	return rec.Body.String(), nil
}

// ── XML extraction helper ──────────────────────────────────────────────────

// extractXMLValue extracts the text content of a simple XML element.
// This is intentionally simple — not a real XML parser.
func extractXMLValue(xml, tag string) string {
	start := fmt.Sprintf("<%s>", tag)
	end := fmt.Sprintf("</%s>", tag)
	i := strings.Index(xml, start)
	if i < 0 {
		return ""
	}
	i += len(start)
	j := strings.Index(xml[i:], end)
	if j < 0 {
		return ""
	}
	return xml[i : i+j]
}

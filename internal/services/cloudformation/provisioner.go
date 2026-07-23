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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

		props := resolveAllProperties(res.Properties, rCtx)
		propsHash := hashProps(props)
		resStart := p.clk.Now()
		physID, provErr := p.provisionResource(ctx, logicalID, res, props, rCtx)
		resElapsed := p.clk.Since(resStart)
		now := p.clk.Now()
		if provErr != nil {
			// Record failed resource state and emit CREATE_FAILED with reason.
			stack.Resources = append(stack.Resources, StackResource{
				LogicalID:    logicalID,
				Type:         res.Type,
				Status:       ResourceCreateFailed,
				StatusReason: provErr.Error(),
				Timestamp:    now,
			})
			p.recordEvent(ctx, stack, logicalID, "", res.Type, ResourceCreateFailed, provErr.Error())

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
			Properties:          props,
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

func (p *provisioner) updateStack(stack *Stack, tmpl *Template, onComplete stackCompletionFunc) {
	done := make(chan struct{})
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer close(done)
		p.updateStackResources(stack, tmpl)
		if onComplete != nil {
			onComplete(p.regionCtx(stack.Region), stack)
		}
	}()
	p.awaitBriefly(done)
}

func (p *provisioner) updateStackResources(stack *Stack, tmpl *Template) {
	ctx := p.regionCtx(stack.Region)

	rCtx := p.buildResolveContext(stack, tmpl)

	// Emit the initial stack UPDATE_IN_PROGRESS event.
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusUpdateInProgress, "User Initiated")

	// For simplicity: treat all resources as requiring re-creation.
	// Build existing map.
	existing := map[string]StackResource{}
	for _, r := range stack.Resources {
		existing[r.LogicalID] = r
	}

	order, err := topoSort(tmpl.Resources)
	if err != nil {
		p.failStack(ctx, stack, StatusUpdateFailed, fmt.Sprintf("dependency cycle: %v", err))
		return
	}

	var newResources []StackResource

	for _, logicalID := range order {
		if ctx.Err() != nil {
			p.failStack(ctx, stack, StatusUpdateFailed, "cancelled")
			return
		}
		res := tmpl.Resources[logicalID]

		props := resolveAllProperties(res.Properties, rCtx)
		propsHash := hashProps(props)

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

			if old.PropertiesHash == "" || old.PropertiesHash == propsHash {
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
					Properties:          props,
					DeletionPolicy:      res.DeletionPolicy,
					UpdateReplacePolicy: res.UpdateReplacePolicy,
				})
				delete(existing, logicalID)
				continue
			}

			// Properties changed — attempt update.
			p.recordEvent(ctx, stack, logicalID, old.PhysicalID, res.Type, ResourceUpdateInProgress, "")
			physID, updErr := p.updateResource(ctx, logicalID, res, props, old.PhysicalID, &old, rCtx)
			now := p.clk.Now()
			if updErr != nil {
				newResources = append(newResources, StackResource{
					LogicalID:    logicalID,
					PhysicalID:   old.PhysicalID,
					Type:         res.Type,
					Status:       ResourceUpdateFailed,
					StatusReason: updErr.Error(),
					Timestamp:    now,
				})
				p.recordEvent(ctx, stack, logicalID, old.PhysicalID, res.Type, ResourceUpdateFailed, updErr.Error())
				if stack.DisableRollback {
					p.failStack(ctx, stack, StatusUpdateFailed,
						fmt.Sprintf("resource %s failed: %v", logicalID, updErr))
					return
				}
				p.rollbackUpdate(ctx, stack, newResources, existing, rCtx,
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
				Properties:          props,
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

		physID, provErr := p.provisionResource(ctx, logicalID, res, props, rCtx)
		now := p.clk.Now()
		if provErr != nil {
			newResources = append(newResources, StackResource{
				LogicalID:    logicalID,
				Type:         res.Type,
				Status:       ResourceCreateFailed,
				StatusReason: provErr.Error(),
				Timestamp:    now,
			})
			p.recordEvent(ctx, stack, logicalID, "", res.Type, ResourceCreateFailed, provErr.Error())
			if stack.DisableRollback {
				p.failStack(ctx, stack, StatusUpdateFailed,
					fmt.Sprintf("resource %s failed: %v", logicalID, provErr))
				return
			}
			// Roll back: delete newly created resources (those not in `existing`)
			// in reverse order, then restore the previous resource list.
			p.rollbackUpdate(ctx, stack, newResources, existing, rCtx,
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
			Properties:          props,
			DeletionPolicy:      res.DeletionPolicy,
			UpdateReplacePolicy: res.UpdateReplacePolicy,
		})
		rCtx.Resources[logicalID] = physID
		p.recordEvent(ctx, stack, logicalID, physID, res.Type, ResourceCreateComplete, "")
		p.publishResourceEvent(ctx, events.CFNResourceProvisioned, stack.StackName, logicalID, res.Type, physID)
	}

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
		p.deleteResource(ctx, logicalID, old.Type, old.PhysicalID, rCtx)
		p.recordEvent(ctx, stack, logicalID, old.PhysicalID, old.Type, ResourceDeleteComplete, "")
		p.publishResourceEvent(ctx, events.CFNResourceDeleted, stack.StackName, logicalID, old.Type, old.PhysicalID)
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
		p.deleteResource(ctx, r.LogicalID, r.Type, r.PhysicalID, rCtx)
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
		return "", err
	}
	// Store attributes for Fn::GetAtt resolution.
	if len(attrs) > 0 {
		if rCtx.Attributes == nil {
			rCtx.Attributes = make(map[string]map[string]string)
		}
		rCtx.Attributes[logicalID] = attrs
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
// supports it (resourceUpdater interface). Otherwise it falls back to
// delete + create so behaviour is correct even for handlers that haven't
// implemented Update yet. Returns the new physical ID and an error.
//
// If the resource has UpdateReplacePolicy=Retain (or Snapshot), and the
// handler does not support in-place updates, the old physical ID is orphaned
// instead of being deleted. Real CloudFormation does the same: the new
// resource is created and the old one is left behind, no longer tracked
// by the stack.
func (p *provisioner) updateResource(ctx context.Context, logicalID string, res TemplateResource, props map[string]any, oldPhysicalID string, oldResource *StackResource, rCtx *resolveContext) (string, error) {
	p.mu.Lock()
	router := p.router
	p.mu.Unlock()
	if router == nil {
		return "", fmt.Errorf("router not initialised")
	}

	handler, ok := p.resolveHandler(res.Type)
	if !ok {
		// Unknown resource type — keep stub physical ID, no-op.
		return oldPhysicalID, nil
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
			if len(attrs) > 0 {
				if rCtx.Attributes == nil {
					rCtx.Attributes = make(map[string]map[string]string)
				}
				rCtx.Attributes[logicalID] = attrs
			}
			return physID, nil
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

	// Replacement path. Honour UpdateReplacePolicy=Retain by orphaning the
	// old resource instead of deleting it.
	retain := false
	if oldResource != nil {
		retain = oldResource.shouldRetainOnReplace()
	}
	if retain {
		p.log.Info("cfn: retaining old resource on replacement (UpdateReplacePolicy=Retain)",
			zap.String("type", res.Type),
			zap.String("logicalId", logicalID),
			zap.String("orphanedPhysicalId", oldPhysicalID))
	} else {
		// AWS fidelity: do not silently push through to Create if Delete
		// fails. Real CloudFormation aborts the replacement and surfaces
		// the failure (the alternative — calling Create against the still-
		// present old physical resource — would 409 with EntityAlreadyExists
		// for any service that enforces uniqueness, which is exactly the
		// confusing failure mode users hit on rebootstrap).
		if err := handler.Delete(ctx, router, p.cfg, oldPhysicalID, rCtx); err != nil {
			return "", fmt.Errorf("delete during replace failed: %w", err)
		}
	}
	return p.provisionResource(ctx, logicalID, res, props, rCtx)
}

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

// deleteResource tears down a provisioned resource.
func (p *provisioner) deleteResource(ctx context.Context, logicalID, resType, physicalID string, rCtx *resolveContext) {
	p.mu.Lock()
	router := p.router
	p.mu.Unlock()
	if router == nil {
		return
	}

	handler, ok := p.resolveHandler(resType)
	if !ok {
		return // stub resources have nothing to delete
	}

	if err := handler.Delete(ctx, router, p.cfg, physicalID, rCtx); err != nil {
		p.log.Warn("cfn: failed to delete resource",
			zap.String("type", resType),
			zap.String("logicalId", logicalID),
			zap.String("physicalId", physicalID),
			zap.Error(err))
	}
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
			params[name] = def.Default
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
	}
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
		if r.Status != ResourceCreateComplete || r.PhysicalID == "" {
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
// It deletes any newly provisioned resources (those not present before the
// update) in reverse order, then restores the previous resource list and marks
// the stack UPDATE_ROLLBACK_COMPLETE.
func (p *provisioner) rollbackUpdate(ctx context.Context, stack *Stack, attempted []StackResource, previous map[string]StackResource, rCtx *resolveContext, reason string) {
	stack.Status = StatusUpdateRollbackInProgress
	stack.StatusReason = reason
	p.recordEvent(ctx, stack, stack.StackName, stack.StackID, "AWS::CloudFormation::Stack", StatusUpdateRollbackInProgress, reason)
	p.publishStackEvent(ctx, events.CFNStackFailed, stack)

	rollbackFailed := false
	// Track resources that were created during the failed update but could
	// not be deleted during rollback. Real CloudFormation keeps these in the
	// stack's resource list with status DELETE_FAILED so subsequent
	// operations can see them (instead of treating them as new and double-
	// creating against an orphaned service-side resource).
	var orphaned []StackResource
	// Delete newly created resources in reverse order. Resources that existed
	// before the update (present in `previous`) are left untouched.
	for i := len(attempted) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			rollbackFailed = true
			break
		}
		r := attempted[i]
		if _, wasExisting := previous[r.LogicalID]; wasExisting {
			continue // was pre-existing — do not delete
		}
		if r.Status != ResourceCreateComplete || r.PhysicalID == "" {
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
	for _, res := range previous {
		restored = append(restored, res)
	}
	restored = append(restored, orphaned...)
	stack.Resources = restored

	if rollbackFailed {
		stack.Status = StatusUpdateRollbackFailed
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

// errReplacementRequired is returned by an Update implementation when one or
// more changed properties cannot be applied in place (for example renaming an
// SQS queue or changing a DynamoDB key schema). The provisioner reacts by
// taking the replacement path — create the new resource and delete the old
// one — which mirrors how AWS CloudFormation handles "Replacement: Yes"
// property changes.
var errReplacementRequired = errors.New("cfn: replacement required")

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
	"AWS::Lambda::EventSourceMapping": &lambdaEventSourceMappingHandler{},
	"AWS::Lambda::Permission":         &stubResourceHandler{},
	"AWS::Lambda::LayerVersion":       &lambdaLayerVersionHandler{},
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
	"AWS::Route53::HostedZone": &route53HostedZoneHandler{},
	"AWS::Route53::RecordSet":  &route53RecordSetHandler{},
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
		queueName = fmt.Sprintf("%s-%s", rCtx.StackName, "Queue")
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

type snsTopicHandler struct{}

func (h *snsTopicHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// physicalID is the topic ARN; last segment is the topic name.
	oldName := physicalID
	if i := strings.LastIndex(physicalID, ":"); i >= 0 {
		oldName = physicalID[i+1:]
	}
	if n, ok := props["TopicName"].(string); ok && n != "" && n != oldName {
		return "", nil, errReplacementRequired
	}

	// Apply mutable attributes via SetTopicAttributes (one call per attr name).
	setAttr := func(name string, val any) error {
		params := map[string]string{
			"Action":         "SetTopicAttributes",
			"TopicArn":       physicalID,
			"AttributeName":  name,
			"AttributeValue": fmt.Sprintf("%v", val),
			"Version":        "2010-03-31",
		}
		_, err := internalQuery(ctx, router, rCtx.Region, params)
		return err
	}
	if v, ok := props["DisplayName"]; ok && v != nil {
		if err := setAttr("DisplayName", v); err != nil {
			return "", nil, fmt.Errorf("sns SetTopicAttributes(DisplayName): %w", err)
		}
	}
	if v, ok := props["KmsMasterKeyId"]; ok && v != nil {
		if err := setAttr("KmsMasterKeyId", v); err != nil {
			return "", nil, fmt.Errorf("sns SetTopicAttributes(KmsMasterKeyId): %w", err)
		}
	}
	return physicalID, map[string]string{"TopicName": oldName, "TopicArn": physicalID}, nil
}

func (h *snsTopicHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	topicName, _ := props["TopicName"].(string)
	if topicName == "" {
		topicName = fmt.Sprintf("%s-Topic", rCtx.StackName)
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
	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("sns Subscribe: %w", err)
	}
	arn := extractXMLValue(rec.Body.String(), "SubscriptionArn")
	if arn == "" {
		arn = fmt.Sprintf("stub-sub-%s-%d", rCtx.StackName, len(rCtx.Resources))
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
	return "", nil, errReplacementRequired
}

// ── S3 Bucket handler ──────────────────────────────────────────────────────

type s3BucketHandler struct{}

func (h *s3BucketHandler) Update(_ context.Context, _ http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// BucketName is immutable.
	if n, ok := props["BucketName"].(string); ok && n != "" && n != physicalID {
		return "", nil, errReplacementRequired
	}
	// Other sub-resources (Tagging, CORS, Versioning, Policy, Encryption,
	// PublicAccessBlock, Lifecycle, etc.) are in-place mutations on the live
	// bucket in real AWS. The emulator does not yet drive those sub-resource
	// PUT calls from CFN — accept the change and keep the bucket. Users who
	// need the configuration to take effect can call the S3 API directly.
	arn := fmt.Sprintf("arn:aws:s3:::%s", physicalID)
	attrs := map[string]string{
		"Arn":                arn,
		"BucketName":         physicalID,
		"DomainName":         fmt.Sprintf("%s.s3.amazonaws.com", physicalID),
		"RegionalDomainName": fmt.Sprintf("%s.s3.%s.amazonaws.com", physicalID, rCtx.Region),
	}
	return physicalID, attrs, nil
}

func (h *s3BucketHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	bucketName, _ := props["BucketName"].(string)
	if bucketName == "" {
		bucketName = fmt.Sprintf("%s-bucket-%d", strings.ToLower(rCtx.StackName), len(rCtx.Resources))
	}

	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, "/"+bucketName, "", nil)
	if err != nil {
		return "", nil, fmt.Errorf("s3 CreateBucket: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:s3:::%s", bucketName)
	attrs := map[string]string{
		"Arn":                arn,
		"BucketName":         bucketName,
		"DomainName":         fmt.Sprintf("%s.s3.amazonaws.com", bucketName),
		"RegionalDomainName": fmt.Sprintf("%s.s3.%s.amazonaws.com", bucketName, rCtx.Region),
	}
	return bucketName, attrs, nil
}

func (h *s3BucketHandler) Delete(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, rCtx *resolveContext) error {
	_, _ = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/"+physicalID, "", nil)
	return nil
}

// ── DynamoDB Table handler ─────────────────────────────────────────────────

type dynamodbTableHandler struct{}

func (h *dynamodbTableHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// TableName and KeySchema are immutable.
	if n, ok := props["TableName"].(string); ok && n != "" && n != physicalID {
		return "", nil, errReplacementRequired
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
	if haveMutable {
		if _, err := internalJSON(ctx, router, rCtx.Region, "DynamoDB_20120810.UpdateTable", reqBody); err != nil {
			return "", nil, fmt.Errorf("dynamodb UpdateTable: %w", err)
		}
	}
	// TimeToLiveSpecification is a separate API call.
	if v, ok := props["TimeToLiveSpecification"].(map[string]any); ok {
		ttlBody := map[string]any{
			"TableName":               physicalID,
			"TimeToLiveSpecification": v,
		}
		if _, err := internalJSON(ctx, router, rCtx.Region, "DynamoDB_20120810.UpdateTimeToLive", ttlBody); err != nil {
			// Non-fatal: emulator may not support UpdateTimeToLive yet.
			_ = err
		}
	}
	return physicalID, map[string]string{"TableName": physicalID}, nil
}

func (h *dynamodbTableHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	tableName, _ := props["TableName"].(string)
	if tableName == "" {
		tableName = fmt.Sprintf("%s-Table", rCtx.StackName)
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
		funcName = fmt.Sprintf("%s-Function", rCtx.StackName)
	}

	// The Lambda handler expects Code.ZipFile as base64-encoded bytes ([]byte).
	// CFN templates provide it as a plain string, so we must encode it.
	code, _ := props["Code"].(map[string]any)
	if zf, ok := code["ZipFile"].(string); ok {
		code["ZipFile"] = base64.StdEncoding.EncodeToString([]byte(zf))
	}

	body := map[string]any{
		"FunctionName": funcName,
		"Runtime":      props["Runtime"],
		"Handler":      props["Handler"],
		"Role":         props["Role"],
		"Code":         code,
	}
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
	if tagMap := mergeLambdaTags(rCtx.StackTags, props["Tags"]); len(tagMap) > 0 {
		body["Tags"] = tagMap
	}

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/2015-03-31/functions", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("lambda CreateFunction: %w", err)
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
	_, _ = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/2015-03-31/functions/"+name, "", nil)
	return nil
}

// Update implements in-place updates for AWS::Lambda::Function. Code changes
// are dispatched to UpdateFunctionCode (PUT /functions/{name}/code) and the
// remaining mutable configuration is dispatched to UpdateFunctionConfiguration
// (PUT /functions/{name}/configuration). The function name is immutable on
// real AWS, so when it changes the provisioner falls back to replacement.
func (h *lambdaFunctionHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name := physicalID
	if i := strings.LastIndex(physicalID, ":"); i >= 0 {
		name = physicalID[i+1:]
	}
	// FunctionName is immutable: a rename forces replacement.
	if newName, _ := props["FunctionName"].(string); newName != "" && newName != name {
		return "", nil, errReplacementRequired
	}

	// 1. Update code if present. CFN templates supply ZipFile as a plain string;
	//    UpdateFunctionCode expects base64-encoded bytes (matches Create).
	if code, ok := props["Code"].(map[string]any); ok && len(code) > 0 {
		body := map[string]any{}
		if zf, ok := code["ZipFile"].(string); ok {
			body["ZipFile"] = base64.StdEncoding.EncodeToString([]byte(zf))
		}
		if v, ok := code["S3Bucket"]; ok {
			body["S3Bucket"] = v
		}
		if v, ok := code["S3Key"]; ok {
			body["S3Key"] = v
		}
		if v, ok := code["ImageUri"]; ok {
			body["ImageUri"] = v
		}
		if len(body) > 0 {
			data, _ := json.Marshal(body)
			rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, "/2015-03-31/functions/"+name+"/code", "application/json", data)
			if err != nil {
				return "", nil, fmt.Errorf("lambda UpdateFunctionCode: %w", err)
			}
			if rec.Code >= 400 {
				return "", nil, fmt.Errorf("lambda UpdateFunctionCode: status %d: %s", rec.Code, rec.Body.String())
			}
		}
	}

	// 2. Update configuration. Always send at least an empty body so that
	//    cleared optional fields propagate; AWS treats omitted fields as
	//    "no change", which matches our handler's semantics.
	cfgBody := map[string]any{}
	for _, k := range []string{"Runtime", "Handler", "Role", "Description", "Environment", "Timeout", "MemorySize", "Layers", "LoggingConfig"} {
		if v, ok := props[k]; ok {
			cfgBody[k] = v
		}
	}
	var arn string
	if len(cfgBody) > 0 {
		data, _ := json.Marshal(cfgBody)
		rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, "/2015-03-31/functions/"+name+"/configuration", "application/json", data)
		if err != nil {
			return "", nil, fmt.Errorf("lambda UpdateFunctionConfiguration: %w", err)
		}
		if rec.Code >= 400 {
			return "", nil, fmt.Errorf("lambda UpdateFunctionConfiguration: status %d: %s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
			if a, ok := resp["FunctionArn"].(string); ok {
				arn = a
			}
		}
	}
	attrs := map[string]string{"FunctionName": name}
	if arn != "" {
		attrs["Arn"] = arn
	}
	return name, attrs, nil
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
	attrs := map[string]string{"Ref": aliasName, "Name": aliasName, "FunctionVersion": functionVersion}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if arn, ok := resp["AliasArn"].(string); ok {
			attrs["Arn"] = arn
			attrs["AliasArn"] = arn
		}
	}
	return functionName + ":" + aliasName, attrs, nil
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
		roleName = fmt.Sprintf("%s-Role-%d", rCtx.StackName, len(rCtx.Resources))
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
		name = fmt.Sprintf("/aws/%s/%s", rCtx.StackName, "logs")
	}

	body := map[string]any{"logGroupName": name}
	_, err := internalJSON(ctx, router, rCtx.Region, "Logs_20140328.CreateLogGroup", body)
	if err != nil {
		return "", nil, fmt.Errorf("logs CreateLogGroup: %w", err)
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

func (h *logsLogGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
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
	} else {
		body := map[string]any{"logGroupName": physicalID}
		_, _ = internalJSON(ctx, router, rCtx.Region, "Logs_20140328.DeleteRetentionPolicy", body)
	}
	arn := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:*", rCtx.Region, rCtx.AccountID, physicalID)
	return physicalID, map[string]string{"Arn": arn, "LogGroupName": physicalID}, nil
}

// ── SSM Parameter handler ──────────────────────────────────────────────────

type ssmParameterHandler struct{}

func (h *ssmParameterHandler) Create(ctx context.Context, router http.Handler, _ *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("/%s/param", rCtx.StackName)
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
		name = fmt.Sprintf("%s-Secret", rCtx.StackName)
	}
	body := map[string]any{"Name": name}
	if sv, ok := props["SecretString"]; ok {
		body["SecretString"] = sv
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

func (h *nestedStackHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
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

	if err := h.p.store.putStack(storeCtx, childStack); err != nil {
		return "", nil, fmt.Errorf("nested stack update store: %w", err)
	}

	h.p.updateStackResources(childStack, tmpl)

	if childStack.Status != StatusUpdateComplete {
		return "", nil, fmt.Errorf("nested stack %s update failed: %s", childName, childStack.StatusReason)
	}

	attrs := make(map[string]string)
	for _, out := range childStack.Outputs {
		attrs["Outputs."+out.Key] = out.Value
	}

	return childStack.StackID, attrs, nil
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

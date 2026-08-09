package cloudformation

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/trace"
)

const cfnXMLNS = "http://cloudformation.amazonaws.com/doc/2010-05-15/"

// Handler holds CloudFormation handler dependencies.
type Handler struct {
	cfg     *config.Config
	store   *cfnStore
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	prov    *provisioner
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

func newHandler(cfg *config.Config, store *cfnStore, log *serviceutil.ServiceLogger, clk clock.Clock, prov *provisioner) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log, clk: clk, prov: prov}
	h.initOps()
	return h
}

// initOps registers every known CloudFormation operation to its handler.
// Implemented operations point to their handler method; stubs use h.stub.
// Adding a new operation: add an entry here and implement or stub it.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// Implemented
		"CreateStack":            h.CreateStack,
		"UpdateStack":            h.UpdateStack,
		"RollbackStack":          h.RollbackStack,
		"DeleteStack":            h.DeleteStack,
		"DescribeStacks":         h.DescribeStacks,
		"ListStacks":             h.ListStacks,
		"GetTemplate":            h.GetTemplate,
		"CreateChangeSet":        h.CreateChangeSet,
		"DescribeChangeSet":      h.DescribeChangeSet,
		"ExecuteChangeSet":       h.ExecuteChangeSet,
		"DeleteChangeSet":        h.DeleteChangeSet,
		"ListChangeSets":         h.ListChangeSets,
		"DescribeStackResources": h.DescribeStackResources,
		"ListStackResources":     h.ListStackResources,
		"DescribeStackEvents":    h.DescribeStackEvents,
		"GetTemplateSummary":     h.GetTemplateSummary,
		"ValidateTemplate":       h.ValidateTemplate,
		"ListExports":            h.ListExports,
		"ListImports":            h.ListImports,
		// Stubs
		"ContinueUpdateRollback":       h.stub,
		"CancelUpdateStack":            h.stub,
		"SignalResource":               h.stub,
		"SetStackPolicy":               h.stub,
		"GetStackPolicy":               h.stub,
		"EstimateTemplateCost":         h.stub,
		"RegisterType":                 h.stub,
		"DescribeType":                 h.stub,
		"ListTypes":                    h.stub,
		"ListTypeRegistrations":        h.stub,
		"DeregisterType":               h.stub,
		"SetTypeDefaultVersion":        h.stub,
		"DescribeTypeRegistration":     h.stub,
		"DescribeAccountLimits":        h.stub,
		"CreateStackInstances":         h.stub,
		"CreateStackSet":               h.stub,
		"DeleteStackInstances":         h.stub,
		"DeleteStackSet":               h.stub,
		"DescribeStackInstance":        h.stub,
		"DescribeStackSet":             h.stub,
		"DescribeStackSetOperation":    h.stub,
		"ListStackInstances":           h.stub,
		"ListStackSetOperationResults": h.stub,
		"ListStackSetOperations":       h.stub,
		"ListStackSets":                h.stub,
		"UpdateStackInstances":         h.stub,
		"UpdateStackSet":               h.stub,
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("Action")
	if fn, ok := h.ops[action]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedQueryXML(w, r)
}

// resolveTemplateBody returns the template body from either TemplateBody or
// TemplateURL. When TemplateURL is provided, the template is fetched from S3
// via internal dispatch (the same mechanism used for nested stacks).
func (h *Handler) resolveTemplateBody(r *http.Request) (string, error) {
	if body := r.FormValue("TemplateBody"); body != "" {
		return body, nil
	}
	templateURL := r.FormValue("TemplateURL")
	if templateURL == "" {
		return "", fmt.Errorf("TemplateBody or TemplateURL is required")
	}
	u, err := url.Parse(templateURL)
	if err != nil {
		return "", fmt.Errorf("invalid TemplateURL: %w", err)
	}
	router := h.prov.router
	if router == nil {
		return "", fmt.Errorf("internal router not initialised")
	}
	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	// Use a fresh context to avoid leaking chi's route context from the
	// parent CloudFormation request into the internal S3 GET dispatch.
	ctx := context.Background()
	rec, err := internalRequest(ctx, router, region, http.MethodGet, u.Path, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch template from %s: %w", templateURL, err)
	}
	return rec.Body.String(), nil
}

// stub returns 501 for unimplemented operations.
func (h *Handler) stub(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// ── CreateStack ────────────────────────────────────────────────────────────

func (h *Handler) CreateStack(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	existing, _ := h.store.getStack(ctx, stackName)
	if existing != nil && existing.Status != StatusDeleteComplete {
		writeCFNError(w, r, "AlreadyExistsException",
			fmt.Sprintf("Stack [%s] already exists", stackName), http.StatusBadRequest)
		return
	}

	templateBody, tplErr := h.resolveTemplateBody(r)
	if tplErr != nil {
		writeCFNError(w, r, "ValidationError", tplErr.Error(), http.StatusBadRequest)
		return
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		writeCFNError(w, r, "ValidationError", err.Error(), http.StatusBadRequest)
		return
	}

	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)

	stackID := fmt.Sprintf("arn:aws:cloudformation:%s:%s:stack/%s/%s",
		region, h.cfg.AccountID, stackName, uuid.NewString())

	disableRollback, aerr := parseDisableRollback(r.FormValue("DisableRollback"))
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	params := collectParameters(r)
	tags := collectTags(r)
	caps := collectCapabilities(r)

	stack := &Stack{
		StackName:       stackName,
		StackID:         stackID,
		Region:          region,
		TemplateBody:    templateBody,
		Parameters:      params,
		Tags:            tags,
		Capabilities:    caps,
		RoleARN:         r.FormValue("RoleARN"),
		DisableRollback: disableRollback,
		Status:          StatusCreateInProgress,
		StatusReason:    "User Initiated",
		CreatedAt:       h.clk.Now(),
	}

	if err := h.store.putStack(ctx, stack); err != nil {
		writeCFNError(w, r, "InternalFailure", "failed to persist stack", http.StatusInternalServerError)
		return
	}

	h.prov.createStack(stack, tmpl, nil, trace.RecorderFromContext(r.Context()))

	writeCFNResponse(w, r, "CreateStackResponse", "CreateStackResult", stackIdResult{StackId: stackID})
}

// ── UpdateStack ────────────────────────────────────────────────────────────

func (h *Handler) UpdateStack(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	stack, aerr := h.store.getStack(ctx, stackName)
	if aerr != nil || stack == nil {
		writeCFNError(w, r, "ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", stackName), http.StatusBadRequest)
		return
	}
	previous := captureStackGeneration(stack)

	templateBody, tplErr := h.resolveTemplateBody(r)
	if tplErr != nil {
		// No template provided — reuse existing.
		templateBody = stack.TemplateBody
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		writeCFNError(w, r, "ValidationError", err.Error(), http.StatusBadRequest)
		return
	}

	disableRollback, aerr := parseDisableRollback(r.FormValue("DisableRollback"))
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	params := collectParameters(r)
	if len(params) > 0 {
		stack.Parameters = params
	}
	if tagsParameterPresent(r) {
		applyStackTags(stack, collectTags(r), true)
	}

	stack.DisableRollback = disableRollback
	stack.TemplateBody = templateBody
	stack.Status = StatusUpdateInProgress
	stack.StatusReason = "User Initiated"
	now := h.clk.Now()
	stack.UpdatedAt = &now

	if err := h.store.putStack(ctx, stack); err != nil {
		writeCFNError(w, r, "InternalFailure", "failed to persist stack", http.StatusInternalServerError)
		return
	}

	h.prov.updateStack(stack, tmpl, previous, nil, trace.RecorderFromContext(r.Context()))

	writeCFNResponse(w, r, "UpdateStackResponse", "UpdateStackResult", stackIdResult{StackId: stack.StackID})
}

// ── RollbackStack ──────────────────────────────────────────────────────────

// rollbackPathFor reports whether a stack in the given status can be rolled
// back, and which of the two rollback flows applies.
//
// Real CloudFormation accepts RollbackStack only for a stack that failed but
// still has a last known stable state to return to. A create that failed
// unwinds to ROLLBACK_COMPLETE; a failed update (or a failed automatic update
// rollback, which RollbackStack retries) unwinds to UPDATE_ROLLBACK_COMPLETE.
// Every other status — including the in-progress and already-stable ones — is
// rejected.
func rollbackPathFor(status string) (createPath bool, ok bool) {
	switch status {
	case StatusCreateFailed:
		return true, true
	case StatusUpdateFailed, StatusUpdateRollbackFailed:
		return false, true
	}
	return false, false
}

// RollbackStack rolls a failed stack back to its last known stable state.
// This is what `cdk rollback` calls to recover a stack stuck in UPDATE_FAILED,
// which otherwise blocks every subsequent deploy.
func (h *Handler) RollbackStack(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	stack, aerr := h.store.getStackByNameOrARN(ctx, stackName)
	if aerr != nil || stack == nil {
		writeCFNError(w, r, "ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", stackName), http.StatusBadRequest)
		return
	}

	createPath, ok := rollbackPathFor(stack.Status)
	if !ok {
		writeCFNError(w, r, "ValidationError",
			fmt.Sprintf("Stack [%s] is in %s state and can not be rolled back.", stack.StackName, stack.Status),
			http.StatusBadRequest)
		return
	}

	// RoleARN is accepted per the AWS API and recorded on the stack, but
	// Overcast does not validate or assume roles.
	if roleARN := r.FormValue("RoleARN"); roleARN != "" {
		stack.RoleARN = roleARN
	}

	// ClientRequestToken and RetainExceptOnCreate are accepted and ignored.
	// The former only tags events for idempotent retries; the latter controls
	// whether resources marked DeletionPolicy: Retain are still deleted when a
	// create rolls back — which is already what rollbackCreate does.

	if createPath {
		stack.Status = StatusRollbackInProgress
	} else {
		stack.Status = StatusUpdateRollbackInProgress
	}
	stack.StatusReason = "User Initiated"
	now := h.clk.Now()
	stack.UpdatedAt = &now

	if err := h.store.putStack(ctx, stack); err != nil {
		writeCFNError(w, r, "InternalFailure", "failed to persist stack", http.StatusInternalServerError)
		return
	}

	h.prov.rollbackStack(stack, createPath, trace.RecorderFromContext(r.Context()))

	writeCFNResponse(w, r, "RollbackStackResponse", "RollbackStackResult", stackIdResult{StackId: stack.StackID})
}

// ── DeleteStack ────────────────────────────────────────────────────────────

func (h *Handler) DeleteStack(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	stack, aerr := h.store.getStack(ctx, stackName)
	if aerr != nil || stack == nil {
		// AWS returns success for non-existent stacks.
		writeCFNResponse(w, r, "DeleteStackResponse", "DeleteStackResult", nil)
		return
	}

	stack.Status = StatusDeleteInProgress
	stack.StatusReason = "User Initiated"
	if err := h.store.putStack(ctx, stack); err != nil {
		writeCFNError(w, r, "InternalFailure", "failed to persist stack", http.StatusInternalServerError)
		return
	}

	h.prov.deleteStack(stack, trace.RecorderFromContext(r.Context()))

	writeCFNResponse(w, r, "DeleteStackResponse", "DeleteStackResult", nil)
}

// ── DescribeStacks ─────────────────────────────────────────────────────────

func (h *Handler) DescribeStacks(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	ctx := r.Context()

	if stackName != "" {
		stack, aerr := h.store.getStack(ctx, stackName)
		if aerr != nil {
			writeCFNError(w, r, "InternalFailure", "failed to read stack", http.StatusInternalServerError)
			return
		}
		if stack == nil {
			writeCFNError(w, r, "ValidationError",
				fmt.Sprintf("Stack with id %s does not exist", stackName), http.StatusBadRequest)
			return
		}
		writeCFNResponse(w, r, "DescribeStacksResponse", "DescribeStacksResult",
			describeStacksResult{Stacks: []stackXML{h.toStackXML(r.Context(), stack)}})
		return
	}

	stacks, aerr := h.store.listStacks(ctx)
	if aerr != nil {
		writeCFNError(w, r, "InternalFailure", "failed to list stacks", http.StatusInternalServerError)
		return
	}
	// Filter out DELETE_COMPLETE stacks (AWS default behaviour).
	var items []stackXML
	for _, s := range stacks {
		if s.Status != StatusDeleteComplete {
			items = append(items, h.toStackXML(r.Context(), s))
		}
	}
	writeCFNResponse(w, r, "DescribeStacksResponse", "DescribeStacksResult",
		describeStacksResult{Stacks: items})
}

// ── ListStacks ─────────────────────────────────────────────────────────────

func (h *Handler) ListStacks(w http.ResponseWriter, r *http.Request) {
	stacks, aerr := h.store.listStacks(r.Context())
	if aerr != nil {
		writeCFNError(w, r, "InternalFailure", "failed to list stacks", http.StatusInternalServerError)
		return
	}
	stacks = filterStacksByStatus(stacks, collectStackStatusFilter(r))
	slices.SortFunc(stacks, func(a, b *Stack) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	var summaries []stackSummaryXML
	for _, s := range stacks {
		summaries = append(summaries, toStackSummaryXML(s))
	}
	writeCFNResponse(w, r, "ListStacksResponse", "ListStacksResult",
		listStacksResult{StackSummaries: summaries})
}

// ── GetTemplate ────────────────────────────────────────────────────────────

func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}
	stack, aerr := h.store.getStack(r.Context(), stackName)
	if aerr != nil || stack == nil {
		writeCFNError(w, r, "ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", stackName), http.StatusBadRequest)
		return
	}
	writeCFNResponse(w, r, "GetTemplateResponse", "GetTemplateResult", getTemplateResult{TemplateBody: stack.TemplateBody})
}

// ── CreateChangeSet ────────────────────────────────────────────────────────

func (h *Handler) CreateChangeSet(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	csName := r.FormValue("ChangeSetName")
	if stackName == "" || csName == "" {
		writeCFNError(w, r, "ValidationError", "StackName and ChangeSetName are required", http.StatusBadRequest)
		return
	}

	templateBody, tplErr := h.resolveTemplateBody(r)
	if tplErr != nil {
		writeCFNError(w, r, "ValidationError", tplErr.Error(), http.StatusBadRequest)
		return
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		writeCFNError(w, r, "ValidationError", err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	changeSetType := r.FormValue("ChangeSetType")
	if changeSetType == "" {
		changeSetType = "UPDATE"
	}

	chsRegion := middleware.RegionFromContext(r.Context(), h.cfg.Region)

	// For CREATE type, the stack may not exist yet — create a placeholder.
	stack, _ := h.store.getStack(ctx, stackName)
	var stackID string
	if stack == nil {
		if changeSetType != "CREATE" {
			writeCFNError(w, r, "ValidationError",
				fmt.Sprintf("Stack [%s] does not exist", stackName), http.StatusBadRequest)
			return
		}
		stackID = fmt.Sprintf("arn:aws:cloudformation:%s:%s:stack/%s/%s",
			chsRegion, h.cfg.AccountID, stackName, uuid.NewString())
		stack = &Stack{
			StackName: stackName,
			StackID:   stackID,
			Region:    chsRegion,
			Status:    "REVIEW_IN_PROGRESS",
			CreatedAt: h.clk.Now(),
		}
		if err := h.store.putStack(ctx, stack); err != nil {
			writeCFNError(w, r, "InternalFailure", "failed to create stack placeholder", http.StatusInternalServerError)
			return
		}
	} else {
		stackID = stack.StackID
	}

	csID := fmt.Sprintf("arn:aws:cloudformation:%s:%s:changeSet/%s/%s",
		chsRegion, h.cfg.AccountID, csName, uuid.NewString())

	// Compute changes.
	changes := computeChanges(tmpl, stack, changeSetType)

	cs := &ChangeSet{
		ChangeSetName:   csName,
		ChangeSetID:     csID,
		StackID:         stackID,
		StackName:       stackName,
		TemplateBody:    templateBody,
		Parameters:      collectParameters(r),
		Tags:            collectTags(r),
		TagsSet:         tagsParameterPresent(r),
		Capabilities:    collectCapabilities(r),
		Status:          ChangeSetStatusCreateComplete,
		ChangeSetType:   changeSetType,
		Changes:         changes,
		CreatedAt:       h.clk.Now(),
		ExecutionStatus: ExecStatusAvailable,
	}

	if err := h.store.putChangeSet(ctx, cs); err != nil {
		writeCFNError(w, r, "InternalFailure", "failed to persist change set", http.StatusInternalServerError)
		return
	}

	writeCFNResponse(w, r, "CreateChangeSetResponse", "CreateChangeSetResult", changeSetIdResult{Id: csID, StackId: stackID})
}

// ── DescribeChangeSet ──────────────────────────────────────────────────────

func (h *Handler) DescribeChangeSet(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	csName := r.FormValue("ChangeSetName")
	if csName == "" {
		writeCFNError(w, r, "ValidationError", "ChangeSetName is required", http.StatusBadRequest)
		return
	}
	if stackName == "" && !isARN(csName) {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	cs, aerr := h.store.getChangeSet(r.Context(), stackName, csName)
	if aerr != nil || cs == nil {
		writeCFNError(w, r, "ChangeSetNotFoundException",
			fmt.Sprintf("ChangeSet [%s] does not exist", csName), http.StatusBadRequest)
		return
	}

	var changesXML []changeXML
	for _, c := range cs.Changes {
		changesXML = append(changesXML, changeXML{
			Type: c.Type,
			ResourceChange: resourceChangeXML{
				Action:            c.ResourceChange.Action,
				LogicalResourceID: c.ResourceChange.LogicalResourceID,
				ResourceType:      c.ResourceChange.ResourceType,
				Replacement:       c.ResourceChange.Replacement,
			},
		})
	}

	result := describeChangeSetResult{
		ChangeSetName:   cs.ChangeSetName,
		ChangeSetID:     cs.ChangeSetID,
		StackID:         cs.StackID,
		StackName:       cs.StackName,
		Status:          cs.Status,
		ExecutionStatus: cs.ExecutionStatus,
		ChangeSetType:   cs.ChangeSetType,
		CreatedAt:       cs.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		Changes:         changesXML,
	}

	writeCFNResponse(w, r, "DescribeChangeSetResponse", "DescribeChangeSetResult", result)
}

// ── ExecuteChangeSet ───────────────────────────────────────────────────────

func (h *Handler) ExecuteChangeSet(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	csName := r.FormValue("ChangeSetName")
	if csName == "" || (stackName == "" && !isARN(csName)) {
		writeCFNError(w, r, "ValidationError", "StackName and ChangeSetName are required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	cs, aerr := h.store.getChangeSet(ctx, stackName, csName)
	if aerr != nil || cs == nil {
		writeCFNError(w, r, "ChangeSetNotFoundException",
			fmt.Sprintf("ChangeSet [%s] does not exist", csName), http.StatusBadRequest)
		return
	}

	if cs.ExecutionStatus != ExecStatusAvailable {
		writeCFNError(w, r, "InvalidChangeSetStatus",
			fmt.Sprintf("ChangeSet [%s] is in %s state and cannot be executed", csName, cs.ExecutionStatus),
			http.StatusBadRequest)
		return
	}

	stack, _ := h.store.getStack(ctx, cs.StackName)
	if stack == nil {
		writeCFNError(w, r, "ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", cs.StackName), http.StatusBadRequest)
		return
	}

	tmpl, err := parseTemplate(cs.TemplateBody)
	if err != nil {
		writeCFNError(w, r, "ValidationError", err.Error(), http.StatusBadRequest)
		return
	}

	// Apply change set parameters/tags to stack.
	previous := captureStackGeneration(stack)
	if len(cs.Parameters) > 0 {
		stack.Parameters = cs.Parameters
	}
	if cs.TagsSet || len(cs.Tags) > 0 {
		applyStackTags(stack, cs.Tags, true)
	}
	stack.TemplateBody = cs.TemplateBody

	cs.ExecutionStatus = ExecStatusExecuteInProgress
	_ = h.store.putChangeSet(ctx, cs)

	if cs.ChangeSetType == "CREATE" {
		stack.Status = StatusCreateInProgress
		stack.StatusReason = "User Initiated"
		_ = h.store.putStack(ctx, stack)
		h.prov.createStack(stack, tmpl, h.prov.completeChangeSet(cs), trace.RecorderFromContext(r.Context()))
	} else {
		stack.Status = StatusUpdateInProgress
		stack.StatusReason = "User Initiated"
		now := h.clk.Now()
		stack.UpdatedAt = &now
		_ = h.store.putStack(ctx, stack)
		h.prov.updateStack(stack, tmpl, previous, h.prov.completeChangeSet(cs), trace.RecorderFromContext(r.Context()))
	}

	writeCFNResponse(w, r, "ExecuteChangeSetResponse", "ExecuteChangeSetResult", nil)
}

// ── DeleteChangeSet ────────────────────────────────────────────────────────

func (h *Handler) DeleteChangeSet(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	csName := r.FormValue("ChangeSetName")
	if csName == "" || stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName and ChangeSetName are required", http.StatusBadRequest)
		return
	}

	_ = h.store.deleteChangeSet(r.Context(), stackName, csName)
	writeCFNResponse(w, r, "DeleteChangeSetResponse", "DeleteChangeSetResult", nil)
}

// ── ListChangeSets ─────────────────────────────────────────────────────────

func (h *Handler) ListChangeSets(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	css, aerr := h.store.listChangeSetsForStack(r.Context(), stackName)
	if aerr != nil {
		writeCFNError(w, r, "InternalFailure", "failed to list change sets", http.StatusInternalServerError)
		return
	}

	var summaries []changeSetSummaryXML
	for _, cs := range css {
		summaries = append(summaries, changeSetSummaryXML{
			ChangeSetName:   cs.ChangeSetName,
			ChangeSetID:     cs.ChangeSetID,
			StackID:         cs.StackID,
			StackName:       cs.StackName,
			Status:          cs.Status,
			ExecutionStatus: cs.ExecutionStatus,
			CreatedAt:       cs.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	writeCFNResponse(w, r, "ListChangeSetsResponse", "ListChangeSetsResult",
		listChangeSetsResult{Summaries: summaries})
}

// ── DescribeStackResources ─────────────────────────────────────────────────

func (h *Handler) DescribeStackResources(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	stack, aerr := h.store.getStack(r.Context(), stackName)
	if aerr != nil || stack == nil {
		writeCFNError(w, r, "ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", stackName), http.StatusBadRequest)
		return
	}

	var resources []stackResourceXML
	for _, res := range stack.Resources {
		resources = append(resources, stackResourceXML{
			LogicalID:    res.LogicalID,
			PhysicalID:   res.PhysicalID,
			Type:         res.Type,
			Status:       res.Status,
			StatusReason: res.StatusReason,
			Timestamp:    res.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	writeCFNResponse(w, r, "DescribeStackResourcesResponse", "DescribeStackResourcesResult",
		describeStackResourcesResult{StackResources: resources})
}

// ── ListStackResources ─────────────────────────────────────────────────────

func (h *Handler) ListStackResources(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	stack, aerr := h.store.getStack(r.Context(), stackName)
	if aerr != nil || stack == nil {
		writeCFNError(w, r, "ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", stackName), http.StatusBadRequest)
		return
	}

	var summaries []stackResourceSummaryXML
	for _, res := range stack.Resources {
		summaries = append(summaries, stackResourceSummaryXML{
			LogicalID:            res.LogicalID,
			PhysicalID:           res.PhysicalID,
			Type:                 res.Type,
			Status:               res.Status,
			ResourceStatusReason: res.StatusReason,
			Timestamp:            res.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	writeCFNResponse(w, r, "ListStackResourcesResponse", "ListStackResourcesResult",
		listStackResourcesResult{Summaries: summaries})
}

// eventsPageSize is the number of stack events returned per DescribeStackEvents
// page. AWS CloudFormation doesn't document a fixed page size; ~20 matches
// observed production behaviour.
const eventsPageSize = 20

// ── DescribeStackEvents ────────────────────────────────────────────────────

func (h *Handler) DescribeStackEvents(w http.ResponseWriter, r *http.Request) {
	stackName := r.FormValue("StackName")
	if stackName == "" {
		writeCFNError(w, r, "ValidationError", "StackName is required", http.StatusBadRequest)
		return
	}

	stack, aerr := h.store.getStack(r.Context(), stackName)
	if aerr != nil || stack == nil {
		writeCFNError(w, r, "ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", stackName), http.StatusBadRequest)
		return
	}

	// Events are stored separately from the stack metadata so that stack
	// reads stay cheap as the event history grows unboundedly.
	allEvents, err := h.store.getStackEvents(r.Context(), stackName)
	if err != nil {
		writeCFNError(w, r, "InternalError", "failed to load stack events", http.StatusInternalServerError)
		return
	}

	// Build a newest-first view of the event history for pagination.
	n := len(allEvents)
	reversed := make([]StackEvent, n)
	for i, e := range allEvents {
		reversed[n-1-i] = e
	}

	// AWS documents no client-settable limit for this operation (see
	// eventsPageSize's doc comment), so requestedLimit is always 0 here and
	// DefaultLimit is the only thing that matters.
	page, err := serviceutil.Paginate(reversed, 0, r.FormValue("NextToken"),
		serviceutil.PaginateOptions{DefaultLimit: eventsPageSize})
	if err != nil {
		// A garbled/expired NextToken must not silently restart the walk
		// from page 1 (see docs/plans/pagination-plan.md G3) — that causes
		// duplicate delivery to any client polling with a stale token.
		// CloudFormation's Query-protocol API has no dedicated
		// "invalid token" error; ValidationError is the documented
		// catch-all for malformed request input:
		// https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/CommonErrors.html
		writeCFNError(w, r, "ValidationError", "The specified NextToken is invalid.", http.StatusBadRequest)
		return
	}

	eventsXML := make([]stackEventXML, 0, len(page.Items))
	for _, e := range page.Items {
		eventsXML = append(eventsXML, stackEventXML{
			StackID:              e.StackID,
			StackName:            e.StackName,
			EventID:              e.EventID,
			LogicalResourceID:    e.LogicalResourceID,
			PhysicalResourceID:   e.PhysicalResourceID,
			ResourceType:         e.ResourceType,
			ResourceStatus:       e.ResourceStatus,
			ResourceStatusReason: e.ResourceStatusReason,
			Timestamp:            e.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	writeCFNResponse(w, r, "DescribeStackEventsResponse", "DescribeStackEventsResult",
		describeStackEventsResult{StackEvents: eventsXML, NextToken: page.NextToken})
}

// ── GetTemplateSummary ─────────────────────────────────────────────────────

func (h *Handler) GetTemplateSummary(w http.ResponseWriter, r *http.Request) {
	templateBody := r.FormValue("TemplateBody")
	templateURL := r.FormValue("TemplateURL")
	stackName := r.FormValue("StackName")

	// Try TemplateURL if TemplateBody is not provided.
	if templateBody == "" && templateURL != "" {
		var err error
		templateBody, err = h.resolveTemplateBody(r)
		if err != nil {
			writeCFNError(w, r, "ValidationError", err.Error(), http.StatusBadRequest)
			return
		}
	}

	if templateBody == "" && stackName != "" {
		stack, aerr := h.store.getStack(r.Context(), stackName)
		if aerr != nil || stack == nil {
			writeCFNError(w, r, "ValidationError",
				fmt.Sprintf("Stack [%s] does not exist", stackName), http.StatusBadRequest)
			return
		}
		templateBody = stack.TemplateBody
	}

	if templateBody == "" {
		writeCFNError(w, r, "ValidationError", "TemplateBody, TemplateURL, or StackName is required", http.StatusBadRequest)
		return
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		writeCFNError(w, r, "ValidationError", err.Error(), http.StatusBadRequest)
		return
	}

	var paramDecls []templateParameterXML
	for name, p := range tmpl.Parameters {
		paramDecls = append(paramDecls, templateParameterXML{
			ParameterKey:  name,
			ParameterType: p.Type,
			DefaultValue:  string(p.Default),
			Description:   p.Description,
		})
	}

	var resourceTypes []string
	for _, res := range tmpl.Resources {
		resourceTypes = append(resourceTypes, res.Type)
	}

	result := templateSummaryResult{
		Description:   tmpl.Description,
		Parameters:    paramDecls,
		ResourceTypes: resourceTypes,
	}

	writeCFNResponse(w, r, "GetTemplateSummaryResponse", "GetTemplateSummaryResult", result)
}

// ── ValidateTemplate ───────────────────────────────────────────────────────

func (h *Handler) ValidateTemplate(w http.ResponseWriter, r *http.Request) {
	templateBody, tplErr := h.resolveTemplateBody(r)
	if tplErr != nil {
		writeCFNError(w, r, "ValidationError", tplErr.Error(), http.StatusBadRequest)
		return
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		writeCFNError(w, r, "ValidationError", err.Error(), http.StatusBadRequest)
		return
	}

	var paramDecls []templateParameterXML
	for name, p := range tmpl.Parameters {
		paramDecls = append(paramDecls, templateParameterXML{
			ParameterKey:  name,
			ParameterType: p.Type,
			DefaultValue:  string(p.Default),
			Description:   p.Description,
		})
	}

	writeCFNResponse(w, r, "ValidateTemplateResponse", "ValidateTemplateResult", validateTemplateResult{
		Description: tmpl.Description,
		Parameters:  paramDecls,
	})
}

// ── Change computation ─────────────────────────────────────────────────────

func computeChanges(tmpl *Template, stack *Stack, changeSetType string) []Change {
	var changes []Change

	existingResources := map[string]StackResource{}
	for _, r := range stack.Resources {
		existingResources[r.LogicalID] = r
	}

	for logicalID, res := range tmpl.Resources {
		if changeSetType == "CREATE" {
			changes = append(changes, Change{
				Type: "Resource",
				ResourceChange: ResourceChange{
					Action:            "Add",
					LogicalResourceID: logicalID,
					ResourceType:      res.Type,
				},
			})
		} else {
			if existing, ok := existingResources[logicalID]; ok {
				changes = append(changes, Change{
					Type: "Resource",
					ResourceChange: ResourceChange{
						Action:             "Modify",
						LogicalResourceID:  logicalID,
						PhysicalResourceID: existing.PhysicalID,
						ResourceType:       res.Type,
						Replacement:        "False",
					},
				})
				delete(existingResources, logicalID)
			} else {
				changes = append(changes, Change{
					Type: "Resource",
					ResourceChange: ResourceChange{
						Action:            "Add",
						LogicalResourceID: logicalID,
						ResourceType:      res.Type,
					},
				})
			}
		}
	}

	// Resources removed from template.
	for logicalID, existing := range existingResources {
		changes = append(changes, Change{
			Type: "Resource",
			ResourceChange: ResourceChange{
				Action:             "Remove",
				LogicalResourceID:  logicalID,
				PhysicalResourceID: existing.PhysicalID,
				ResourceType:       existing.Type,
			},
		})
	}

	return changes
}

// ── ListExports / ListImports ──────────────────────────────────────────────

func (h *Handler) ListExports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exports, aerr := h.store.listExports(ctx)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	items := make([]exportXML, 0, len(exports))
	for _, e := range exports {
		items = append(items, exportXML(e))
	}

	writeCFNResponse(w, r, "ListExportsResponse", "ListExportsResult",
		listExportsResult{Exports: items})
}

func (h *Handler) ListImports(w http.ResponseWriter, r *http.Request) {
	exportName := r.FormValue("ExportName")
	if exportName == "" {
		writeCFNError(w, r, "ValidationError", "ExportName is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	importers, aerr := h.store.listImportingStacks(ctx, exportName)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}

	writeCFNResponse(w, r, "ListImportsResponse", "ListImportsResult",
		listImportsResult{Imports: importers})
}

// ── Form parameter helpers ─────────────────────────────────────────────────

func collectParameters(r *http.Request) []Parameter {
	var params []Parameter
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Parameters.member.%d.ParameterKey", i))
		if key == "" {
			break
		}
		val := r.FormValue(fmt.Sprintf("Parameters.member.%d.ParameterValue", i))
		params = append(params, Parameter{Key: key, Value: val})
	}
	return params
}

func collectTags(r *http.Request) []Tag {
	var tags []Tag
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.member.%d.Key", i))
		if key == "" {
			break
		}
		val := r.FormValue(fmt.Sprintf("Tags.member.%d.Value", i))
		tags = append(tags, Tag{Key: key, Value: val})
	}
	return tags
}

func tagsParameterPresent(r *http.Request) bool {
	_ = r.ParseForm()
	if _, ok := r.Form["Tags"]; ok {
		return true
	}
	for key := range r.Form {
		if strings.HasPrefix(key, "Tags.member.") {
			return true
		}
	}
	return false
}

func applyStackTags(stack *Stack, tags []Tag, present bool) {
	if !present {
		return
	}
	stack.Tags = append([]Tag(nil), tags...)
}

// collectStackStatusFilter reads ListStacks' StackStatusFilter.member.N form
// values. An empty result means the caller sent no filter, which AWS treats as
// "every stack" rather than "no stacks" — see filterStacksByStatus.
func collectStackStatusFilter(r *http.Request) []string {
	var statuses []string
	for i := 1; ; i++ {
		status := r.FormValue(fmt.Sprintf("StackStatusFilter.member.%d", i))
		if status == "" {
			break
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// filterStacksByStatus applies ListStacks' StackStatusFilter: with one or more
// statuses named, only stacks in one of them are returned; with none named,
// every stack is, "including existing stacks and stacks that have been
// deleted". That last part is why this is not DescribeStacks' filter — the
// caller-visible default there drops DELETE_COMPLETE, and here it must not.
//
// A status outside the AWS enum simply matches nothing. Real CloudFormation
// rejects one with a ValidationError naming the whole enum; Overcast does not
// model the enum, and guessing at a subset would reject statuses AWS accepts
// (the IMPORT_* family, which the emulator never produces but a client may
// legitimately filter on).
//
// https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStacks.html
func filterStacksByStatus(stacks []*Stack, statuses []string) []*Stack {
	if len(statuses) == 0 {
		return stacks
	}
	filtered := make([]*Stack, 0, len(stacks))
	for _, s := range stacks {
		if slices.Contains(statuses, s.Status) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// parseDisableRollback resolves one operation's DisableRollback member from its
// wire value, empty meaning absent.
//
// AWS models it as an optional Boolean on CreateStack and UpdateStack alike and
// documents the same default for both — `False`. It is therefore a decision
// each operation makes for itself: an UpdateStack that omits it rolls back on
// failure even when the stack was created with rollback disabled, and one that
// sends `false` overrides the value CreateStack was given.
// https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_UpdateStack.html
func parseDisableRollback(raw string) (bool, *protocol.AWSError) {
	if raw == "" {
		return false, nil
	}
	disabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false, cfnerr("ValidationError",
			fmt.Sprintf("Value '%s' at 'disableRollback' failed to satisfy constraint: Member must be a boolean", raw),
			http.StatusBadRequest)
	}
	return disabled, nil
}

func collectCapabilities(r *http.Request) []string {
	var caps []string
	for i := 1; ; i++ {
		cap := r.FormValue(fmt.Sprintf("Capabilities.member.%d", i))
		if cap == "" {
			break
		}
		caps = append(caps, cap)
	}
	return caps
}

// ── XML response types ─────────────────────────────────────────────────────

type stackIdResult struct {
	StackId string `xml:"StackId"`
}

type changeSetIdResult struct {
	Id      string `xml:"Id"`
	StackId string `xml:"StackId"`
}

type getTemplateResult struct {
	TemplateBody string `xml:"TemplateBody"`
}

type validateTemplateResult struct {
	Description string                 `xml:"Description"`
	Parameters  []templateParameterXML `xml:"Parameters>member"`
}

type exportXML struct {
	ExportingStackId string `xml:"ExportingStackId"`
	Name             string `xml:"Name"`
	Value            string `xml:"Value"`
}

type listExportsResult struct {
	Exports []exportXML `xml:"Exports>member,omitempty"`
}

type listImportsResult struct {
	Imports []string `xml:"Imports>member,omitempty"`
}

// stackXML is AWS's Stack shape. Its last-updated element is LastUpdatedTime —
// LastUpdatedTimestamp is StackResourceSummary's spelling, and the SDKs' generated
// deserialisers match these names literally, so the wrong one parses as an absent
// field rather than as an error.
type stackXML struct {
	StackName    string      `xml:"StackName"`
	StackID      string      `xml:"StackId"`
	ParentID     string      `xml:"ParentId,omitempty"`
	RootID       string      `xml:"RootId,omitempty"`
	StackStatus  string      `xml:"StackStatus"`
	StatusReason string      `xml:"StackStatusReason,omitempty"`
	CreatedAt    string      `xml:"CreationTime"`
	UpdatedAt    string      `xml:"LastUpdatedTime,omitempty"`
	Parameters   []paramXML  `xml:"Parameters>member,omitempty"`
	Outputs      []outputXML `xml:"Outputs>member,omitempty"`
	Tags         []tagXML    `xml:"Tags>member,omitempty"`
	Capabilities []string    `xml:"Capabilities>member,omitempty"`
}

type paramXML struct {
	Key   string `xml:"ParameterKey"`
	Value string `xml:"ParameterValue"`
}

type outputXML struct {
	Key         string `xml:"OutputKey"`
	Value       string `xml:"OutputValue"`
	Description string `xml:"Description,omitempty"`
	ExportName  string `xml:"ExportName,omitempty"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type describeStacksResult struct {
	Stacks []stackXML `xml:"Stacks>member"`
}

type stackSummaryXML struct {
	StackName   string `xml:"StackName"`
	StackID     string `xml:"StackId"`
	ParentID    string `xml:"ParentId,omitempty"`
	RootID      string `xml:"RootId,omitempty"`
	StackStatus string `xml:"StackStatus"`
	// StackSummary carries the reason as well as the status, which is what
	// lets a list view say why a stack failed without a second call.
	StatusReason string `xml:"StackStatusReason,omitempty"`
	CreatedAt    string `xml:"CreationTime"`
	UpdatedAt    string `xml:"LastUpdatedTime,omitempty"`
	DeletedAt    string `xml:"DeletionTime,omitempty"`
}

// toStackSummaryXML builds the ListStacks view of a stack. ListStacks has two
// entry points — the Query handler and the typed operation — and both answer
// from this one place so a field cannot reach callers on only one of them.
func toStackSummaryXML(s *Stack) stackSummaryXML {
	summary := stackSummaryXML{
		StackName:    s.StackName,
		StackID:      s.StackID,
		ParentID:     s.ParentStackID,
		RootID:       s.RootID,
		StackStatus:  s.Status,
		StatusReason: s.StatusReason,
		CreatedAt:    s.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	// Both are absent until the stack has actually been updated or deleted: an
	// empty element would deserialise to a zero timestamp rather than to nothing.
	if s.UpdatedAt != nil {
		summary.UpdatedAt = s.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	if s.DeletedAt != nil {
		summary.DeletedAt = s.DeletedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return summary
}

type listStacksResult struct {
	StackSummaries []stackSummaryXML `xml:"StackSummaries>member"`
}

type changeXML struct {
	Type           string            `xml:"Type"`
	ResourceChange resourceChangeXML `xml:"ResourceChange"`
}

type resourceChangeXML struct {
	Action            string `xml:"Action"`
	LogicalResourceID string `xml:"LogicalResourceId"`
	ResourceType      string `xml:"ResourceType"`
	Replacement       string `xml:"Replacement,omitempty"`
}

type describeChangeSetResult struct {
	ChangeSetName   string      `xml:"ChangeSetName"`
	ChangeSetID     string      `xml:"ChangeSetId"`
	StackID         string      `xml:"StackId"`
	StackName       string      `xml:"StackName"`
	Status          string      `xml:"Status"`
	ExecutionStatus string      `xml:"ExecutionStatus"`
	ChangeSetType   string      `xml:"ChangeSetType"`
	CreatedAt       string      `xml:"CreationTime"`
	Changes         []changeXML `xml:"Changes>member,omitempty"`
}

type changeSetSummaryXML struct {
	ChangeSetName   string `xml:"ChangeSetName"`
	ChangeSetID     string `xml:"ChangeSetId"`
	StackID         string `xml:"StackId"`
	StackName       string `xml:"StackName"`
	Status          string `xml:"Status"`
	ExecutionStatus string `xml:"ExecutionStatus"`
	CreatedAt       string `xml:"CreationTime"`
}

type listChangeSetsResult struct {
	Summaries []changeSetSummaryXML `xml:"Summaries>member"`
}

type stackResourceXML struct {
	LogicalID  string `xml:"LogicalResourceId"`
	PhysicalID string `xml:"PhysicalResourceId,omitempty"`
	Type       string `xml:"ResourceType"`
	Status     string `xml:"ResourceStatus"`
	// AWS's StackResource carries the reason next to the status, and it is the
	// only place a DescribeStackResources caller learns why a resource is
	// DELETE_FAILED. ListStackResources has always reported it; this shape
	// dropped it, so the CLI showed the failed status with no explanation.
	StatusReason string `xml:"ResourceStatusReason,omitempty"`
	Timestamp    string `xml:"Timestamp"`
}

type describeStackResourcesResult struct {
	StackResources []stackResourceXML `xml:"StackResources>member"`
}

type stackResourceSummaryXML struct {
	LogicalID            string `xml:"LogicalResourceId"`
	PhysicalID           string `xml:"PhysicalResourceId,omitempty"`
	Type                 string `xml:"ResourceType"`
	Status               string `xml:"ResourceStatus"`
	ResourceStatusReason string `xml:"ResourceStatusReason,omitempty"`
	Timestamp            string `xml:"LastUpdatedTimestamp"`
}

type listStackResourcesResult struct {
	Summaries []stackResourceSummaryXML `xml:"StackResourceSummaries>member"`
}

type stackEventXML struct {
	StackID              string `xml:"StackId"`
	StackName            string `xml:"StackName"`
	EventID              string `xml:"EventId"`
	LogicalResourceID    string `xml:"LogicalResourceId"`
	PhysicalResourceID   string `xml:"PhysicalResourceId,omitempty"`
	ResourceType         string `xml:"ResourceType"`
	ResourceStatus       string `xml:"ResourceStatus"`
	ResourceStatusReason string `xml:"ResourceStatusReason,omitempty"`
	Timestamp            string `xml:"Timestamp"`
}

type describeStackEventsResult struct {
	StackEvents []stackEventXML `xml:"StackEvents>member"`
	NextToken   string          `xml:"NextToken,omitempty"`
}

type templateParameterXML struct {
	ParameterKey  string `xml:"ParameterKey"`
	ParameterType string `xml:"ParameterType,omitempty"`
	DefaultValue  string `xml:"DefaultValue,omitempty"`
	Description   string `xml:"Description,omitempty"`
}

type templateSummaryResult struct {
	Description   string                 `xml:"Description,omitempty"`
	Parameters    []templateParameterXML `xml:"Parameters>member,omitempty"`
	ResourceTypes []string               `xml:"ResourceTypes>member,omitempty"`
}

// reachableURL re-hosts a stack output that names a host-routed AWS endpoint
// onto the origin the caller reached Overcast on. Anything else is returned
// unchanged.
//
// CDK composes invoke URLs in the template itself:
//
//	{"Fn::Join": ["", ["https://", {"Ref": "Api"}, ".execute-api.us-east-1.",
//	                   {"Ref": "AWS::URLSuffix"}, "/", {"Ref": "Stage"}, "/"]]}
//
// The scheme is a literal and there is no port, so resolving AWS::URLSuffix to
// anything other than "amazonaws.com" cannot produce a dialable URL — and
// would make the pseudo-parameter lie about what it is. The correction belongs
// here instead, where Overcast has already assembled the finished string and
// still holds the request.
//
// It parses with middleware.ParseHostRoute — the same grammar that decides
// inbound routing — and re-mints through serviceutil.HostRoutedURL, the helper
// every service that hands back such a URL already uses. So the grammar is
// stated once and applied in both directions, the scheme and port come from
// ClientBaseURL (which honours TLS and the request port), and a rewritten
// output is by construction a URL this router accepts.
//
// Only a registered host-route label is claimed, so ECR registry URIs, S3
// URLs, ARNs and plain strings pass through untouched: Overcast does not serve
// those hostnames and must not claim it does.
// clientBaseURL is serviceutil.ClientBaseURL for a caller that holds a context
// rather than a request: a configured OVERCAST_HOSTNAME wins over the address
// the caller happened to dial, but the caller's port is kept, since it is the
// one known to reach this process. This used to be a private copy of that
// precedence — the copy predates ClientBaseURLFromOrigin, which now implements
// it for every context-shaped caller (see docs/plans/client-facing-url-minting.md).
// serviceutil cannot read the client endpoint itself — middleware imports
// serviceutil, not the other way round — so the context lookup happens here.
func (h *Handler) clientBaseURL(ctx context.Context) string {
	return serviceutil.ClientBaseURLFromOrigin(h.cfg, middleware.ClientEndpointFromContext(ctx))
}

// It takes a context rather than a request so the Query and typed (Smithy)
// paths share one implementation; the caller's origin is read from the context
// the ClientEndpoint middleware stamps.
func (h *Handler) reachableURL(ctx context.Context, value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return value
	}
	m, ok := middleware.ParseHostRoute(u.Host)
	if !ok {
		// Not a host-routed endpoint. It may still be one of Overcast's own
		// path-style URLs — an SQS queue URL is `http://<origin>/<account>/<name>`,
		// whose host is a plain address rather than a routing label.
		return h.reoriginOwnURL(ctx, u, value)
	}
	rehosted := serviceutil.HostRoutedURLFromBase(h.clientBaseURL(ctx), m.Label, m.ID, m.Region, u.Path)
	if rehosted == "" {
		return value
	}
	if u.RawQuery != "" {
		rehosted += "?" + u.RawQuery
	}
	return rehosted
}

// reoriginOwnURL re-mints a stack output that names Overcast on the origin the
// caller reached it on, leaving the path and query alone.
//
// Resource handlers build these URLs while provisioning, from the configured
// origin — provisioning runs through internal requests, so there is no caller
// to derive one from. That value is then stored and handed to every later
// caller, including ones that reached Overcast somewhere else entirely: a
// container published on a different host port, or over a different hostname.
// Correcting it here rather than at provisioning time is what makes it right
// for each caller rather than only for the one who deployed.
//
// Only an origin that is recognisably Overcast's own is claimed. Anything else
// — a third-party endpoint a template happened to output, an S3 URL, an ECR
// URI — is left exactly as the user wrote it, the same restraint the
// host-routed branch shows.
func (h *Handler) reoriginOwnURL(ctx context.Context, u *url.URL, value string) string {
	base := h.clientBaseURL(ctx)
	if base == "" {
		return value
	}
	target, err := url.Parse(base)
	if err != nil || target.Host == "" || target.Host == u.Host {
		return value
	}
	if !h.isOwnOrigin(u) {
		return value
	}
	u.Scheme = target.Scheme
	u.Host = target.Host
	return u.String()
}

// isOwnOrigin reports whether a URL's origin is one Overcast answers on: the
// configured external base, or the loopback forms of its own listen port.
//
// Loopback counts because a URL minted for one caller — or by a host-side
// deploy — routinely names localhost, and inside a container or from another
// host that is not Overcast at all.
func (h *Handler) isOwnOrigin(u *url.URL) bool {
	if h.cfg == nil {
		return false
	}
	if configured, err := url.Parse(h.cfg.ExternalBaseURL()); err == nil && configured.Host == u.Host {
		return true
	}
	port := u.Port()
	if port == "" || port != strconv.Itoa(h.cfg.Port) {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return true
	}
	return false
}

func (h *Handler) toStackXML(ctx context.Context, s *Stack) stackXML {
	sx := stackXML{
		StackName:    s.StackName,
		StackID:      s.StackID,
		ParentID:     s.ParentStackID,
		RootID:       s.RootID,
		StackStatus:  s.Status,
		StatusReason: s.StatusReason,
		CreatedAt:    s.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if s.UpdatedAt != nil {
		sx.UpdatedAt = s.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	for _, p := range s.Parameters {
		sx.Parameters = append(sx.Parameters, paramXML(p))
	}
	for _, o := range s.Outputs {
		ox := outputXML(o)
		ox.Value = h.reachableURL(ctx, ox.Value)
		sx.Outputs = append(sx.Outputs, ox)
	}
	for _, t := range s.Tags {
		sx.Tags = append(sx.Tags, tagXML(t))
	}
	sx.Capabilities = s.Capabilities
	return sx
}

// ── XML response writer ────────────────────────────────────────────────────

func writeCFNResponse(w http.ResponseWriter, r *http.Request, responseName, resultName string, result any) {
	reqID := protocol.RequestIDFromContext(r.Context())

	// We need to produce XML like:
	// <{responseName} xmlns="...">
	//   <{resultName}>...result...</{resultName}>
	//   <ResponseMetadata><RequestId>...</RequestId></ResponseMetadata>
	// </{responseName}>

	type resultWrapper struct {
		XMLName xml.Name
		Inner   any `xml:",innerxml"`
	}

	// Marshal the inner result first. nil means empty result body.
	// xml.Marshal wraps the output in a root element named after the type
	// (e.g. <stackIdResult>…</stackIdResult>). Since the response wrapper
	// already provides the correct element name via resultName, strip the
	// outer element to avoid double-wrapping.
	var innerStr string
	if result != nil {
		innerBytes, err := xml.Marshal(result)
		if err != nil {
			protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
			return
		}
		innerStr = stripRootElement(string(innerBytes))
	}

	type responseMetadata struct {
		RequestId string `xml:"RequestId"`
	}

	// Build complete response manually for correct element names.
	type fullResponse struct {
		XMLName          xml.Name
		Xmlns            string `xml:"xmlns,attr"`
		ResultInner      resultWrapper
		ResponseMetadata responseMetadata `xml:"ResponseMetadata"`
	}

	resp := fullResponse{
		XMLName: xml.Name{Local: responseName},
		Xmlns:   cfnXMLNS,
		ResultInner: resultWrapper{
			XMLName: xml.Name{Local: resultName},
			Inner:   innerStr,
		},
		ResponseMetadata: responseMetadata{RequestId: reqID},
	}

	out, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xml.Header)) //nolint:errcheck
	w.Write(out)                //nolint:errcheck
}

func writeCFNError(w http.ResponseWriter, r *http.Request, code, message string, httpStatus int) {
	protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
	})
}

// stripRootElement removes the outermost XML element produced by xml.Marshal,
// returning only the inner content. For example:
//
//	"<stackIdResult><StackId>x</StackId></stackIdResult>"  →  "<StackId>x</StackId>"
//	"<empty></empty>"                                       →  ""
//
// This is needed because writeCFNResponse already wraps the content in a
// named result element; without stripping, the type name would appear as
// a spurious nested element.
func stripRootElement(s string) string {
	// Find end of opening tag.
	open := strings.Index(s, ">")
	if open < 0 {
		return s
	}
	// Find start of closing tag (last "</").
	close := strings.LastIndex(s, "</")
	if close < 0 || close <= open {
		return s
	}
	return s[open+1 : close]
}

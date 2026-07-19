package cloudformation

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
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
		DisableRollback: r.FormValue("DisableRollback") == "true",
		Status:          StatusCreateInProgress,
		StatusReason:    "User Initiated",
		CreatedAt:       h.clk.Now(),
	}

	if err := h.store.putStack(ctx, stack); err != nil {
		writeCFNError(w, r, "InternalFailure", "failed to persist stack", http.StatusInternalServerError)
		return
	}

	h.prov.createStack(stack, tmpl, nil)

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

	params := collectParameters(r)
	if len(params) > 0 {
		stack.Parameters = params
	}
	tags := collectTags(r)
	if len(tags) > 0 {
		stack.Tags = tags
	}

	stack.TemplateBody = templateBody
	stack.Status = StatusUpdateInProgress
	stack.StatusReason = "User Initiated"
	now := h.clk.Now()
	stack.UpdatedAt = &now

	if err := h.store.putStack(ctx, stack); err != nil {
		writeCFNError(w, r, "InternalFailure", "failed to persist stack", http.StatusInternalServerError)
		return
	}

	h.prov.updateStack(stack, tmpl, nil)

	writeCFNResponse(w, r, "UpdateStackResponse", "UpdateStackResult", stackIdResult{StackId: stack.StackID})
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

	h.prov.deleteStack(stack)

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
			describeStacksResult{Stacks: []stackXML{toStackXML(stack)}})
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
			items = append(items, toStackXML(s))
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
	slices.SortFunc(stacks, func(a, b *Stack) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	var summaries []stackSummaryXML
	for _, s := range stacks {
		summaries = append(summaries, stackSummaryXML{
			StackName:   s.StackName,
			StackID:     s.StackID,
			ParentID:    s.ParentStackID,
			RootID:      s.RootID,
			StackStatus: s.Status,
			CreatedAt:   s.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
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
	if len(cs.Parameters) > 0 {
		stack.Parameters = cs.Parameters
	}
	if len(cs.Tags) > 0 {
		stack.Tags = cs.Tags
	}
	stack.TemplateBody = cs.TemplateBody

	cs.ExecutionStatus = ExecStatusExecuteInProgress
	_ = h.store.putChangeSet(ctx, cs)

	if cs.ChangeSetType == "CREATE" {
		stack.Status = StatusCreateInProgress
		stack.StatusReason = "User Initiated"
		_ = h.store.putStack(ctx, stack)
		h.prov.createStack(stack, tmpl, h.prov.completeChangeSet(cs))
	} else {
		stack.Status = StatusUpdateInProgress
		stack.StatusReason = "User Initiated"
		now := h.clk.Now()
		stack.UpdatedAt = &now
		_ = h.store.putStack(ctx, stack)
		h.prov.updateStack(stack, tmpl, h.prov.completeChangeSet(cs))
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
			LogicalID:  res.LogicalID,
			PhysicalID: res.PhysicalID,
			Type:       res.Type,
			Status:     res.Status,
			Timestamp:  res.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
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

	page := serviceutil.Paginate(reversed, eventsPageSize, r.FormValue("NextToken"))

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
			DefaultValue:  p.Default,
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
			DefaultValue:  p.Default,
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

type stackXML struct {
	StackName    string      `xml:"StackName"`
	StackID      string      `xml:"StackId"`
	ParentID     string      `xml:"ParentId,omitempty"`
	RootID       string      `xml:"RootId,omitempty"`
	StackStatus  string      `xml:"StackStatus"`
	StatusReason string      `xml:"StackStatusReason,omitempty"`
	CreatedAt    string      `xml:"CreationTime"`
	UpdatedAt    string      `xml:"LastUpdatedTimestamp,omitempty"`
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
	CreatedAt   string `xml:"CreationTime"`
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
	Timestamp  string `xml:"Timestamp"`
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

func toStackXML(s *Stack) stackXML {
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
		sx.Outputs = append(sx.Outputs, outputXML(o))
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

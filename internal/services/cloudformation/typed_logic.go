package cloudformation

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ── Request types (json tags used by codec.Decode for form mapping) ───

type createStackReq struct {
	StackName       string           `json:"StackName"`
	TemplateBody    string           `json:"TemplateBody"`
	TemplateURL     string           `json:"TemplateURL"`
	RoleARN         string           `json:"RoleARN"`
	DisableRollback string           `json:"DisableRollback"`
	Parameters      []cfnParamMember `json:"Parameters"`
	Tags            []cfnTagMember   `json:"Tags"`
	Capabilities    []string         `json:"Capabilities"`
}

type updateStackReq struct {
	StackName    string           `json:"StackName"`
	TemplateBody string           `json:"TemplateBody"`
	TemplateURL  string           `json:"TemplateURL"`
	Parameters   []cfnParamMember `json:"Parameters"`
	Tags         []cfnTagMember   `json:"Tags"`
	Capabilities []string         `json:"Capabilities"`
}

type deleteStackReq struct {
	StackName string `json:"StackName"`
}

type describeStacksReq struct {
	StackName string `json:"StackName"`
}

type listStacksReq struct{}

type getTemplateReq struct {
	StackName string `json:"StackName"`
}

type createChangeSetReq struct {
	StackName     string           `json:"StackName"`
	ChangeSetName string           `json:"ChangeSetName"`
	ChangeSetType string           `json:"ChangeSetType"`
	TemplateBody  string           `json:"TemplateBody"`
	TemplateURL   string           `json:"TemplateURL"`
	Parameters    []cfnParamMember `json:"Parameters"`
	Tags          []cfnTagMember   `json:"Tags"`
	Capabilities  []string         `json:"Capabilities"`
}

type describeChangeSetReq struct {
	StackName     string `json:"StackName"`
	ChangeSetName string `json:"ChangeSetName"`
}

type executeChangeSetReq struct {
	StackName     string `json:"StackName"`
	ChangeSetName string `json:"ChangeSetName"`
}

type deleteChangeSetReq struct {
	StackName     string `json:"StackName"`
	ChangeSetName string `json:"ChangeSetName"`
}

type listChangeSetsReq struct {
	StackName string `json:"StackName"`
}

type describeStackResourcesReq struct {
	StackName string `json:"StackName"`
}

type listStackResourcesReq struct {
	StackName string `json:"StackName"`
}

type describeStackEventsReq struct {
	StackName string `json:"StackName"`
	NextToken string `json:"NextToken"`
}

type getTemplateSummaryReq struct {
	TemplateBody string `json:"TemplateBody"`
	TemplateURL  string `json:"TemplateURL"`
	StackName    string `json:"StackName"`
}

type validateTemplateReq struct {
	TemplateBody string `json:"TemplateBody"`
	TemplateURL  string `json:"TemplateURL"`
}

type listImportsReq struct {
	ExportName string `json:"ExportName"`
}

type cfnParamMember struct {
	ParameterKey   string `json:"ParameterKey"`
	ParameterValue string `json:"ParameterValue"`
}

type cfnTagMember struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// ── Response types (xml tags for QueryXML codec WriteResponse) ────

type cfnResponseMeta struct {
	RequestId string `xml:"RequestId"`
}

type createStackResp struct {
	XMLName struct{}        `xml:"CreateStackResponse"`
	Xmlns   string          `xml:"xmlns,attr"`
	Result  stackIdResult   `xml:"CreateStackResult"`
	Meta    cfnResponseMeta `xml:"ResponseMetadata"`
}

type updateStackResp struct {
	XMLName struct{}        `xml:"UpdateStackResponse"`
	Xmlns   string          `xml:"xmlns,attr"`
	Result  stackIdResult   `xml:"UpdateStackResult"`
	Meta    cfnResponseMeta `xml:"ResponseMetadata"`
}

type describeStacksResp struct {
	XMLName struct{}             `xml:"DescribeStacksResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  describeStacksResult `xml:"DescribeStacksResult"`
	Meta    cfnResponseMeta      `xml:"ResponseMetadata"`
}

type listStacksResp struct {
	XMLName struct{}         `xml:"ListStacksResponse"`
	Xmlns   string           `xml:"xmlns,attr"`
	Result  listStacksResult `xml:"ListStacksResult"`
	Meta    cfnResponseMeta  `xml:"ResponseMetadata"`
}

type getTemplateResp struct {
	XMLName struct{}          `xml:"GetTemplateResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  getTemplateResult `xml:"GetTemplateResult"`
	Meta    cfnResponseMeta   `xml:"ResponseMetadata"`
}

type createChangeSetResp struct {
	XMLName struct{}          `xml:"CreateChangeSetResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  changeSetIdResult `xml:"CreateChangeSetResult"`
	Meta    cfnResponseMeta   `xml:"ResponseMetadata"`
}

type describeChangeSetResp struct {
	XMLName struct{}                `xml:"DescribeChangeSetResponse"`
	Xmlns   string                  `xml:"xmlns,attr"`
	Result  describeChangeSetResult `xml:"DescribeChangeSetResult"`
	Meta    cfnResponseMeta         `xml:"ResponseMetadata"`
}

type listChangeSetsResp struct {
	XMLName struct{}             `xml:"ListChangeSetsResponse"`
	Xmlns   string               `xml:"xmlns,attr"`
	Result  listChangeSetsResult `xml:"ListChangeSetsResult"`
	Meta    cfnResponseMeta      `xml:"ResponseMetadata"`
}

type describeStackResourcesResp struct {
	XMLName struct{}                     `xml:"DescribeStackResourcesResponse"`
	Xmlns   string                       `xml:"xmlns,attr"`
	Result  describeStackResourcesResult `xml:"DescribeStackResourcesResult"`
	Meta    cfnResponseMeta              `xml:"ResponseMetadata"`
}

type listStackResourcesResp struct {
	XMLName struct{}                 `xml:"ListStackResourcesResponse"`
	Xmlns   string                   `xml:"xmlns,attr"`
	Result  listStackResourcesResult `xml:"ListStackResourcesResult"`
	Meta    cfnResponseMeta          `xml:"ResponseMetadata"`
}

type describeStackEventsResp struct {
	XMLName struct{}                  `xml:"DescribeStackEventsResponse"`
	Xmlns   string                    `xml:"xmlns,attr"`
	Result  describeStackEventsResult `xml:"DescribeStackEventsResult"`
	Meta    cfnResponseMeta           `xml:"ResponseMetadata"`
}

type getTemplateSummaryResp struct {
	XMLName struct{}              `xml:"GetTemplateSummaryResponse"`
	Xmlns   string                `xml:"xmlns,attr"`
	Result  templateSummaryResult `xml:"GetTemplateSummaryResult"`
	Meta    cfnResponseMeta       `xml:"ResponseMetadata"`
}

type validateTemplateResp struct {
	XMLName struct{}               `xml:"ValidateTemplateResponse"`
	Xmlns   string                 `xml:"xmlns,attr"`
	Result  validateTemplateResult `xml:"ValidateTemplateResult"`
	Meta    cfnResponseMeta        `xml:"ResponseMetadata"`
}

type listExportsResp struct {
	XMLName struct{}          `xml:"ListExportsResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  listExportsResult `xml:"ListExportsResult"`
	Meta    cfnResponseMeta   `xml:"ResponseMetadata"`
}

type listImportsResp struct {
	XMLName struct{}          `xml:"ListImportsResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  listImportsResult `xml:"ListImportsResult"`
	Meta    cfnResponseMeta   `xml:"ResponseMetadata"`
}

// ── Typed handler functions ────────────────────────────────────────

func (h *Handler) createStackTyped(ctx context.Context, req *createStackReq) (*createStackResp, *protocol.AWSError) {
	if req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}

	existing, _ := h.store.getStack(ctx, req.StackName)
	if existing != nil && existing.Status != StatusDeleteComplete {
		return nil, cfnerr("AlreadyExistsException",
			fmt.Sprintf("Stack [%s] already exists", req.StackName), http.StatusBadRequest)
	}

	templateBody, tplErr := h.resolveTypedTemplateBody(ctx, req.TemplateBody, req.TemplateURL)
	if tplErr != nil {
		return nil, cfnerr("ValidationError", tplErr.Error(), http.StatusBadRequest)
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		return nil, cfnerr("ValidationError", err.Error(), http.StatusBadRequest)
	}

	region := h.cfg.Region

	stackID := fmt.Sprintf("arn:aws:cloudformation:%s:%s:stack/%s/%s",
		region, h.cfg.AccountID, req.StackName, uuid.NewString())

	params := typedCollectParams(req.Parameters)
	tags := typedCollectTags(req.Tags)
	caps := req.Capabilities

	stack := &Stack{
		StackName:       req.StackName,
		StackID:         stackID,
		Region:          region,
		TemplateBody:    templateBody,
		Parameters:      params,
		Tags:            tags,
		Capabilities:    caps,
		RoleARN:         req.RoleARN,
		DisableRollback: req.DisableRollback == "true",
		Status:          StatusCreateInProgress,
		StatusReason:    "User Initiated",
		CreatedAt:       h.clk.Now(),
	}

	if err := h.store.putStack(ctx, stack); err != nil {
		return nil, cfnerr("InternalFailure", "failed to persist stack", http.StatusInternalServerError)
	}

	h.prov.createStack(stack, tmpl)

	return &createStackResp{
		Xmlns:  cfnXMLNS,
		Result: stackIdResult{StackId: stackID},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) updateStackTyped(ctx context.Context, req *updateStackReq) (*updateStackResp, *protocol.AWSError) {
	if req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}

	stack, aerr := h.store.getStack(ctx, req.StackName)
	if aerr != nil || stack == nil {
		return nil, cfnerr("ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", req.StackName), http.StatusBadRequest)
	}

	templateBody := req.TemplateBody
	if req.TemplateURL != "" {
		var tplErr error
		templateBody, tplErr = h.resolveTypedTemplateBody(ctx, req.TemplateBody, req.TemplateURL)
		if tplErr != nil {
			return nil, cfnerr("ValidationError", tplErr.Error(), http.StatusBadRequest)
		}
	}
	if templateBody == "" {
		templateBody = stack.TemplateBody
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		return nil, cfnerr("ValidationError", err.Error(), http.StatusBadRequest)
	}

	params := typedCollectParams(req.Parameters)
	if len(params) > 0 {
		stack.Parameters = params
	}
	tags := typedCollectTags(req.Tags)
	if len(tags) > 0 {
		stack.Tags = tags
	}

	stack.TemplateBody = templateBody
	stack.Status = StatusUpdateInProgress
	stack.StatusReason = "User Initiated"
	now := h.clk.Now()
	stack.UpdatedAt = &now

	if err := h.store.putStack(ctx, stack); err != nil {
		return nil, cfnerr("InternalFailure", "failed to persist stack", http.StatusInternalServerError)
	}

	h.prov.updateStack(stack, tmpl)

	return &updateStackResp{
		Xmlns:  cfnXMLNS,
		Result: stackIdResult{StackId: stack.StackID},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) deleteStackTyped(ctx context.Context, req *deleteStackReq) (*struct{}, *protocol.AWSError) {
	if req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}

	stack, aerr := h.store.getStack(ctx, req.StackName)
	if aerr != nil || stack == nil {
		return &struct{}{}, nil
	}

	stack.Status = StatusDeleteInProgress
	stack.StatusReason = "User Initiated"
	if err := h.store.putStack(ctx, stack); err != nil {
		return nil, cfnerr("InternalFailure", "failed to persist stack", http.StatusInternalServerError)
	}

	h.prov.deleteStack(stack)

	return &struct{}{}, nil
}

func (h *Handler) describeStacksTyped(ctx context.Context, req *describeStacksReq) (*describeStacksResp, *protocol.AWSError) {
	if req.StackName != "" {
		stack, aerr := h.store.getStack(ctx, req.StackName)
		if aerr != nil {
			return nil, cfnerr("InternalFailure", "failed to read stack", http.StatusInternalServerError)
		}
		if stack == nil {
			return nil, cfnerr("ValidationError",
				fmt.Sprintf("Stack with id %s does not exist", req.StackName), http.StatusBadRequest)
		}
		return &describeStacksResp{
			Xmlns:  cfnXMLNS,
			Result: describeStacksResult{Stacks: []stackXML{toStackXML(stack)}},
			Meta:   metaFromCFNCtx(ctx),
		}, nil
	}

	stacks, aerr := h.store.listStacks(ctx)
	if aerr != nil {
		return nil, cfnerr("InternalFailure", "failed to list stacks", http.StatusInternalServerError)
	}
	var items []stackXML
	for _, s := range stacks {
		if s.Status != StatusDeleteComplete {
			items = append(items, toStackXML(s))
		}
	}
	return &describeStacksResp{
		Xmlns:  cfnXMLNS,
		Result: describeStacksResult{Stacks: items},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) listStacksTyped(ctx context.Context, _ *listStacksReq) (*listStacksResp, *protocol.AWSError) {
	stacks, aerr := h.store.listStacks(ctx)
	if aerr != nil {
		return nil, cfnerr("InternalFailure", "failed to list stacks", http.StatusInternalServerError)
	}
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
	return &listStacksResp{
		Xmlns:  cfnXMLNS,
		Result: listStacksResult{StackSummaries: summaries},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) getTemplateTyped(ctx context.Context, req *getTemplateReq) (*getTemplateResp, *protocol.AWSError) {
	if req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}
	stack, aerr := h.store.getStack(ctx, req.StackName)
	if aerr != nil || stack == nil {
		return nil, cfnerr("ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", req.StackName), http.StatusBadRequest)
	}
	return &getTemplateResp{
		Xmlns:  cfnXMLNS,
		Result: getTemplateResult{TemplateBody: stack.TemplateBody},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) createChangeSetTyped(ctx context.Context, req *createChangeSetReq) (*createChangeSetResp, *protocol.AWSError) {
	if req.StackName == "" || req.ChangeSetName == "" {
		return nil, cfnerr("ValidationError", "StackName and ChangeSetName are required", http.StatusBadRequest)
	}

	templateBody, tplErr := h.resolveTypedTemplateBody(ctx, req.TemplateBody, req.TemplateURL)
	if tplErr != nil {
		return nil, cfnerr("ValidationError", tplErr.Error(), http.StatusBadRequest)
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		return nil, cfnerr("ValidationError", err.Error(), http.StatusBadRequest)
	}

	changeSetType := req.ChangeSetType
	if changeSetType == "" {
		changeSetType = "UPDATE"
	}

	chsRegion := h.cfg.Region

	stack, _ := h.store.getStack(ctx, req.StackName)
	var stackID string
	if stack == nil {
		if changeSetType != "CREATE" {
			return nil, cfnerr("ValidationError",
				fmt.Sprintf("Stack [%s] does not exist", req.StackName), http.StatusBadRequest)
		}
		stackID = fmt.Sprintf("arn:aws:cloudformation:%s:%s:stack/%s/%s",
			chsRegion, h.cfg.AccountID, req.StackName, uuid.NewString())
		stack = &Stack{
			StackName: req.StackName,
			StackID:   stackID,
			Region:    chsRegion,
			Status:    "REVIEW_IN_PROGRESS",
			CreatedAt: h.clk.Now(),
		}
		if err := h.store.putStack(ctx, stack); err != nil {
			return nil, cfnerr("InternalFailure", "failed to create stack placeholder", http.StatusInternalServerError)
		}
	} else {
		stackID = stack.StackID
	}

	csID := fmt.Sprintf("arn:aws:cloudformation:%s:%s:changeSet/%s/%s",
		chsRegion, h.cfg.AccountID, req.ChangeSetName, uuid.NewString())

	changes := computeChanges(tmpl, stack, changeSetType)

	cs := &ChangeSet{
		ChangeSetName:   req.ChangeSetName,
		ChangeSetID:     csID,
		StackID:         stackID,
		StackName:       req.StackName,
		TemplateBody:    templateBody,
		Parameters:      typedCollectParams(req.Parameters),
		Tags:            typedCollectTags(req.Tags),
		Capabilities:    req.Capabilities,
		Status:          ChangeSetStatusCreateComplete,
		ChangeSetType:   changeSetType,
		Changes:         changes,
		CreatedAt:       h.clk.Now(),
		ExecutionStatus: ExecStatusAvailable,
	}

	if err := h.store.putChangeSet(ctx, cs); err != nil {
		return nil, cfnerr("InternalFailure", "failed to persist change set", http.StatusInternalServerError)
	}

	return &createChangeSetResp{
		Xmlns:  cfnXMLNS,
		Result: changeSetIdResult{Id: csID, StackId: stackID},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) describeChangeSetTyped(ctx context.Context, req *describeChangeSetReq) (*describeChangeSetResp, *protocol.AWSError) {
	if req.ChangeSetName == "" {
		return nil, cfnerr("ValidationError", "ChangeSetName is required", http.StatusBadRequest)
	}
	if req.StackName == "" && !isARN(req.ChangeSetName) {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}

	cs, aerr := h.store.getChangeSet(ctx, req.StackName, req.ChangeSetName)
	if aerr != nil || cs == nil {
		return nil, cfnerr("ChangeSetNotFoundException",
			fmt.Sprintf("ChangeSet [%s] does not exist", req.ChangeSetName), http.StatusBadRequest)
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

	return &describeChangeSetResp{
		Xmlns: cfnXMLNS,
		Result: describeChangeSetResult{
			ChangeSetName:   cs.ChangeSetName,
			ChangeSetID:     cs.ChangeSetID,
			StackID:         cs.StackID,
			StackName:       cs.StackName,
			Status:          cs.Status,
			ExecutionStatus: cs.ExecutionStatus,
			ChangeSetType:   cs.ChangeSetType,
			CreatedAt:       cs.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			Changes:         changesXML,
		},
		Meta: metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) executeChangeSetTyped(ctx context.Context, req *executeChangeSetReq) (*struct{}, *protocol.AWSError) {
	if req.ChangeSetName == "" || (req.StackName == "" && !isARN(req.ChangeSetName)) {
		return nil, cfnerr("ValidationError", "StackName and ChangeSetName are required", http.StatusBadRequest)
	}

	cs, aerr := h.store.getChangeSet(ctx, req.StackName, req.ChangeSetName)
	if aerr != nil || cs == nil {
		return nil, cfnerr("ChangeSetNotFoundException",
			fmt.Sprintf("ChangeSet [%s] does not exist", req.ChangeSetName), http.StatusBadRequest)
	}

	if cs.ExecutionStatus != ExecStatusAvailable {
		return nil, cfnerr("InvalidChangeSetStatus",
			fmt.Sprintf("ChangeSet [%s] is in %s state and cannot be executed", req.ChangeSetName, cs.ExecutionStatus),
			http.StatusBadRequest)
	}

	stack, _ := h.store.getStack(ctx, cs.StackName)
	if stack == nil {
		return nil, cfnerr("ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", cs.StackName), http.StatusBadRequest)
	}

	tmpl, err := parseTemplate(cs.TemplateBody)
	if err != nil {
		return nil, cfnerr("ValidationError", err.Error(), http.StatusBadRequest)
	}

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
		h.prov.createStack(stack, tmpl)
	} else {
		stack.Status = StatusUpdateInProgress
		stack.StatusReason = "User Initiated"
		now := h.clk.Now()
		stack.UpdatedAt = &now
		_ = h.store.putStack(ctx, stack)
		h.prov.updateStack(stack, tmpl)
	}

	return &struct{}{}, nil
}

func (h *Handler) deleteChangeSetTyped(ctx context.Context, req *deleteChangeSetReq) (*struct{}, *protocol.AWSError) {
	if req.ChangeSetName == "" || req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName and ChangeSetName are required", http.StatusBadRequest)
	}

	_ = h.store.deleteChangeSet(ctx, req.StackName, req.ChangeSetName)
	return &struct{}{}, nil
}

func (h *Handler) listChangeSetsTyped(ctx context.Context, req *listChangeSetsReq) (*listChangeSetsResp, *protocol.AWSError) {
	if req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}

	css, aerr := h.store.listChangeSetsForStack(ctx, req.StackName)
	if aerr != nil {
		return nil, cfnerr("InternalFailure", "failed to list change sets", http.StatusInternalServerError)
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

	return &listChangeSetsResp{
		Xmlns:  cfnXMLNS,
		Result: listChangeSetsResult{Summaries: summaries},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) describeStackResourcesTyped(ctx context.Context, req *describeStackResourcesReq) (*describeStackResourcesResp, *protocol.AWSError) {
	if req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}

	stack, aerr := h.store.getStack(ctx, req.StackName)
	if aerr != nil || stack == nil {
		return nil, cfnerr("ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", req.StackName), http.StatusBadRequest)
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

	return &describeStackResourcesResp{
		Xmlns:  cfnXMLNS,
		Result: describeStackResourcesResult{StackResources: resources},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) listStackResourcesTyped(ctx context.Context, req *listStackResourcesReq) (*listStackResourcesResp, *protocol.AWSError) {
	if req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}

	stack, aerr := h.store.getStack(ctx, req.StackName)
	if aerr != nil || stack == nil {
		return nil, cfnerr("ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", req.StackName), http.StatusBadRequest)
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

	return &listStackResourcesResp{
		Xmlns:  cfnXMLNS,
		Result: listStackResourcesResult{Summaries: summaries},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) describeStackEventsTyped(ctx context.Context, req *describeStackEventsReq) (*describeStackEventsResp, *protocol.AWSError) {
	if req.StackName == "" {
		return nil, cfnerr("ValidationError", "StackName is required", http.StatusBadRequest)
	}

	stack, aerr := h.store.getStack(ctx, req.StackName)
	if aerr != nil || stack == nil {
		return nil, cfnerr("ValidationError",
			fmt.Sprintf("Stack [%s] does not exist", req.StackName), http.StatusBadRequest)
	}

	allEvents, err := h.store.getStackEvents(ctx, req.StackName)
	if err != nil {
		return nil, cfnerr("InternalError", "failed to load stack events", http.StatusInternalServerError)
	}

	n := len(allEvents)
	reversed := make([]StackEvent, n)
	for i, e := range allEvents {
		reversed[n-1-i] = e
	}

	page := serviceutil.Paginate(reversed, eventsPageSize, req.NextToken)

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

	return &describeStackEventsResp{
		Xmlns:  cfnXMLNS,
		Result: describeStackEventsResult{StackEvents: eventsXML, NextToken: page.NextToken},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) getTemplateSummaryTyped(ctx context.Context, req *getTemplateSummaryReq) (*getTemplateSummaryResp, *protocol.AWSError) {
	templateBody := req.TemplateBody

	if templateBody == "" && req.TemplateURL != "" {
		var err error
		templateBody, err = h.resolveTypedTemplateBody(ctx, req.TemplateBody, req.TemplateURL)
		if err != nil {
			return nil, cfnerr("ValidationError", err.Error(), http.StatusBadRequest)
		}
	}

	if templateBody == "" && req.StackName != "" {
		stack, aerr := h.store.getStack(ctx, req.StackName)
		if aerr != nil || stack == nil {
			return nil, cfnerr("ValidationError",
				fmt.Sprintf("Stack [%s] does not exist", req.StackName), http.StatusBadRequest)
		}
		templateBody = stack.TemplateBody
	}

	if templateBody == "" {
		return nil, cfnerr("ValidationError", "TemplateBody, TemplateURL, or StackName is required", http.StatusBadRequest)
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		return nil, cfnerr("ValidationError", err.Error(), http.StatusBadRequest)
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

	return &getTemplateSummaryResp{
		Xmlns: cfnXMLNS,
		Result: templateSummaryResult{
			Description:   tmpl.Description,
			Parameters:    paramDecls,
			ResourceTypes: resourceTypes,
		},
		Meta: metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) validateTemplateTyped(ctx context.Context, req *validateTemplateReq) (*validateTemplateResp, *protocol.AWSError) {
	templateBody, tplErr := h.resolveTypedTemplateBody(ctx, req.TemplateBody, req.TemplateURL)
	if tplErr != nil {
		return nil, cfnerr("ValidationError", tplErr.Error(), http.StatusBadRequest)
	}

	tmpl, err := parseTemplate(templateBody)
	if err != nil {
		return nil, cfnerr("ValidationError", err.Error(), http.StatusBadRequest)
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

	return &validateTemplateResp{
		Xmlns: cfnXMLNS,
		Result: validateTemplateResult{
			Description: tmpl.Description,
			Parameters:  paramDecls,
		},
		Meta: metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) listExportsTyped(ctx context.Context, _ *struct{}) (*listExportsResp, *protocol.AWSError) {
	exports, aerr := h.store.listExports(ctx)
	if aerr != nil {
		return nil, cfnerr("InternalFailure", "failed to list exports", http.StatusInternalServerError)
	}

	items := make([]exportXML, 0, len(exports))
	for _, e := range exports {
		items = append(items, exportXML(e))
	}

	return &listExportsResp{
		Xmlns:  cfnXMLNS,
		Result: listExportsResult{Exports: items},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

func (h *Handler) listImportsTyped(ctx context.Context, req *listImportsReq) (*listImportsResp, *protocol.AWSError) {
	if req.ExportName == "" {
		return nil, cfnerr("ValidationError", "ExportName is required", http.StatusBadRequest)
	}

	importers, aerr := h.store.listImportingStacks(ctx, req.ExportName)
	if aerr != nil {
		return nil, cfnerr("InternalFailure", "failed to list imports", http.StatusInternalServerError)
	}

	return &listImportsResp{
		Xmlns:  cfnXMLNS,
		Result: listImportsResult{Imports: importers},
		Meta:   metaFromCFNCtx(ctx),
	}, nil
}

// ── Typed helpers ───────────────────────────────────────────────────

func cfnerr(code, message string, httpStatus int) *protocol.AWSError {
	return &protocol.AWSError{Code: code, Message: message, HTTPStatus: httpStatus}
}

func metaFromCFNCtx(ctx context.Context) cfnResponseMeta {
	return cfnResponseMeta{RequestId: protocol.RequestIDFromContext(ctx)}
}

func typedCollectParams(members []cfnParamMember) []Parameter {
	params := make([]Parameter, 0, len(members))
	for _, m := range members {
		if m.ParameterKey != "" {
			params = append(params, Parameter{Key: m.ParameterKey, Value: m.ParameterValue})
		}
	}
	return params
}

func typedCollectTags(members []cfnTagMember) []Tag {
	tags := make([]Tag, 0, len(members))
	for _, m := range members {
		if m.Key != "" {
			tags = append(tags, Tag(m))
		}
	}
	return tags
}

func (h *Handler) resolveTypedTemplateBody(ctx context.Context, templateBody, templateURL string) (string, error) {
	if templateBody != "" {
		return templateBody, nil
	}
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
	region := h.cfg.Region
	rec, err := internalRequest(context.Background(), router, region, http.MethodGet, u.Path, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch template from %s: %w", templateURL, err)
	}
	return rec.Body.String(), nil
}

var _ = middleware.RegionFromContext

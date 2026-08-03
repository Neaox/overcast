package iam

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Neaox/overcast/internal/iampolicy"
	"github.com/Neaox/overcast/internal/protocol"
)

// Policy simulation: SimulatePrincipalPolicy and SimulateCustomPolicy.
//
// Both operations answer "what would happen", using the same evaluator the
// opt-in enforcement middleware uses (internal/iampolicy), so a simulation
// result describes what enforcement would decide for the same call.
//
// Simulation is read-only and carries no fidelity risk of its own: it never
// changes how any other request is handled. Request-time enforcement remains
// opt-in through OVERCAST_ENFORCE_IAM and defaults off.

// ─── Request types ───────────────────────────────────────────────────────────

type contextEntryReq struct {
	ContextKeyName   string   `json:"ContextKeyName"`
	ContextKeyValues []string `json:"ContextKeyValues"`
	ContextKeyType   string   `json:"ContextKeyType"`
}

type simulateCustomPolicyReq struct {
	PolicyInputList                    []string          `json:"PolicyInputList"`
	PermissionsBoundaryPolicyInputList []string          `json:"PermissionsBoundaryPolicyInputList"`
	ActionNames                        []string          `json:"ActionNames"`
	ResourceArns                       []string          `json:"ResourceArns"`
	ResourcePolicy                     string            `json:"ResourcePolicy"`
	ResourceOwner                      string            `json:"ResourceOwner"`
	CallerArn                          string            `json:"CallerArn"`
	ContextEntries                     []contextEntryReq `json:"ContextEntries"`
	Marker                             string            `json:"Marker"`
	MaxItems                           int               `json:"MaxItems"`
}

// ─── Response types ──────────────────────────────────────────────────────────

// positionXML is AWS's Position type: a 1-based row/column into a policy
// document, used by statementXML's StartPosition/EndPosition.
type positionXML struct {
	Line   int `xml:"Line"`
	Column int `xml:"Column"`
}

// statementXML is AWS's Statement type (the MatchedStatements member).
// StartPosition/EndPosition point at the deciding statement's opening `{`
// and closing `}` within the exact document text supplied on the wire
// (PolicyInputList entry, inline/managed policy document, or ResourcePolicy).
// A consumer can use them to tell apart two statements that share one
// document — the gap this type closes — and, given the same document text
// back, to locate the exact statement. They are Overcast's own byte-accurate
// computation, not copied from any upstream source; nothing else about the
// wire format changes.
type statementXML struct {
	SourcePolicyId   string       `xml:"SourcePolicyId"`
	SourcePolicyType string       `xml:"SourcePolicyType"`
	StartPosition    *positionXML `xml:"StartPosition,omitempty"`
	EndPosition      *positionXML `xml:"EndPosition,omitempty"`
}

type boundaryDetailXML struct {
	AllowedByPermissionsBoundary bool `xml:"AllowedByPermissionsBoundary"`
}

type resourceSpecificResultXML struct {
	EvalResourceName            string                       `xml:"EvalResourceName"`
	EvalResourceDecision        string                       `xml:"EvalResourceDecision"`
	MatchedStatements           listMembersXML[statementXML] `xml:"MatchedStatements"`
	MissingContextValues        listMembersXML[string]       `xml:"MissingContextValues"`
	PermissionsBoundaryDecision *boundaryDetailXML           `xml:"PermissionsBoundaryDecisionDetail,omitempty"`
}

type evaluationResultXML struct {
	EvalActionName              string                                     `xml:"EvalActionName"`
	EvalResourceName            string                                     `xml:"EvalResourceName"`
	EvalDecision                string                                     `xml:"EvalDecision"`
	MatchedStatements           listMembersXML[statementXML]               `xml:"MatchedStatements"`
	MissingContextValues        listMembersXML[string]                     `xml:"MissingContextValues"`
	PermissionsBoundaryDecision *boundaryDetailXML                         `xml:"PermissionsBoundaryDecisionDetail,omitempty"`
	ResourceSpecificResults     *listMembersXML[resourceSpecificResultXML] `xml:"ResourceSpecificResults,omitempty"`
}

type simulateResultXML struct {
	EvaluationResults listMembersXML[evaluationResultXML] `xml:"EvaluationResults"`
	IsTruncated       bool                                `xml:"IsTruncated"`
}

type simulateCustomPolicyResp struct {
	XMLName struct{}          `xml:"SimulateCustomPolicyResponse"`
	Xmlns   string            `xml:"xmlns,attr"`
	Result  simulateResultXML `xml:"SimulateCustomPolicyResult"`
	Meta    respMeta          `xml:"ResponseMetadata"`
}

// ─── Errors ──────────────────────────────────────────────────────────────────

// errInvalidInput is AWS's InvalidInput, returned when a supplied policy
// document cannot be parsed.
func errInvalidInput(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidInput",
		Message:    msg,
		HTTPStatus: http.StatusBadRequest,
	}
}

// errPolicyEvaluation is AWS's PolicyEvaluation error: "the request failed
// because a provided policy could not be successfully evaluated". It is how a
// construct this evaluator does not implement is reported — never as a
// fabricated allow or deny.
func errPolicyEvaluation(details []string) *protocol.AWSError {
	return &protocol.AWSError{
		Code: "PolicyEvaluation",
		Message: "The request failed because a provided policy could not be successfully evaluated: " +
			strings.Join(details, "; "),
		HTTPStatus: http.StatusInternalServerError,
	}
}

// ─── Typed handlers ──────────────────────────────────────────────────────────

func (h *Handler) simulatePrincipalPolicyTyped(ctx context.Context, req *simulatePrincipalPolicyReq) (*simulatePrincipalPolicyResp, *protocol.AWSError) {
	if strings.TrimSpace(req.PolicySourceArn) == "" {
		return nil, protocol.ErrMissingParameter("PolicySourceArn")
	}
	if len(req.ActionNames) == 0 {
		return nil, protocol.ErrMissingParameter("ActionNames")
	}

	identityDocs, principal, aerr := h.principalPolicyDocuments(ctx, req.PolicySourceArn)
	if aerr != nil {
		return nil, aerr
	}
	for i, doc := range req.PolicyInputList {
		identityDocs = append(identityDocs, iampolicy.SourcedDocument{
			Source:   iampolicy.SourceRef{ID: fmt.Sprintf("PolicyInputList.%d", i+1), Type: iampolicy.SourceTypeInputPolicy},
			Document: doc,
		})
	}

	callerArn := strings.TrimSpace(req.CallerArn)
	if callerArn == "" {
		callerArn = principal.arn
	}

	result, aerr := h.runSimulation(simulationInput{
		identityDocs:   identityDocs,
		boundaryDocs:   req.PermissionsBoundaryPolicyInputList,
		resourcePolicy: req.ResourcePolicy,
		actions:        req.ActionNames,
		resources:      req.ResourceArns,
		contextEntries: req.ContextEntries,
		callerArn:      callerArn,
		callerAccount:  accountFromARN(callerArn),
		principalName:  principal.name,
	})
	if aerr != nil {
		return nil, aerr
	}

	return &simulatePrincipalPolicyResp{
		Xmlns:  iamXMLNS,
		Result: *result,
		Meta:   metaFromCtx(ctx),
	}, nil
}

func (h *Handler) simulateCustomPolicyTyped(ctx context.Context, req *simulateCustomPolicyReq) (*simulateCustomPolicyResp, *protocol.AWSError) {
	if len(req.PolicyInputList) == 0 {
		return nil, protocol.ErrMissingParameter("PolicyInputList")
	}
	if len(req.ActionNames) == 0 {
		return nil, protocol.ErrMissingParameter("ActionNames")
	}

	identityDocs := make([]iampolicy.SourcedDocument, 0, len(req.PolicyInputList))
	for i, doc := range req.PolicyInputList {
		identityDocs = append(identityDocs, iampolicy.SourcedDocument{
			Source:   iampolicy.SourceRef{ID: fmt.Sprintf("PolicyInputList.%d", i+1), Type: iampolicy.SourceTypeInputPolicy},
			Document: doc,
		})
	}

	callerArn := strings.TrimSpace(req.CallerArn)
	result, aerr := h.runSimulation(simulationInput{
		identityDocs:   identityDocs,
		boundaryDocs:   req.PermissionsBoundaryPolicyInputList,
		resourcePolicy: req.ResourcePolicy,
		actions:        req.ActionNames,
		resources:      req.ResourceArns,
		contextEntries: req.ContextEntries,
		callerArn:      callerArn,
		callerAccount:  accountFromARN(callerArn),
	})
	if aerr != nil {
		return nil, aerr
	}

	return &simulateCustomPolicyResp{
		Xmlns:  iamXMLNS,
		Result: *result,
		Meta:   metaFromCtx(ctx),
	}, nil
}

// ─── Simulation core ─────────────────────────────────────────────────────────

type simulationInput struct {
	identityDocs   []iampolicy.SourcedDocument
	boundaryDocs   []string
	resourcePolicy string
	actions        []string
	resources      []string
	contextEntries []contextEntryReq
	callerArn      string
	callerAccount  string
	principalName  string
}

func (h *Handler) runSimulation(in simulationInput) (*simulateResultXML, *protocol.AWSError) {
	identity, err := iampolicy.Compile(in.identityDocs)
	if err != nil {
		return nil, errInvalidInput(err.Error())
	}

	var boundary []iampolicy.Statement
	if len(in.boundaryDocs) > 0 {
		docs := make([]iampolicy.SourcedDocument, 0, len(in.boundaryDocs))
		for i, doc := range in.boundaryDocs {
			docs = append(docs, iampolicy.SourcedDocument{
				Source: iampolicy.SourceRef{
					ID:   fmt.Sprintf("PermissionsBoundaryPolicyInputList.%d", i+1),
					Type: iampolicy.SourceTypeInputPolicy,
				},
				Document: doc,
			})
		}
		boundary, err = iampolicy.Compile(docs)
		if err != nil {
			return nil, errInvalidInput(err.Error())
		}
	}

	var resourceStatements []iampolicy.Statement
	if strings.TrimSpace(in.resourcePolicy) != "" {
		resourceStatements, err = iampolicy.ParseDocument(in.resourcePolicy,
			iampolicy.SourceRef{ID: "ResourcePolicy", Type: iampolicy.SourceTypeResourcePolicy})
		if err != nil {
			return nil, errInvalidInput(err.Error())
		}
	}

	simContext := h.simulationContext(in)

	resources := in.resources
	if len(resources) == 0 {
		resources = []string{"*"}
	}

	var (
		results     []evaluationResultXML
		unsupported []string
	)
	for _, action := range in.actions {
		perResource := make([]resourceSpecificResultXML, 0, len(resources))
		for _, resource := range resources {
			evaluated := evaluateOne(iampolicy.Input{
				Request: iampolicy.Request{
					Action:           action,
					Resource:         resource,
					Context:          simContext,
					PrincipalARN:     in.callerArn,
					PrincipalAccount: in.callerAccount,
				},
				Identity:       identity,
				ResourcePolicy: resourceStatements,
			}, boundary)
			unsupported = appendUnsupported(unsupported, evaluated.unsupported)
			perResource = append(perResource, evaluated.toResourceSpecific(resource))
		}

		// AWS reports one EvaluationResult per action, naming the first
		// resource; every resource also gets its own ResourceSpecificResult
		// when more than one was supplied.
		primary := perResource[0]
		result := evaluationResultXML{
			EvalActionName:              action,
			EvalResourceName:            primary.EvalResourceName,
			EvalDecision:                primary.EvalResourceDecision,
			MatchedStatements:           primary.MatchedStatements,
			MissingContextValues:        primary.MissingContextValues,
			PermissionsBoundaryDecision: primary.PermissionsBoundaryDecision,
		}
		if len(resources) > 1 {
			result.ResourceSpecificResults = &listMembersXML[resourceSpecificResultXML]{
				Members: perResource, Tag: "member",
			}
		}
		results = append(results, result)
	}

	if len(unsupported) > 0 {
		return nil, errPolicyEvaluation(unsupported)
	}

	return &simulateResultXML{
		EvaluationResults: listMembersXML[evaluationResultXML]{Members: results, Tag: "member"},
		IsTruncated:       false,
	}, nil
}

// evaluation is one (action, resource) outcome plus the permissions-boundary
// verdict that shaped it.
type evaluation struct {
	decision    iampolicy.Decision
	matched     []iampolicy.Match
	missing     []string
	unsupported []string
	boundary    *boundaryDetailXML
}

// evaluateOne applies the identity/resource evaluation, then caps it with the
// permissions boundary if one was supplied: an action allowed by the identity
// policies but not by the boundary is an implicit deny, which is how AWS
// reports a boundary-capped permission.
func evaluateOne(input iampolicy.Input, boundary []iampolicy.Statement) evaluation {
	res := iampolicy.Evaluate(input)
	out := evaluation{
		decision:    res.Decision,
		matched:     res.Matched,
		missing:     res.MissingContextKeys,
		unsupported: res.Unsupported,
	}
	if len(boundary) == 0 {
		return out
	}

	boundaryRes := iampolicy.Evaluate(iampolicy.Input{Request: input.Request, Identity: boundary})
	out.unsupported = appendUnsupported(out.unsupported, boundaryRes.Unsupported)
	allowedByBoundary := boundaryRes.Decision == iampolicy.DecisionAllowed
	out.boundary = &boundaryDetailXML{AllowedByPermissionsBoundary: allowedByBoundary}
	if !allowedByBoundary && out.decision == iampolicy.DecisionAllowed {
		out.decision = iampolicy.DecisionImplicitDeny
	}
	if boundaryRes.Decision == iampolicy.DecisionExplicitDeny {
		out.decision = iampolicy.DecisionExplicitDeny
	}
	return out
}

func (e evaluation) toResourceSpecific(resource string) resourceSpecificResultXML {
	matched := make([]statementXML, 0, len(e.matched))
	for _, m := range e.matched {
		matched = append(matched, statementXML{
			SourcePolicyId:   m.Source.ID,
			SourcePolicyType: m.Source.Type,
			StartPosition:    toPositionXML(m.StartPosition),
			EndPosition:      toPositionXML(m.EndPosition),
		})
	}
	return resourceSpecificResultXML{
		EvalResourceName:            resource,
		EvalResourceDecision:        string(e.decision),
		MatchedStatements:           listMembersXML[statementXML]{Members: matched, Tag: "member"},
		MissingContextValues:        listMembersXML[string]{Members: e.missing, Tag: "member"},
		PermissionsBoundaryDecision: e.boundary,
	}
}

// toPositionXML converts an internal iampolicy.Position to its wire shape,
// or nil if the position was never computed.
func toPositionXML(p *iampolicy.Position) *positionXML {
	if p == nil {
		return nil
	}
	return &positionXML{Line: p.Line, Column: p.Column}
}

func appendUnsupported(dst, src []string) []string {
	for _, s := range src {
		found := false
		for _, existing := range dst {
			if existing == s {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, s)
		}
	}
	return dst
}

// simulationContext builds the condition-key context for the simulation from
// the caller-supplied ContextEntries plus the keys AWS derives from the
// principal itself.
func (h *Handler) simulationContext(in simulationInput) map[string]string {
	ctx := make(map[string]string, len(in.contextEntries)+4)
	if in.callerArn != "" {
		ctx["aws:principalarn"] = in.callerArn
	}
	if in.callerAccount != "" {
		ctx["aws:principalaccount"] = in.callerAccount
	}
	if in.principalName != "" {
		ctx["aws:username"] = in.principalName
	}
	for _, entry := range in.contextEntries {
		name := strings.TrimSpace(entry.ContextKeyName)
		if name == "" || len(entry.ContextKeyValues) == 0 {
			continue
		}
		// Multi-valued context keys are matched against their first value;
		// ForAllValues/ForAnyValue set operators are not implemented, and a
		// policy using one is reported through the unknown-operator path.
		ctx[strings.ToLower(name)] = entry.ContextKeyValues[0]
	}
	return ctx
}

// ─── Principal resolution ────────────────────────────────────────────────────

type simulatedPrincipal struct {
	arn  string
	name string
}

// principalPolicyDocuments resolves a PolicySourceArn to the identity policies
// that apply to it: inline and attached policies for a user, role or group,
// plus the policies of every group a user belongs to.
func (h *Handler) principalPolicyDocuments(ctx context.Context, sourceArn string) ([]iampolicy.SourcedDocument, simulatedPrincipal, *protocol.AWSError) {
	kind, name, ok := parsePrincipalArn(sourceArn)
	if !ok {
		return nil, simulatedPrincipal{}, errInvalidInput(
			"The specified value for PolicySourceArn is invalid: " + sourceArn)
	}

	var docs []iampolicy.SourcedDocument
	switch kind {
	case "user":
		user, aerr := h.store.getUser(ctx, name)
		if aerr != nil {
			return nil, simulatedPrincipal{}, aerr
		}
		docs = h.appendIdentityDocs(ctx, docs, iampolicy.SourceTypeUser, user.InlinePolicies, user.AttachedPolicies)
		groups, aerr := h.store.listGroupsForUser(ctx, name)
		if aerr != nil {
			return nil, simulatedPrincipal{}, aerr
		}
		for i := range groups {
			docs = h.appendIdentityDocs(ctx, docs, iampolicy.SourceTypeGroup, groups[i].InlinePolicies, groups[i].AttachedPolicies)
		}
		return docs, simulatedPrincipal{arn: user.Arn, name: user.UserName}, nil

	case "role":
		role, aerr := h.store.getRole(ctx, name)
		if aerr != nil {
			return nil, simulatedPrincipal{}, aerr
		}
		docs = h.appendIdentityDocs(ctx, docs, iampolicy.SourceTypeRole, role.InlinePolicies, role.AttachedPolicies)
		return docs, simulatedPrincipal{arn: role.Arn, name: role.RoleName}, nil

	case "group":
		group, aerr := h.store.getGroup(ctx, name)
		if aerr != nil {
			return nil, simulatedPrincipal{}, aerr
		}
		docs = h.appendIdentityDocs(ctx, docs, iampolicy.SourceTypeGroup, group.InlinePolicies, group.AttachedPolicies)
		return docs, simulatedPrincipal{arn: group.Arn, name: group.GroupName}, nil

	default:
		return nil, simulatedPrincipal{}, errInvalidInput(
			"The specified value for PolicySourceArn is invalid: " + sourceArn)
	}
}

// appendIdentityDocs appends a principal's inline policies (in name order, so
// MatchedStatements is stable across calls) and its attached managed policies.
func (h *Handler) appendIdentityDocs(
	ctx context.Context,
	docs []iampolicy.SourcedDocument,
	inlineType string,
	inline map[string]string,
	attached []AttachedPolicy,
) []iampolicy.SourcedDocument {
	names := make([]string, 0, len(inline))
	for name := range inline {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		docs = append(docs, iampolicy.SourcedDocument{
			Source:   iampolicy.SourceRef{ID: name, Type: inlineType},
			Document: inline[name],
		})
	}

	for _, ref := range attached {
		if strings.TrimSpace(ref.PolicyArn) == "" {
			continue
		}
		policy, aerr := h.store.getPolicy(ctx, ref.PolicyArn)
		if aerr != nil || strings.TrimSpace(policy.Document) == "" {
			continue
		}
		id := policy.PolicyName
		if id == "" {
			id = ref.PolicyArn
		}
		docs = append(docs, iampolicy.SourcedDocument{
			Source:   iampolicy.SourceRef{ID: id, Type: iampolicy.SourceTypeIAMPolicy},
			Document: policy.Document,
		})
	}
	return docs
}

// parsePrincipalArn splits an IAM entity ARN into its kind (user/role/group)
// and name. Paths are tolerated: arn:aws:iam::…:user/team/alice yields alice.
func parsePrincipalArn(arn string) (kind, name string, ok bool) {
	trimmed := strings.TrimSpace(arn)
	if !strings.HasPrefix(trimmed, "arn:aws:iam::") {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, ":", 6)
	if len(parts) < 6 {
		return "", "", false
	}
	resource := parts[5]
	kind, path, found := strings.Cut(resource, "/")
	if !found || strings.TrimSpace(path) == "" {
		return "", "", false
	}
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		path = path[idx+1:]
	}
	if path == "" {
		return "", "", false
	}
	return kind, path, true
}

// accountFromARN returns the account ID segment of an ARN, or "".
func accountFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return ""
	}
	return strings.TrimSpace(parts[4])
}

package lambda

// handler_policy.go implements Lambda resource-policy operations.

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

const maxFunctionPolicyBytes = 20 * 1024

type addPermissionRequest struct {
	Action                string  `json:"Action"`
	EventSourceToken      string  `json:"EventSourceToken,omitempty"`
	FunctionURLAuthType   string  `json:"FunctionUrlAuthType,omitempty"`
	InvokedViaFunctionURL *bool   `json:"InvokedViaFunctionUrl,omitempty"`
	Principal             string  `json:"Principal"`
	PrincipalOrgID        string  `json:"PrincipalOrgID,omitempty"`
	RevisionID            string  `json:"RevisionId,omitempty"`
	SourceAccount         string  `json:"SourceAccount,omitempty"`
	SourceARN             string  `json:"SourceArn,omitempty"`
	StatementID           *string `json:"StatementId"`
}

type permissionStatement struct {
	Sid       string                       `json:"Sid"`
	Effect    string                       `json:"Effect"`
	Principal any                          `json:"Principal"`
	Action    string                       `json:"Action"`
	Resource  string                       `json:"Resource"`
	Condition map[string]map[string]string `json:"Condition,omitempty"`
}

type functionPolicy struct {
	RevisionID string                `json:"revision_id"`
	Statements []permissionStatement `json:"statements"`
}

type policyDocument struct {
	Version   string                `json:"Version"`
	ID        string                `json:"Id"`
	Statement []permissionStatement `json:"Statement"`
}

type getPolicyResponse struct {
	Policy     string `json:"Policy"`
	RevisionID string `json:"RevisionId"`
}

var (
	statementIDPattern        = regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)
	policyFunctionNamePattern = regexp.MustCompile(`^(?:arn:((?:aws[a-zA-Z-]*)?):lambda:)?(?:([a-z]{2}(?:(?:-gov)|(?:-iso[a-z]?))?-[a-z]+-\d{1}):)?(?:(\d{12}):)?(?:function:)?([a-zA-Z0-9-_\.]+)(?::(\$LATEST(?:\.PUBLISHED)?|[a-zA-Z0-9-_]+))?$`)
	qualifierPattern          = regexp.MustCompile(`^(?:\$LATEST(?:\.PUBLISHED)?|[a-zA-Z0-9-_$]+)$`)
	permissionActionPattern   = regexp.MustCompile(`^(?:\*|lambda:(?:\*|[a-zA-Z]+))$`)
	sourceAccountPattern      = regexp.MustCompile(`^[0-9]{12}$`)
	sourceARNPattern          = regexp.MustCompile(`^arn:aws[a-zA-Z0-9-]*:[a-zA-Z0-9-]+:(?:[a-z]{2}(?:-gov)?-[a-z]+-[0-9])?:(?:[0-9]{12})?:.*$`)
	principalOrgIDPattern     = regexp.MustCompile(`^o-[a-z0-9]{10,32}$`)
	eventSourceTokenPattern   = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

const (
	policyFunctionNameConstraint = `(arn:(aws[a-zA-Z-]*)?:lambda:)?([a-z]{2}((-gov)|(-iso([a-z]?)))?-[a-z]+-\d{1}:)?(\d{12}:)?(function:)?([a-zA-Z0-9-_\.]+)(:(\$LATEST(\.PUBLISHED)?|[a-zA-Z0-9-_]+))?`
	// qualifierConstraint is the Qualifier / NumericLatestPublishedOrAliasQualifier
	// pattern from the pinned Lambda model, reported verbatim in validation errors.
	qualifierConstraint        = `\$(LATEST(\.PUBLISHED)?)|[a-zA-Z0-9-_$]+`
	statementIDConstraint      = `([a-zA-Z0-9-_]+)`
	permissionActionConstraint = `(lambda:[*]|lambda:[a-zA-Z]+|[*])`
	principalConstraint        = `[^\s]+`
	sourceAccountConstraint    = `\d{12}`
	sourceARNConstraint        = `arn:(aws[a-zA-Z0-9-]*):([a-zA-Z0-9\-])+:([a-z]{2}(-gov)?-[a-z]+-\d{1})?:(\d{12})?:(.*)`
	principalOrgIDConstraint   = `o-[a-z0-9]{10,32}`
	eventSourceTokenConstraint = `[a-zA-Z0-9._\-]+`
)

func validatePermissionRequest(req addPermissionRequest) *protocol.AWSError {
	if req.Action == "" {
		return protocol.ErrMissingParameter("Action")
	}
	if req.Principal == "" {
		return protocol.ErrMissingParameter("Principal")
	}
	if req.StatementID == nil {
		return smithyRequiredConstraint("statementId")
	}
	if len(*req.StatementID) < 1 {
		return smithyStringMinimumLengthConstraint("statementId", *req.StatementID, 1)
	}
	if len(*req.StatementID) > 100 {
		return smithyStringLengthConstraint("statementId", *req.StatementID, 100)
	}
	if !statementIDPattern.MatchString(*req.StatementID) {
		return smithyPatternConstraint("statementId", *req.StatementID, statementIDConstraint)
	}
	if len(req.Action) > 10000 {
		return smithyStringLengthConstraint("action", req.Action, 10000)
	}
	if !permissionActionPattern.MatchString(req.Action) {
		return smithyPatternConstraint("action", req.Action, permissionActionConstraint)
	}
	if len(req.Principal) > 2048 {
		return smithyStringLengthConstraint("principal", req.Principal, 2048)
	}
	if strings.ContainsAny(req.Principal, "\r\n\t ") {
		return smithyPatternConstraint("principal", req.Principal, principalConstraint)
	}
	if req.SourceAccount != "" && len(req.SourceAccount) > 12 {
		return smithyStringLengthConstraint("sourceAccount", req.SourceAccount, 12)
	}
	if req.SourceAccount != "" && !sourceAccountPattern.MatchString(req.SourceAccount) {
		return smithyPatternConstraint("sourceAccount", req.SourceAccount, sourceAccountConstraint)
	}
	if req.SourceARN != "" && len(req.SourceARN) > 1024 {
		return smithyStringLengthConstraint("sourceArn", req.SourceARN, 1024)
	}
	if req.SourceARN != "" && !sourceARNPattern.MatchString(req.SourceARN) {
		return smithyPatternConstraint("sourceArn", req.SourceARN, sourceARNConstraint)
	}
	if req.PrincipalOrgID != "" && len(req.PrincipalOrgID) < 12 {
		return smithyStringMinimumLengthConstraint("principalOrgID", req.PrincipalOrgID, 12)
	}
	if len(req.PrincipalOrgID) > 34 {
		return smithyStringLengthConstraint("principalOrgID", req.PrincipalOrgID, 34)
	}
	if req.PrincipalOrgID != "" && !principalOrgIDPattern.MatchString(req.PrincipalOrgID) {
		return smithyPatternConstraint("principalOrgID", req.PrincipalOrgID, principalOrgIDConstraint)
	}
	if req.EventSourceToken != "" && len(req.EventSourceToken) > 256 {
		return smithyStringLengthConstraint("eventSourceToken", req.EventSourceToken, 256)
	}
	if req.EventSourceToken != "" && !eventSourceTokenPattern.MatchString(req.EventSourceToken) {
		return smithyPatternConstraint("eventSourceToken", req.EventSourceToken, eventSourceTokenConstraint)
	}
	if req.FunctionURLAuthType != "" && req.FunctionURLAuthType != "NONE" && req.FunctionURLAuthType != "AWS_IAM" {
		return lambdaInvalidParameter("1 validation error detected: Value '" + req.FunctionURLAuthType + "' at 'functionUrlAuthType' failed to satisfy constraint: Member must satisfy enum value set: [NONE, AWS_IAM]")
	}
	return nil
}

func (h *Handler) AddPermission(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithRecorder(r.Context())
	identifier := chi.URLParam(r, "name")
	name, qualifier := splitFunctionIdentifier(identifier, r.URL.Query().Get("Qualifier"))
	log.Debug("add permission", zap.String("function", name), zap.String("qualifier", qualifier))
	var req addPermissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}
	if aerr := validatePermissionRequest(req); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if qualifier == "$LATEST" {
		protocol.WriteJSONError(w, r, lambdaInvalidParameter("We currently do not support adding policies for $LATEST."))
		return
	}
	statementID := *req.StatementID
	fn, aerr := h.validatePolicyTarget(r.Context(), identifier, name, qualifier)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	statement := permissionStatement{
		Sid: statementID, Effect: "Allow", Principal: permissionPrincipal(req.Principal, fn.ARN),
		Action: req.Action, Resource: policyResourceARN(fn.ARN, qualifier), Condition: permissionConditions(req),
	}
	aerr = h.ls.mutateFunctionPolicy(r.Context(), name, qualifier, func(policy *functionPolicy, _ bool) (bool, *protocol.AWSError) {
		if req.RevisionID != "" && req.RevisionID != policy.RevisionID {
			return false, policyRevisionMismatch()
		}
		for _, existing := range policy.Statements {
			if existing.Sid == statementID {
				return false, &protocol.AWSError{Code: "ResourceConflictException", Message: "The statement id (" + statementID + ") provided already exists.", HTTPStatus: http.StatusConflict}
			}
		}
		candidate := append(append([]permissionStatement(nil), policy.Statements...), statement)
		document, err := json.Marshal(policyDocument{Version: "2012-10-17", ID: "default", Statement: candidate})
		if err != nil {
			return false, protocol.Wrap(protocol.ErrInternalError, err)
		}
		if len(document) > maxFunctionPolicyBytes {
			return false, &protocol.AWSError{Code: "PolicyLengthExceededException", Message: "The final policy size is bigger than the limit.", HTTPStatus: http.StatusBadRequest}
		}
		policy.Statements = candidate
		policy.RevisionID = uuid.NewString()
		return false, nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	statementJSON, err := json.Marshal(statement)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"Statement": string(statementJSON)})
}

func (h *Handler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	identifier := chi.URLParam(r, "name")
	name, qualifier := splitFunctionIdentifier(identifier, r.URL.Query().Get("Qualifier"))
	if _, aerr := h.validatePolicyTarget(r.Context(), identifier, name, qualifier); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	policy, found, aerr := h.ls.getFunctionPolicy(r.Context(), name, qualifier)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !found || len(policy.Statements) == 0 {
		protocol.WriteJSONError(w, r, policyNotFoundError(name, qualifier))
		return
	}
	documentJSON, err := json.Marshal(policyDocument{Version: "2012-10-17", ID: "default", Statement: policy.Statements})
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(getPolicyResponse{Policy: string(documentJSON), RevisionID: policy.RevisionID})
}

func (h *Handler) RemovePermission(w http.ResponseWriter, r *http.Request) {
	identifier := chi.URLParam(r, "name")
	name, qualifier := splitFunctionIdentifier(identifier, r.URL.Query().Get("Qualifier"))
	statementID := chi.URLParam(r, "statementId")
	if len(statementID) > 100 {
		protocol.WriteJSONError(w, r, smithyStringLengthConstraint("statementId", statementID, 100))
		return
	}
	if !statementIDPattern.MatchString(statementID) {
		protocol.WriteJSONError(w, r, smithyPatternConstraint("statementId", statementID, statementIDConstraint))
		return
	}
	if _, aerr := h.validatePolicyTarget(r.Context(), identifier, name, qualifier); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	aerr := h.ls.mutateFunctionPolicy(r.Context(), name, qualifier, func(policy *functionPolicy, found bool) (bool, *protocol.AWSError) {
		if !found {
			return false, policyNotFoundError(name, qualifier)
		}
		if revisionID := r.URL.Query().Get("RevisionId"); revisionID != "" && revisionID != policy.RevisionID {
			return false, policyRevisionMismatch()
		}
		remaining := make([]permissionStatement, 0, len(policy.Statements))
		removed := false
		for _, statement := range policy.Statements {
			if statement.Sid == statementID {
				removed = true
				continue
			}
			remaining = append(remaining, statement)
		}
		if !removed {
			return false, policyNotFoundError(name, qualifier)
		}
		policy.Statements = remaining
		policy.RevisionID = uuid.NewString()
		return len(remaining) == 0, nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) validatePolicyTarget(ctx context.Context, identifier, name, qualifier string) (*Function, *protocol.AWSError) {
	if len(identifier) > 256 {
		return nil, smithyStringLengthConstraint("functionName", identifier, 256)
	}
	if !policyFunctionNamePattern.MatchString(identifier) {
		return nil, smithyPatternConstraint("functionName", identifier, policyFunctionNameConstraint)
	}
	if len(name) > 64 {
		return nil, smithyStringLengthConstraint("functionName", name, 64)
	}
	if len(qualifier) > 128 {
		return nil, smithyStringLengthConstraint("qualifier", qualifier, 128)
	}
	if qualifier != "" && !qualifierPattern.MatchString(qualifier) {
		return nil, smithyPatternConstraint("qualifier", qualifier, qualifierConstraint)
	}
	if reference, ok := parsePolicyFunctionReference(identifier, ""); ok {
		expectedRegion := middleware.RegionFromContext(ctx, h.cfg.Region)
		if (reference.fullARN && reference.partition != "aws") ||
			(reference.region != "" && reference.region != expectedRegion) ||
			(reference.account != "" && reference.account != h.cfg.AccountID) {
			return nil, policyFunctionNotFoundIdentifier(identifier, qualifier)
		}
	}
	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	if fn == nil {
		return nil, policyFunctionNotFound(name, qualifier)
	}
	if qualifier == "" || qualifier == "$LATEST" {
		return fn, nil
	}
	if version, err := strconv.Atoi(qualifier); err == nil {
		versions, aerr := h.ls.listVersions(ctx, name)
		if aerr != nil {
			return nil, aerr
		}
		for _, candidate := range versions {
			if candidate.Version == version {
				return fn, nil
			}
		}
	} else {
		alias, aerr := h.ls.getAlias(ctx, name, qualifier)
		if aerr != nil {
			return nil, aerr
		}
		if alias != nil {
			return fn, nil
		}
	}
	return nil, policyFunctionNotFound(name, qualifier)
}

type policyFunctionReference struct {
	partition string
	region    string
	account   string
	name      string
	qualifier string
	fullARN   bool
}

func parsePolicyFunctionReference(identifier, queryQualifier string) (policyFunctionReference, bool) {
	match := policyFunctionNamePattern.FindStringSubmatch(identifier)
	if match == nil {
		return policyFunctionReference{}, false
	}
	reference := policyFunctionReference{
		partition: match[1],
		region:    match[2],
		account:   match[3],
		name:      match[4],
		qualifier: match[5],
		fullARN:   strings.HasPrefix(identifier, "arn:"),
	}
	if queryQualifier != "" {
		reference.qualifier = queryQualifier
	}
	return reference, true
}

// splitFunctionIdentifier separates a NamespacedFunctionName — a bare name, a
// name:qualifier, a partial ARN or a full ARN — into the function name and its
// qualifier, letting an explicit Qualifier query parameter win. Shared by the
// resource-policy handlers and DeleteFunction, both of which accept every form.
func splitFunctionIdentifier(identifier, queryQualifier string) (string, string) {
	reference, ok := parsePolicyFunctionReference(identifier, queryQualifier)
	if !ok {
		return identifier, queryQualifier
	}
	return reference.name, reference.qualifier
}

func permissionPrincipal(principal, functionARN string) any {
	if principal == "*" {
		return principal
	}
	if strings.HasSuffix(principal, ".amazonaws.com") {
		return map[string]string{"Service": principal}
	}
	if sourceAccountPattern.MatchString(principal) {
		partition := "aws"
		if parts := strings.SplitN(functionARN, ":", 3); len(parts) == 3 && parts[0] == "arn" && parts[1] != "" {
			partition = parts[1]
		}
		principal = "arn:" + partition + ":iam::" + principal + ":root"
	}
	return map[string]string{"AWS": principal}
}

func permissionConditions(req addPermissionRequest) map[string]map[string]string {
	conditions := make(map[string]map[string]string)
	add := func(operator, key, value string) {
		if value == "" {
			return
		}
		if conditions[operator] == nil {
			conditions[operator] = make(map[string]string)
		}
		conditions[operator][key] = value
	}
	add("ArnLike", "AWS:SourceArn", req.SourceARN)
	add("StringEquals", "AWS:SourceAccount", req.SourceAccount)
	add("StringEquals", "lambda:EventSourceToken", req.EventSourceToken)
	add("StringEquals", "aws:PrincipalOrgID", req.PrincipalOrgID)
	add("StringEquals", "lambda:FunctionUrlAuthType", req.FunctionURLAuthType)
	if req.InvokedViaFunctionURL != nil {
		add("Bool", "lambda:InvokedViaFunctionUrl", strconv.FormatBool(*req.InvokedViaFunctionURL))
	}
	if len(conditions) == 0 {
		return nil
	}
	return conditions
}

func policyResourceARN(functionARN, qualifier string) string {
	if qualifier == "" {
		return functionARN
	}
	return functionARN + ":" + qualifier
}

func policyRevisionMismatch() *protocol.AWSError {
	return &protocol.AWSError{Code: "PreconditionFailedException", Message: "The Revision Id provided does not match the latest Revision Id.", HTTPStatus: http.StatusPreconditionFailed}
}

func policyFunctionNotFound(name, qualifier string) *protocol.AWSError {
	return lambdaFunctionNotFound(qualifiedResourceName(name, qualifier))
}

func policyFunctionNotFoundIdentifier(identifier, qualifier string) *protocol.AWSError {
	resource := identifier
	if qualifier != "" && !strings.HasSuffix(identifier, ":"+qualifier) {
		resource += ":" + qualifier
	}
	return &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Function not found: " + resource, HTTPStatus: http.StatusNotFound}
}

func policyNotFoundError(name, qualifier string) *protocol.AWSError {
	return &protocol.AWSError{Code: "ResourceNotFoundException", Message: "The resource you requested does not exist: " + qualifiedResourceName(name, qualifier), HTTPStatus: http.StatusNotFound}
}

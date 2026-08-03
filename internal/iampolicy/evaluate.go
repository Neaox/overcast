package iampolicy

import (
	"fmt"
	"sort"
	"strings"
)

// Decision is the outcome of an evaluation, using AWS's
// PolicyEvaluationDecisionType vocabulary.
type Decision string

const (
	DecisionAllowed      Decision = "allowed"
	DecisionExplicitDeny Decision = "explicitDeny"
	DecisionImplicitDeny Decision = "implicitDeny"
)

// Request describes the API call being authorized.
type Request struct {
	// Action is the IAM action key, e.g. "s3:GetObject".
	Action string
	// Resource is the ARN the action is being performed on, or "*".
	Resource string
	// Context holds IAM condition keys, keyed lower-case
	// (e.g. "aws:requestedregion").
	Context map[string]string
	// PrincipalARN and PrincipalAccount identify the caller; they are used to
	// match a resource-based policy's Principal block.
	PrincipalARN     string
	PrincipalAccount string
}

// Input is one evaluation: a request, the principal's identity policies, and
// optionally the resource-based policy attached to the resource.
type Input struct {
	Request        Request
	Identity       []Statement
	ResourcePolicy []Statement
}

// Match records a statement that applied to the request. It maps onto AWS's
// MatchedStatements.
type Match struct {
	Source SourceRef
	Sid    string
	Effect string
	// Index is the statement's 0-based position within its source document.
	Index int
}

// Result is the outcome of an evaluation.
type Result struct {
	Decision Decision
	// Matched lists the statements that applied, deny statements first, in the
	// order they were evaluated.
	Matched []Match
	// MissingContextKeys names condition keys a matching statement needed that
	// the request did not carry. AWS reports these as MissingContextValues.
	MissingContextKeys []string
	// Unsupported names policy constructs this evaluator does not implement.
	// It is never empty-and-ignored: callers must surface it rather than treat
	// the decision as authoritative.
	Unsupported []string
}

// Evaluate applies AWS's evaluation logic: an explicit deny anywhere wins;
// otherwise an allow from either the identity policies or (within the same
// account) the resource-based policy grants access; otherwise the default of
// implicit deny applies.
//
// Evaluation allocates only when something has to be reported, so a caller
// that caches compiled statements can re-evaluate them on a hot request path.
func Evaluate(in Input) Result {
	var (
		res          Result
		rep          conditionReport
		allowed      bool
		explicitDeny bool
	)

	evaluateSet := func(stmts []Statement, resourceBased bool) {
		for i := range stmts {
			stmt := &stmts[i]
			if !stmt.appliesTo(in.Request, resourceBased, &rep) {
				continue
			}
			if !evaluateConditions(stmt.Condition, in.Request.Context, &rep) {
				continue
			}
			res.Matched = append(res.Matched, Match{
				Source: stmt.Source,
				Sid:    stmt.Sid,
				Effect: stmt.Effect,
				Index:  stmt.Index,
			})
			if stmt.Effect == "Deny" {
				explicitDeny = true
				continue
			}
			allowed = true
		}
	}

	evaluateSet(in.Identity, false)
	evaluateSet(in.ResourcePolicy, true)

	// Condition blocks are maps, so what got reported depends on map iteration
	// order. Sorting keeps the response wire-stable across identical calls.
	sort.Strings(rep.missing)
	sort.Strings(rep.unsupported)
	res.MissingContextKeys = rep.missing
	res.Unsupported = rep.unsupported

	switch {
	case explicitDeny:
		res.Decision = DecisionExplicitDeny
	case allowed:
		res.Decision = DecisionAllowed
	default:
		res.Decision = DecisionImplicitDeny
	}
	return res
}

// appliesTo reports whether the statement's Action/Resource (and, for a
// resource-based policy, Principal) elements select this request.
func (s *Statement) appliesTo(req Request, resourceBased bool, rep *conditionReport) bool {
	if !s.matchesAction(req.Action) {
		return false
	}
	if !s.matchesResource(req.Resource, req.Context) {
		return false
	}
	if resourceBased && !s.matchesPrincipal(req, rep) {
		return false
	}
	return true
}

func (s *Statement) matchesAction(action string) bool {
	// Action names are matched case-insensitively by AWS.
	if len(s.Action) > 0 {
		return matchAny(s.Action, action, nil, true)
	}
	if len(s.NotAction) > 0 {
		return !matchAny(s.NotAction, action, nil, true)
	}
	return false
}

func (s *Statement) matchesResource(resource string, ctx map[string]string) bool {
	// A resource-based policy statement may omit Resource entirely, in which
	// case it applies to the resource the policy is attached to.
	if len(s.Resource) == 0 && len(s.NotResource) == 0 {
		return s.Principal != nil || s.NotPrincipal != nil
	}
	if len(s.Resource) > 0 {
		return matchAny(s.Resource, resource, ctx, false)
	}
	return !matchAny(s.NotResource, resource, ctx, false)
}

func (s *Statement) matchesPrincipal(req Request, rep *conditionReport) bool {
	if s.NotPrincipal != nil {
		reportPrincipalGaps(s.NotPrincipal, rep)
		return !principalSetMatches(s.NotPrincipal, req)
	}
	if s.Principal == nil {
		// A resource-based policy statement without a Principal grants nothing.
		return false
	}
	reportPrincipalGaps(s.Principal, rep)
	return principalSetMatches(s.Principal, req)
}

func reportPrincipalGaps(set *PrincipalSet, rep *conditionReport) {
	for _, msg := range set.Unsupported {
		rep.addUnsupported(msg)
	}
}

func principalSetMatches(set *PrincipalSet, req Request) bool {
	if set.Anonymous {
		return true
	}
	arn := strings.TrimSpace(req.PrincipalARN)
	account := strings.TrimSpace(req.PrincipalAccount)
	for _, p := range set.AWS {
		raw := p.String()
		if arn != "" && p.Match(arn) {
			return true
		}
		if account == "" {
			continue
		}
		// A bare account ID or the account root ARN both mean "any principal
		// in that account", which within one account is the caller.
		if raw == account || raw == fmt.Sprintf("arn:aws:iam::%s:root", account) {
			return true
		}
	}
	// Service principals never match an IAM user or assumed-role caller.
	return false
}

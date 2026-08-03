// Package iampolicy implements the AWS IAM policy grammar and AWS's policy
// evaluation algorithm (explicit deny wins, then allow, otherwise implicit
// deny).
//
// It is deliberately shared: the IAM service's SimulatePrincipalPolicy /
// SimulateCustomPolicy operations and the opt-in request-time enforcement
// middleware evaluate policies through the same code, so what the simulator
// reports is what enforcement would do.
//
// Two properties matter more than breadth of coverage:
//
//   - Constructs the evaluator does not understand are reported in
//     [Result.Unsupported] rather than being silently resolved to an allow or a
//     deny. Callers decide what to do with that — the simulator surfaces AWS's
//     PolicyEvaluation error, enforcement fails closed.
//   - Documents are parsed and their wildcards compiled once, by [Compile].
//     Evaluation itself allocates nothing per statement, so an enforced request
//     path can cache compiled statements and re-evaluate them cheaply.
package iampolicy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Source types used in MatchedStatements, matching the vocabulary AWS's
// SimulatePrincipalPolicy uses for SourcePolicyType.
const (
	SourceTypeIAMPolicy      = "IAM Policy"
	SourceTypeResourcePolicy = "Resource Policy"
	SourceTypeUser           = "User"
	SourceTypeGroup          = "Group"
	SourceTypeRole           = "Role"
	SourceTypeInputPolicy    = "InputPolicy"
)

// SourceRef identifies the policy a statement came from. It maps onto AWS's
// MatchedStatements members (SourcePolicyId / SourcePolicyType).
type SourceRef struct {
	ID   string
	Type string
}

// SourcedDocument is a raw policy document together with its provenance.
type SourcedDocument struct {
	Source   SourceRef
	Document string
}

// Statement is a compiled policy statement. Wildcard patterns are compiled by
// [Compile]; a zero-valued Statement built by hand will not match anything.
type Statement struct {
	Sid    string
	Effect string
	Source SourceRef
	// Index is the statement's 0-based position within its source document.
	Index int

	Action      []Pattern
	NotAction   []Pattern
	Resource    []Pattern
	NotResource []Pattern

	// Principal / NotPrincipal apply to resource-based policies only.
	Principal    *PrincipalSet
	NotPrincipal *PrincipalSet

	// Condition maps operator → condition key → values.
	Condition map[string]map[string][]string
}

// PrincipalSet is the parsed Principal block of a resource-based policy
// statement.
type PrincipalSet struct {
	// Anonymous is true for "Principal": "*" or {"AWS": "*"} — every principal.
	Anonymous bool
	// AWS holds the arn:aws:iam:: principals and bare account IDs.
	AWS []Pattern
	// Service holds service principals (e.g. lambda.amazonaws.com).
	Service []string
	// Unsupported names principal types this evaluator cannot match
	// (Federated, CanonicalUser), reported rather than ignored.
	Unsupported []string
}

// wireStatement is the on-the-wire shape of one statement.
type wireStatement struct {
	Sid          string                     `json:"Sid"`
	Effect       string                     `json:"Effect"`
	Action       json.RawMessage            `json:"Action"`
	NotAction    json.RawMessage            `json:"NotAction"`
	Resource     json.RawMessage            `json:"Resource"`
	NotResource  json.RawMessage            `json:"NotResource"`
	Principal    json.RawMessage            `json:"Principal"`
	NotPrincipal json.RawMessage            `json:"NotPrincipal"`
	Condition    map[string]json.RawMessage `json:"Condition"`
}

type wireDocument struct {
	Version   string          `json:"Version"`
	Statement json.RawMessage `json:"Statement"`
}

// Compile parses every document and returns the statements they contain, with
// wildcard patterns pre-compiled. A malformed document or a statement AWS
// itself would reject is an error naming the offending source — never a
// silently dropped policy, which would turn a deny into an allow.
func Compile(docs []SourcedDocument) ([]Statement, error) {
	out := make([]Statement, 0, len(docs))
	for _, doc := range docs {
		stmts, err := ParseDocument(doc.Document, doc.Source)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
	}
	return out, nil
}

// ParseDocument parses a single policy document into compiled statements.
func ParseDocument(raw string, src SourceRef) ([]Statement, error) {
	var wd wireDocument
	if err := json.Unmarshal([]byte(raw), &wd); err != nil {
		return nil, fmt.Errorf("policy %s: document is not valid JSON: %w", sourceName(src), err)
	}
	if len(wd.Statement) == 0 {
		return nil, fmt.Errorf("policy %s: document has no Statement element", sourceName(src))
	}

	var wires []wireStatement
	if err := json.Unmarshal(wd.Statement, &wires); err != nil {
		var single wireStatement
		if err2 := json.Unmarshal(wd.Statement, &single); err2 != nil {
			return nil, fmt.Errorf("policy %s: Statement is neither a statement nor a list of statements: %w", sourceName(src), err)
		}
		wires = []wireStatement{single}
	}

	out := make([]Statement, 0, len(wires))
	for i, ws := range wires {
		stmt, err := compileStatement(ws, src, i)
		if err != nil {
			return nil, err
		}
		out = append(out, stmt)
	}
	return out, nil
}

func compileStatement(ws wireStatement, src SourceRef, index int) (Statement, error) {
	where := fmt.Sprintf("policy %s statement %d", sourceName(src), index+1)

	effect := strings.TrimSpace(ws.Effect)
	if !strings.EqualFold(effect, "Allow") && !strings.EqualFold(effect, "Deny") {
		return Statement{}, fmt.Errorf("%s: Effect must be Allow or Deny, got %q", where, ws.Effect)
	}

	actions, hasAction, err := parsePatterns(ws.Action, where, "Action")
	if err != nil {
		return Statement{}, err
	}
	notActions, hasNotAction, err := parsePatterns(ws.NotAction, where, "NotAction")
	if err != nil {
		return Statement{}, err
	}
	resources, hasResource, err := parsePatterns(ws.Resource, where, "Resource")
	if err != nil {
		return Statement{}, err
	}
	notResources, hasNotResource, err := parsePatterns(ws.NotResource, where, "NotResource")
	if err != nil {
		return Statement{}, err
	}

	switch {
	case hasAction && hasNotAction:
		return Statement{}, fmt.Errorf("%s: has both Action and NotAction", where)
	case !hasAction && !hasNotAction:
		return Statement{}, fmt.Errorf("%s: has neither Action nor NotAction", where)
	case hasResource && hasNotResource:
		return Statement{}, fmt.Errorf("%s: has both Resource and NotResource", where)
	}

	principal, err := parsePrincipal(ws.Principal, where, "Principal")
	if err != nil {
		return Statement{}, err
	}
	notPrincipal, err := parsePrincipal(ws.NotPrincipal, where, "NotPrincipal")
	if err != nil {
		return Statement{}, err
	}

	cond, err := parseConditionBlock(ws.Condition, where)
	if err != nil {
		return Statement{}, err
	}

	return Statement{
		Sid:          strings.TrimSpace(ws.Sid),
		Effect:       canonicalEffect(effect),
		Source:       src,
		Index:        index,
		Action:       actions,
		NotAction:    notActions,
		Resource:     resources,
		NotResource:  notResources,
		Principal:    principal,
		NotPrincipal: notPrincipal,
		Condition:    cond,
	}, nil
}

func canonicalEffect(effect string) string {
	if strings.EqualFold(effect, "Deny") {
		return "Deny"
	}
	return "Allow"
}

// parsePatterns decodes a string-or-string-list element into compiled
// patterns. The second return value reports whether the element was present.
func parsePatterns(raw json.RawMessage, where, field string) ([]Pattern, bool, error) {
	values, present, err := parseStringOrStringArray(raw)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %s must be a string or a list of strings: %w", where, field, err)
	}
	if !present {
		return nil, false, nil
	}
	out := make([]Pattern, 0, len(values))
	for _, v := range values {
		out = append(out, NewPattern(v))
	}
	return out, true, nil
}

// parseStringOrStringArray decodes the "string or list of strings" shape used
// throughout the policy grammar.
func parseStringOrStringArray(raw json.RawMessage) ([]string, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, true, nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false, err
	}
	return arr, true, nil
}

// parsePrincipal decodes a Principal / NotPrincipal block. "*" and
// {"AWS": "*"} are anonymous; principal types this evaluator cannot match are
// recorded on the set rather than dropped.
func parsePrincipal(raw json.RawMessage, where, field string) (*PrincipalSet, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var wildcard string
	if err := json.Unmarshal(raw, &wildcard); err == nil {
		if strings.TrimSpace(wildcard) != "*" {
			return nil, fmt.Errorf("%s: %s as a bare string must be \"*\", got %q", where, field, wildcard)
		}
		return &PrincipalSet{Anonymous: true}, nil
	}

	var block map[string]json.RawMessage
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, fmt.Errorf("%s: %s must be \"*\" or an object: %w", where, field, err)
	}

	set := &PrincipalSet{}
	for key, valueRaw := range block {
		values, _, err := parseStringOrStringArray(valueRaw)
		if err != nil {
			return nil, fmt.Errorf("%s: %s.%s must be a string or a list of strings: %w", where, field, key, err)
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "aws":
			for _, v := range values {
				if strings.TrimSpace(v) == "*" {
					set.Anonymous = true
					continue
				}
				set.AWS = append(set.AWS, NewPattern(v))
			}
		case "service":
			set.Service = append(set.Service, values...)
		default:
			set.Unsupported = append(set.Unsupported,
				fmt.Sprintf("%s: %s type %q is not evaluated", where, field, key))
		}
	}
	return set, nil
}

// parseConditionBlock converts the wire-format Condition map (operator →
// {key: string|[]string}) into the canonical form used by evaluateConditions.
func parseConditionBlock(raw map[string]json.RawMessage, where string) (map[string]map[string][]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]map[string][]string, len(raw))
	for op, keyRaw := range raw {
		var pairs map[string]json.RawMessage
		if err := json.Unmarshal(keyRaw, &pairs); err != nil {
			return nil, fmt.Errorf("%s: Condition %q must be an object of condition keys: %w", where, op, err)
		}
		keyMap := make(map[string][]string, len(pairs))
		for k, vRaw := range pairs {
			vals, present, err := parseStringOrStringArray(vRaw)
			if err != nil {
				// Numbers and booleans are legal condition values on the wire.
				var scalars []any
				if err2 := json.Unmarshal(vRaw, &scalars); err2 == nil {
					for _, v := range scalars {
						vals = append(vals, fmt.Sprintf("%v", v))
					}
					present = len(vals) > 0
				} else {
					var scalar any
					if err3 := json.Unmarshal(vRaw, &scalar); err3 != nil {
						return nil, fmt.Errorf("%s: Condition %q key %q has an unreadable value: %w", where, op, k, err)
					}
					vals = []string{fmt.Sprintf("%v", scalar)}
					present = true
				}
			}
			if !present {
				continue
			}
			keyMap[k] = vals
		}
		if len(keyMap) > 0 {
			out[op] = keyMap
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func sourceName(src SourceRef) string {
	if strings.TrimSpace(src.ID) == "" {
		return "(unnamed)"
	}
	return src.ID
}

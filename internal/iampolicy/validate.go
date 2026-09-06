package iampolicy

// validate.go — the structural check AWS applies to a policy document at the
// IAM API boundary, before the document is stored (#1717).
//
// It is deliberately the same parser the evaluator uses. A second,
// independently written policy reader would be free to disagree with the one
// that decides allow-or-deny, and the first symptom of that disagreement would
// be a document CreatePolicy accepted and enforcement then refused to read.
// [ValidateDocument] is therefore [ParseDocumentWithOptions] with the options
// AWS's own grammar requires, plus the one rule the evaluator has no reason to
// apply to a document already in the store: Effect is case sensitive.
//
// Scope is deliberate. This is the cheap, high-value tier — a document that
// parses, has statements, and has statements shaped like statements. It is
// **not** full policy-grammar validation, and what it does not check is
// recorded in the IAM capability notes and in docs/services/iam/limitations.md
// rather than half-implemented here.

import (
	"encoding/json"
	"errors"
	"fmt"
)

// boundaryOptions are the grammar rules AWS enforces on any policy document
// submitted to IAM, from the IAM User Guide's policy-elements reference:
//
//   - Statement "is required" and "can contain a single statement or an array
//     of individual statements" (reference_policies_elements_statement.html) —
//     an empty array satisfies neither.
//   - "Statements must include either an Action or NotAction element"
//     (reference_policies_elements_action.html), and the two are mutually
//     exclusive, as are Resource/NotResource and Principal/NotPrincipal
//     (reference_policies_elements.html).
//   - "Valid values for Effect are Allow and Deny" and "The Effect value is
//     case sensitive" (reference_policies_elements_effect.html).
//   - Version is optional, and when present is one of exactly two values:
//     `<version_block> = "Version" : ("2008-10-17" | "2012-10-17")`
//     (reference_policies_grammar.html).
//
// RequirePrincipal stays off: an identity policy has no Principal, and a trust
// policy's is checked no further than "is it a shape the grammar allows".
var boundaryOptions = ParseOptions{
	RequireStatements: true,
	StrictEffect:      true,
	AllowedVersions:   []string{"2008-10-17", "2012-10-17"},
}

// ValidateDocument reports whether raw is a policy document AWS's IAM API
// would accept. A nil return means the document parses and every statement in
// it is well formed; a non-nil error names the specific defect, which is what
// AWS's own MalformedPolicyDocument promises ("The error message describes the
// specific error" — IAM API Reference, API_CreatePolicy.html Errors).
//
// Callers turn the error into their own API fault; nothing here knows about
// HTTP or the Query protocol.
func ValidateDocument(raw string) error {
	// The document must be a JSON object. Unmarshalling straight into the
	// wire struct would report a JSON array or a bare string as a type error
	// mentioning an internal Go type, so the shape is established first and
	// the parser is left to speak about policy elements.
	var probe any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return fmt.Errorf("the policy document is not valid JSON: %w", err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return errors.New("the policy document must be a JSON object")
	}
	if _, err := ParseDocumentWithOptions(raw, SourceRef{}, boundaryOptions); err != nil {
		return err
	}
	return nil
}

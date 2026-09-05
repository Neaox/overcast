package serviceutil

// policy.go — the public-access check shared by every service that stores an
// IAM-shaped resource policy and refuses to make a resource public by
// accident. AWS's own definition, reused verbatim by Lambda's PutResourcePolicy
// and Secrets Manager's BlockPublicPolicy: an Allow statement whose Principal
// names everyone — the bare "*", {"AWS": "*"}, or either spelling inside a
// list — and which a Condition does nothing to narrow.
//
// Effect, Principal and Condition are taken as decoded JSON (`any`) rather
// than a shared statement struct so each service's own statement type —
// Lambda's permissionStatement, Secrets Manager's policyStatement — can pass
// its fields straight through without an intermediate conversion.

// StatementGrantsPublicAccess reports whether one policy statement allows
// every principal without narrowing the grant by a condition.
func StatementGrantsPublicAccess(effect string, principal, condition any) bool {
	if effect != "Allow" || !PrincipalIsWildcard(principal) {
		return false
	}
	// AWS's wording is "contains no condition keys", so an omitted Condition
	// and an empty one narrow the statement equally little.
	if conditions, ok := condition.(map[string]any); ok {
		return len(conditions) == 0
	}
	return condition == nil
}

// PrincipalIsWildcard recognises the spellings of "everyone" a policy
// document may use for a Principal: the bare "*", {"AWS": "*"}, and a list
// containing either.
func PrincipalIsWildcard(principal any) bool {
	switch p := principal.(type) {
	case string:
		return p == "*"
	case []any:
		for _, item := range p {
			if PrincipalIsWildcard(item) {
				return true
			}
		}
	case map[string]any:
		for _, value := range p {
			if PrincipalIsWildcard(value) {
				return true
			}
		}
	}
	return false
}

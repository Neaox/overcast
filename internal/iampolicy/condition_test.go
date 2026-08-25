package iampolicy

import "testing"

// evaluateConditionsLegacy adapts evaluateConditions to the (met, unknown)
// shape these tests were written against, so the behaviour they pin is
// unchanged by the move out of internal/middleware.
func evaluateConditionsLegacy(cond map[string]map[string][]string, ctx map[string]string) (bool, bool) {
	var rep conditionReport
	met := evaluateConditions(cond, ctx, &rep)
	return met, len(rep.unsupported) > 0
}

// ─── Condition evaluation unit tests ─────────────────────────────────────────

func TestEvaluateConditions_noCondition_alwaysMet(t *testing.T) {
	met, unknown := evaluateConditionsLegacy(nil, map[string]string{})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEquals": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEquals_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEquals": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEquals_multiValueOR(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEquals": {"aws:requestedregion": {"us-east-1", "eu-west-1"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringNotEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringNotEquals": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringLike_wildcardMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringLike": {"aws:requestedregion": {"us-*"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringLike_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringLike": {"aws:requestedregion": {"us-*"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_arnLike_wildcardMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"ArnLike": {"aws:requestedregion": {"arn:aws:s3:::my-bucket*"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "arn:aws:s3:::my-bucket-prod"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_boolEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"Bool": {"aws:requestedregion": {"true"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "true"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_ipAddress_exactMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"IpAddress": {"aws:sourceip": {"192.168.1.1"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:sourceip": "192.168.1.1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_ipAddress_cidrMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"IpAddress": {"aws:sourceip": {"10.0.0.0/8"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:sourceip": "10.1.2.3"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_ipAddress_cidrNoMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"IpAddress": {"aws:sourceip": {"10.0.0.0/8"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:sourceip": "192.168.1.1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_unknownOperator_signalsUnknown(t *testing.T) {
	cond := map[string]map[string][]string{
		"SomeUnknownOp": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if met || !unknown {
		t.Fatalf("expected (false, true), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_missingContextKey_notMet(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEquals": {"aws:principalarn": {"arn:aws:iam::123:user/admin"}},
	}
	// aws:principalarn is not in the context
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_dateLessThan_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"DateLessThan": {"aws:currenttime": {"2026-04-24T00:00:00Z"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:currenttime": "2026-04-23T00:00:00Z"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_dateGreaterThan_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"DateGreaterThan": {"aws:currenttime": {"2026-04-24T00:00:00Z"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:currenttime": "2026-04-23T00:00:00Z"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_dateNotEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"DateNotEquals": {"aws:currenttime": {"2026-04-24T00:00:00Z", "2026-04-25T00:00:00Z"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:currenttime": "2026-04-23T00:00:00Z"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_null_true_matchesMissingKey(t *testing.T) {
	cond := map[string]map[string][]string{
		"Null": {"aws:principalarn": {"true"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_null_false_matchesPresentKey(t *testing.T) {
	cond := map[string]map[string][]string{
		"Null": {"aws:principalarn": {"false"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:principalarn": "arn:aws:iam::123456789012:user/test"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_null_false_failsMissingKey(t *testing.T) {
	cond := map[string]map[string][]string{
		"Null": {"aws:principalarn": {"false"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEqualsIfExists_missingKey_met(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEqualsIfExists": {"aws:principaltag/team": {"platform"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEqualsIfExists_presentKeyMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEqualsIfExists": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_stringEqualsIfExists_presentKeyNoMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringEqualsIfExists": {"aws:requestedregion": {"us-east-1"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "eu-west-1"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

// ─── Numeric condition operator unit tests ────────────────────────────────────

func TestEvaluateConditions_numericEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericEquals": {"aws:requestedcontentlength": {"100"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedcontentlength": "100"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericEquals_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericEquals": {"aws:requestedcontentlength": {"100"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedcontentlength": "99"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericLessThan_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericLessThan": {"aws:requestedcontentlength": {"1000"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedcontentlength": "50"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericLessThan_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericLessThan": {"aws:requestedcontentlength": {"1000"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedcontentlength": "5000"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericGreaterThan_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericGreaterThan": {"aws:requestedcontentlength": {"10"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedcontentlength": "42"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericNotEquals_match(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericNotEquals": {"aws:requestedcontentlength": {"0"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedcontentlength": "512"})
	if !met || unknown {
		t.Fatalf("expected (true, false), got (%v, %v)", met, unknown)
	}
}

func TestEvaluateConditions_numericNotEquals_noMatch(t *testing.T) {
	cond := map[string]map[string][]string{
		"NumericNotEquals": {"aws:requestedcontentlength": {"512"}},
	}
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedcontentlength": "512"})
	if met || unknown {
		t.Fatalf("expected (false, false), got (%v, %v)", met, unknown)
	}
}

// ─── Negated operator + missing context key tests (#1475) ────────────────────
//
// AWS's condition-operator reference states this as the general rule (not a
// per-operator carve-out): "If the key that you specify in a policy
// condition is not present in the request context, the values do not match
// and the condition is false. If the policy condition requires that the key
// is not matched, such as StringNotLike or ArnNotLike, and the right key is
// not present, the condition is true. This logic applies to all condition
// operators except [...IfExists] and [Null check]."
// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_policies_elements_condition_operators.html
//
// The next test reproduces the defect directly: a Deny+StringNotEquals guard
// against an absent context key must deny. Before the fix in
// internal/iampolicy/condition.go, evaluateConditions treated every operator
// alike on a missing key ("condition not met"), so this Deny's condition
// never matched and the request was allowed.

func TestEvaluateConditions_denyStringNotEquals_missingKey_conditionMet(t *testing.T) {
	cond := map[string]map[string][]string{
		"StringNotEquals": {"s3:x-amz-bucket-namespace": {"account-regional"}},
	}
	// The bucket-namespace key is absent from the request context entirely —
	// the request carried no X-Amz-Bucket-Namespace header.
	met, unknown := evaluateConditionsLegacy(cond, map[string]string{"aws:requestedregion": "us-east-1"})
	if !met || unknown {
		t.Fatalf("expected (true, false) — AWS denies a missing key against StringNotEquals — got (%v, %v)", met, unknown)
	}
}

// negatedOperatorCase drives the present-and-equal / present-and-different /
// absent table for one negated operator per class (string, string
// ignore-case, string wildcard, ARN exact, ARN wildcard, numeric, date, IP).
type negatedOperatorCase struct {
	class      string
	operator   string
	contextKey string
	policyVal  string
	equalVal   string // matches policyVal under the operator's comparison
	diffVal    string // does not match policyVal
}

func TestEvaluateConditions_negatedOperators_table(t *testing.T) {
	cases := []negatedOperatorCase{
		{"string", "StringNotEquals", "aws:requestedregion", "us-east-1", "us-east-1", "eu-west-1"},
		{"stringIgnoreCase", "StringNotEqualsIgnoreCase", "aws:requestedregion", "US-EAST-1", "us-east-1", "eu-west-1"},
		{"stringLike", "StringNotLike", "aws:requestedregion", "us-*", "us-east-1", "eu-west-1"},
		{"arn", "ArnNotEquals", "aws:sourcearn", "arn:aws:s3:::my-bucket", "arn:aws:s3:::my-bucket", "arn:aws:s3:::other-bucket"},
		{"arnLike", "ArnNotLike", "aws:sourcearn", "arn:aws:s3:::my-bucket*", "arn:aws:s3:::my-bucket-1", "arn:aws:s3:::other-bucket"},
		{"numeric", "NumericNotEquals", "aws:requestedcontentlength", "100", "100", "200"},
		{"date", "DateNotEquals", "aws:currenttime", "2026-04-24T00:00:00Z", "2026-04-24T00:00:00Z", "2026-04-25T00:00:00Z"},
		{"ip", "NotIpAddress", "aws:sourceip", "10.0.0.0/8", "10.1.2.3", "192.168.1.1"},
	}

	for _, tc := range cases {
		t.Run(tc.class+"/present_matchesSet_conditionNotMet", func(t *testing.T) {
			cond := map[string]map[string][]string{tc.operator: {tc.contextKey: {tc.policyVal}}}
			met, unknown := evaluateConditionsLegacy(cond, map[string]string{tc.contextKey: tc.equalVal})
			if met || unknown {
				t.Fatalf("%s: expected (false, false) when the value matches the negated set, got (%v, %v)", tc.operator, met, unknown)
			}
		})
		t.Run(tc.class+"/present_differsFromSet_conditionMet", func(t *testing.T) {
			cond := map[string]map[string][]string{tc.operator: {tc.contextKey: {tc.policyVal}}}
			met, unknown := evaluateConditionsLegacy(cond, map[string]string{tc.contextKey: tc.diffVal})
			if !met || unknown {
				t.Fatalf("%s: expected (true, false) when the value differs from the negated set, got (%v, %v)", tc.operator, met, unknown)
			}
		})
		t.Run(tc.class+"/absentKey_conditionMet", func(t *testing.T) {
			cond := map[string]map[string][]string{tc.operator: {tc.contextKey: {tc.policyVal}}}
			met, unknown := evaluateConditionsLegacy(cond, map[string]string{"unrelated:key": "value"})
			if !met || unknown {
				t.Fatalf("%s: expected (true, false) when the context key is absent (AWS: negated operators match on a missing key), got (%v, %v)", tc.operator, met, unknown)
			}
		})
	}
}

// ─── Policy variable expansion unit tests ─────────────────────────────────────

func TestExpandPolicyVariables_substitutesUsername(t *testing.T) {
	reqCtx := map[string]string{"aws:username": "alice"}
	got := expandPolicyVariables("arn:aws:s3:::home/${aws:username}/*", reqCtx)
	want := "arn:aws:s3:::home/alice/*"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExpandPolicyVariables_substitutesUserid(t *testing.T) {
	reqCtx := map[string]string{"aws:userid": "AIDAEXAMPLE"}
	got := expandPolicyVariables("arn:aws:iam::000000000000:user/${aws:userid}", reqCtx)
	want := "arn:aws:iam::000000000000:user/AIDAEXAMPLE"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExpandPolicyVariables_unknownVariableLeftLiteral(t *testing.T) {
	reqCtx := map[string]string{}
	got := expandPolicyVariables("arn:aws:s3:::home/${aws:unknown}/*", reqCtx)
	want := "arn:aws:s3:::home/${aws:unknown}/*"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExpandPolicyVariables_noVariables(t *testing.T) {
	reqCtx := map[string]string{"aws:username": "alice"}
	got := expandPolicyVariables("arn:aws:s3:::my-bucket/*", reqCtx)
	want := "arn:aws:s3:::my-bucket/*"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExpandPolicyVariables_caseInsensitiveVariable(t *testing.T) {
	reqCtx := map[string]string{"aws:username": "bob"}
	// Variable name in different case — should still resolve.
	got := expandPolicyVariables("arn:aws:s3:::home/${AWS:Username}/*", reqCtx)
	want := "arn:aws:s3:::home/bob/*"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestStatementMatchesResource_expandsUsernameVariable(t *testing.T) {
	stmt := Statement{
		Resource: []Pattern{NewPattern("arn:aws:s3:::home/${aws:username}/*")},
	}
	reqCtx := map[string]string{"aws:username": "carol"}
	if !stmt.matchesResource("arn:aws:s3:::home/carol/file.txt", reqCtx) {
		t.Fatal("expected resource to match after variable expansion")
	}
}

func TestStatementMatchesResource_expandsUsername_noMatch(t *testing.T) {
	stmt := Statement{
		Resource: []Pattern{NewPattern("arn:aws:s3:::home/${aws:username}/*")},
	}
	reqCtx := map[string]string{"aws:username": "carol"}
	if stmt.matchesResource("arn:aws:s3:::home/mallory/file.txt", reqCtx) {
		t.Fatal("expected resource to NOT match after variable expansion")
	}
}

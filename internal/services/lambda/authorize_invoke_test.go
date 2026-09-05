package lambda

import (
	"encoding/json"
	"testing"
)

// statementsFromJSON decodes a policy document the way the store hands one
// back, so the evaluator is exercised on the generic JSON shapes rather than on
// hand-built Go maps a real invocation never sees.
func statementsFromJSON(t *testing.T, document string) []permissionStatement {
	t.Helper()
	var parsed policyDocument
	if err := json.Unmarshal([]byte(document), &parsed); err != nil {
		t.Fatalf("decoding the policy document: %v", err)
	}
	return parsed.Statement
}

const (
	testFunctionARN = "arn:aws:lambda:us-east-1:000000000000:function:handler"
	testBucketARN   = "arn:aws:s3:::pictures"

	// The service principals each caller invokes as, spelled the way its own
	// package does — this decision has no opinion about which services exist.
	principalS3          = "s3.amazonaws.com"
	principalEventBridge = "events.amazonaws.com"
)

func TestInvokePolicyVerdict_s3StatementAllowsItsOwnBucket(t *testing.T) {
	// Given: the statement `add-permission` writes for an S3 notification
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Sid":"s3invoke","Effect":"Allow",
	  "Principal":{"Service":"s3.amazonaws.com"},
	  "Action":"lambda:InvokeFunction",
	  "Resource":"`+testFunctionARN+`",
	  "Condition":{"ArnLike":{"AWS:SourceArn":"`+testBucketARN+`"},
	               "StringEquals":{"AWS:SourceAccount":"000000000000"}}
	}]}`)

	// When: that bucket, in that account, invokes the unqualified function
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, "000000000000")

	// Then: the invocation is allowed
	if got != verdictAllow {
		t.Fatalf("verdict = %v, want verdictAllow", got)
	}
}

func TestInvokePolicyVerdict_mismatchedSourceARNDenies(t *testing.T) {
	// Given: a statement scoped to one bucket
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Sid":"s3invoke","Effect":"Allow",
	  "Principal":{"Service":"s3.amazonaws.com"},
	  "Action":"lambda:InvokeFunction",
	  "Resource":"`+testFunctionARN+`",
	  "Condition":{"ArnLike":{"AWS:SourceArn":"`+testBucketARN+`"}}
	}]}`)

	// When: a different bucket invokes
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, "arn:aws:s3:::documents", "")

	// Then: nothing matches, so the default deny stands
	if got != verdictImplicitDeny {
		t.Fatalf("verdict = %v, want verdictImplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_mismatchedSourceAccountDenies(t *testing.T) {
	// Given: a statement requiring the bucket to belong to this account
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Sid":"s3invoke","Effect":"Allow",
	  "Principal":{"Service":"s3.amazonaws.com"},
	  "Action":"lambda:InvokeFunction",
	  "Resource":"`+testFunctionARN+`",
	  "Condition":{"StringEquals":{"AWS:SourceAccount":"000000000000"}}
	}]}`)

	// When: the same bucket name in somebody else's account invokes
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, "111122223333")

	// Then: the condition fails
	if got != verdictImplicitDeny {
		t.Fatalf("verdict = %v, want verdictImplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_wildcardSourceARNMatches(t *testing.T) {
	// Given: a statement admitting every bucket whose name starts with "logs-"
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},
	  "Action":"lambda:InvokeFunction","Resource":"`+testFunctionARN+`",
	  "Condition":{"ArnLike":{"AWS:SourceArn":"arn:aws:s3:::logs-*"}}
	}]}`)

	// When: one of them invokes
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, "arn:aws:s3:::logs-2026", "")

	// Then: the wildcard matches
	if got != verdictAllow {
		t.Fatalf("verdict = %v, want verdictAllow", got)
	}
}

func TestInvokePolicyVerdict_wrongPrincipalDenies(t *testing.T) {
	// Given: a statement granting SNS
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow","Principal":{"Service":"sns.amazonaws.com"},
	  "Action":"lambda:InvokeFunction","Resource":"`+testFunctionARN+`"
	}]}`)

	// When: S3 invokes
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, "")

	// Then: a grant to one service is not a grant to another
	if got != verdictImplicitDeny {
		t.Fatalf("verdict = %v, want verdictImplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_awsPrincipalIsNotAServicePrincipal(t *testing.T) {
	// Given: a statement granting an account rather than a service
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow","Principal":{"AWS":"*"},
	  "Action":"lambda:InvokeFunction","Resource":"`+testFunctionARN+`",
	  "Condition":{"StringEquals":{"AWS:SourceAccount":"000000000000"}}
	}]}`)

	// When: S3 invokes
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, "000000000000")

	// Then: {"AWS": …} names accounts and IAM principals, not services
	if got != verdictImplicitDeny {
		t.Fatalf("verdict = %v, want verdictImplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_explicitDenyBeatsAllow(t *testing.T) {
	// Given: a broad allow and a narrower deny, in that order
	statements := statementsFromJSON(t, `{"Statement":[
	  {"Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},
	   "Action":"lambda:*","Resource":"`+testFunctionARN+`"},
	  {"Effect":"Deny","Principal":{"Service":"s3.amazonaws.com"},
	   "Action":"lambda:InvokeFunction","Resource":"`+testFunctionARN+`",
	   "Condition":{"ArnLike":{"AWS:SourceArn":"`+testBucketARN+`"}}}
	]}`)

	// When: the denied bucket invokes
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, "")

	// Then: the explicit deny wins wherever it sits in the document
	if got != verdictExplicitDeny {
		t.Fatalf("verdict = %v, want verdictExplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_qualifiedResourceDoesNotAuthoriseLatest(t *testing.T) {
	// Given: a statement scoped to the "live" alias
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},
	  "Action":"lambda:InvokeFunction","Resource":"`+testFunctionARN+`:live"
	}]}`)

	// When: the unqualified function is invoked
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, "")

	// Then: Lambda compares the resource element against the qualifier too
	if got != verdictImplicitDeny {
		t.Fatalf("verdict = %v, want verdictImplicitDeny", got)
	}

	// And: the alias itself is allowed
	if got := invokePolicyVerdict(statements, principalS3, testFunctionARN+":live", testBucketARN, ""); got != verdictAllow {
		t.Fatalf("alias verdict = %v, want verdictAllow", got)
	}
}

func TestInvokePolicyVerdict_unqualifiedResourceDoesNotAuthoriseAnAlias(t *testing.T) {
	// Given: a statement on the unqualified function
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},
	  "Action":"lambda:InvokeFunction","Resource":"`+testFunctionARN+`"
	}]}`)

	// When: an alias is invoked
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN+":live", testBucketARN, "")

	// Then: an unqualified grant admits only an unqualified invoke
	if got != verdictImplicitDeny {
		t.Fatalf("verdict = %v, want verdictImplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_qualifierWildcardMatchesAnyQualifierButNotLatest(t *testing.T) {
	// Given: a statement whose resource ends ":*"
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},
	  "Action":"lambda:InvokeFunction","Resource":"`+testFunctionARN+`:*"
	}]}`)

	// When / Then: any qualified ARN is admitted
	if got := invokePolicyVerdict(statements, principalS3, testFunctionARN+":7", testBucketARN, ""); got != verdictAllow {
		t.Fatalf("qualified verdict = %v, want verdictAllow", got)
	}
	// And: the unqualified one is not
	if got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, ""); got != verdictImplicitDeny {
		t.Fatalf("unqualified verdict = %v, want verdictImplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_unevaluableConditionKeyDenies(t *testing.T) {
	// Given: a statement conditioned on a key no emulated caller supplies
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},
	  "Action":"lambda:InvokeFunction","Resource":"`+testFunctionARN+`",
	  "Condition":{"StringEquals":{"aws:PrincipalOrgID":"o-abc123def4"}}
	}]}`)

	// When: S3 invokes
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, "")

	// Then: an unevaluable condition refuses rather than being ignored
	if got != verdictImplicitDeny {
		t.Fatalf("verdict = %v, want verdictImplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_functionURLStatementDoesNotAuthoriseAService(t *testing.T) {
	// Given: the statement `add-permission --function-url-auth-type NONE` writes
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow","Principal":"*",
	  "Action":"lambda:InvokeFunctionUrl","Resource":"`+testFunctionARN+`",
	  "Condition":{"StringEquals":{"lambda:FunctionUrlAuthType":"NONE"}}
	}]}`)

	// When: S3 invokes
	got := invokePolicyVerdict(statements, principalS3, testFunctionARN, testBucketARN, "")

	// Then: a function-URL grant is not an InvokeFunction grant
	if got != verdictImplicitDeny {
		t.Fatalf("verdict = %v, want verdictImplicitDeny", got)
	}
}

func TestInvokePolicyVerdict_listValuedActionAndPrincipal(t *testing.T) {
	// Given: a PutResourcePolicy document using the full IAM grammar
	statements := statementsFromJSON(t, `{"Statement":[{
	  "Effect":"Allow",
	  "Principal":{"Service":["sns.amazonaws.com","events.amazonaws.com"]},
	  "Action":["lambda:GetFunction","lambda:InvokeFunction"],
	  "Resource":["`+testFunctionARN+`","`+testFunctionARN+`:*"]
	}]}`)

	// When: EventBridge invokes
	got := invokePolicyVerdict(statements, principalEventBridge, testFunctionARN, "arn:aws:events:us-east-1:000000000000:rule/nightly", "")

	// Then: list-valued elements are an OR on each axis
	if got != verdictAllow {
		t.Fatalf("verdict = %v, want verdictAllow", got)
	}
}

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"*", "anything", true},
		{"", "", true},
		{"", "x", false},
		{"a*c", "abc", true},
		{"a*c", "ac", true},
		{"a*c", "abd", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"arn:aws:s3:::logs-*", "arn:aws:s3:::logs-2026", true},
		{"arn:aws:s3:::logs-*", "arn:aws:s3:::pictures", false},
		{"*:function:handler", "arn:aws:lambda:us-east-1:0:function:handler", true},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
	}
	for _, tc := range cases {
		if got := wildcardMatch(tc.pattern, tc.value); got != tc.want {
			t.Errorf("wildcardMatch(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}

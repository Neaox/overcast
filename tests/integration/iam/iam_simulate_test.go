package iam_test

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ─── Response shapes ─────────────────────────────────────────────────────────

type simulatePrincipalResponse struct {
	XMLName xml.Name       `xml:"SimulatePrincipalPolicyResponse"`
	Result  simulateResult `xml:"SimulatePrincipalPolicyResult"`
}

type simulateCustomResponse struct {
	XMLName xml.Name       `xml:"SimulateCustomPolicyResponse"`
	Result  simulateResult `xml:"SimulateCustomPolicyResult"`
}

type simulateResult struct {
	IsTruncated       bool            `xml:"IsTruncated"`
	EvaluationResults []evaluationXML `xml:"EvaluationResults>member"`
}

type evaluationXML struct {
	EvalActionName        string             `xml:"EvalActionName"`
	EvalResourceName      string             `xml:"EvalResourceName"`
	EvalDecision          string             `xml:"EvalDecision"`
	MatchedStatements     []matchedStatement `xml:"MatchedStatements>member"`
	MissingContextValues  []string           `xml:"MissingContextValues>member"`
	ResourceSpecific      []resourceSpecific `xml:"ResourceSpecificResults>member"`
	PermissionsBoundaries []struct {
		AllowedByPermissionsBoundary bool `xml:"AllowedByPermissionsBoundary"`
	} `xml:"PermissionsBoundaryDecisionDetail"`
}

type positionXML struct {
	Line   int `xml:"Line"`
	Column int `xml:"Column"`
}

type matchedStatement struct {
	SourcePolicyId   string       `xml:"SourcePolicyId"`
	SourcePolicyType string       `xml:"SourcePolicyType"`
	StartPosition    *positionXML `xml:"StartPosition"`
	EndPosition      *positionXML `xml:"EndPosition"`
}

type resourceSpecific struct {
	EvalResourceName     string   `xml:"EvalResourceName"`
	EvalResourceDecision string   `xml:"EvalResourceDecision"`
	MissingContextValues []string `xml:"MissingContextValues>member"`
}

// simulate performs a simulation call and decodes the evaluation results.
func simulate(t *testing.T, srv *helpers.TestServer, action string, params url.Values) []evaluationXML {
	t.Helper()
	resp := iamCall(t, srv, action, params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	if action == "SimulateCustomPolicy" {
		var decoded simulateCustomResponse
		helpers.DecodeXML(t, resp, &decoded)
		return decoded.Result.EvaluationResults
	}
	var decoded simulatePrincipalResponse
	helpers.DecodeXML(t, resp, &decoded)
	return decoded.Result.EvaluationResults
}

const allowS3ReadDoc = `{"Version":"2012-10-17","Statement":[{"Sid":"AllowRead","Effect":"Allow",` +
	`"Action":"s3:GetObject","Resource":"arn:aws:s3:::reports/*"}]}`

// ─── Fixture helpers ─────────────────────────────────────────────────────────

// iamOK performs an IAM call and asserts it succeeded.
func iamOK(t *testing.T, srv *helpers.TestServer, action string, params url.Values) {
	t.Helper()
	resp := iamCall(t, srv, action, params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func putUserPolicy(t *testing.T, srv *helpers.TestServer, user, name, doc string) {
	t.Helper()
	iamOK(t, srv, "PutUserPolicy", url.Values{
		"UserName": {user}, "PolicyName": {name}, "PolicyDocument": {doc},
	})
}

func putRolePolicy(t *testing.T, srv *helpers.TestServer, role, name, doc string) {
	t.Helper()
	iamOK(t, srv, "PutRolePolicy", url.Values{
		"RoleName": {role}, "PolicyName": {name}, "PolicyDocument": {doc},
	})
}

func putGroupPolicy(t *testing.T, srv *helpers.TestServer, group, name, doc string) {
	t.Helper()
	iamOK(t, srv, "PutGroupPolicy", url.Values{
		"GroupName": {group}, "PolicyName": {name}, "PolicyDocument": {doc},
	})
}

func attachUserPolicy(t *testing.T, srv *helpers.TestServer, user, policyArn string) {
	t.Helper()
	iamOK(t, srv, "AttachUserPolicy", url.Values{"UserName": {user}, "PolicyArn": {policyArn}})
}

func addUserToGroup(t *testing.T, srv *helpers.TestServer, user, group string) {
	t.Helper()
	iamOK(t, srv, "AddUserToGroup", url.Values{"UserName": {user}, "GroupName": {group}})
}

// ─── SimulatePrincipalPolicy ─────────────────────────────────────────────────

func TestSimulatePrincipalPolicy_userWithoutPolicies_implicitDeny(t *testing.T) {
	// Given: a user with no policies attached
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "no-policy-user")

	// When: an action is simulated
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:user/no-policy-user"},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: AWS's default applies
	if len(results) != 1 {
		t.Fatalf("EvaluationResults = %d, want 1", len(results))
	}
	if results[0].EvalDecision != "implicitDeny" {
		t.Fatalf("EvalDecision = %q, want implicitDeny", results[0].EvalDecision)
	}
	if results[0].EvalActionName != "s3:GetObject" {
		t.Fatalf("EvalActionName = %q, want s3:GetObject", results[0].EvalActionName)
	}
	if results[0].EvalResourceName != "arn:aws:s3:::reports/q1.csv" {
		t.Fatalf("EvalResourceName = %q, want the requested ARN", results[0].EvalResourceName)
	}
}

func TestSimulatePrincipalPolicy_inlinePolicyMatches_allowed(t *testing.T) {
	// Given: a user with an inline policy allowing the action
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "reader")
	putUserPolicy(t, srv, "reader", "read-reports", allowS3ReadDoc)

	// When: the allowed action is simulated
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:user/reader"},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: it is allowed and the matching statement is named
	if results[0].EvalDecision != "allowed" {
		t.Fatalf("EvalDecision = %q, want allowed", results[0].EvalDecision)
	}
	if len(results[0].MatchedStatements) != 1 {
		t.Fatalf("MatchedStatements = %#v, want one", results[0].MatchedStatements)
	}
	if results[0].MatchedStatements[0].SourcePolicyId != "read-reports" {
		t.Fatalf("SourcePolicyId = %q, want read-reports", results[0].MatchedStatements[0].SourcePolicyId)
	}
	if results[0].MatchedStatements[0].SourcePolicyType != "User" {
		t.Fatalf("SourcePolicyType = %q, want User", results[0].MatchedStatements[0].SourcePolicyType)
	}
}

func TestSimulatePrincipalPolicy_actionNotCovered_implicitDeny(t *testing.T) {
	// Given: a user allowed only to read
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "reader-only")
	putUserPolicy(t, srv, "reader-only", "read-reports", allowS3ReadDoc)

	// When: a write is simulated
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:user/reader-only"},
		"ActionNames.member.1":  {"s3:PutObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: nothing matched
	if results[0].EvalDecision != "implicitDeny" {
		t.Fatalf("EvalDecision = %q, want implicitDeny", results[0].EvalDecision)
	}
}

func TestSimulatePrincipalPolicy_explicitDeny_beatsAllow(t *testing.T) {
	// Given: a user allowed everything and denied one action
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "denied-user")
	putUserPolicy(t, srv, "denied-user", "allow-all",
		`{"Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`)
	putUserPolicy(t, srv, "denied-user", "deny-delete",
		`{"Statement":[{"Sid":"NoDelete","Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`)

	// When: the denied action is simulated
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:user/denied-user"},
		"ActionNames.member.1":  {"s3:DeleteObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: the explicit deny wins
	if results[0].EvalDecision != "explicitDeny" {
		t.Fatalf("EvalDecision = %q, want explicitDeny", results[0].EvalDecision)
	}
}

func TestSimulatePrincipalPolicy_attachedManagedPolicy_allowed(t *testing.T) {
	// Given: a user with an attached managed policy allowing s3:*
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "managed-user")
	arn := createPolicy(t, srv, "managed-s3")
	attachUserPolicy(t, srv, "managed-user", arn)

	// When: an s3 action is simulated
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:user/managed-user"},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: the managed policy grants it, and is named as the source
	if results[0].EvalDecision != "allowed" {
		t.Fatalf("EvalDecision = %q, want allowed", results[0].EvalDecision)
	}
	if len(results[0].MatchedStatements) == 0 ||
		results[0].MatchedStatements[0].SourcePolicyType != "IAM Policy" {
		t.Fatalf("MatchedStatements = %#v, want an IAM Policy source", results[0].MatchedStatements)
	}
}

func TestSimulatePrincipalPolicy_groupPolicy_allowed(t *testing.T) {
	// Given: a user whose group carries the allow
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "group-member")
	createGroup(t, srv, "readers")
	addUserToGroup(t, srv, "group-member", "readers")
	putGroupPolicy(t, srv, "readers", "group-read", allowS3ReadDoc)

	// When: the action is simulated for the user
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:user/group-member"},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: group policies count towards the decision
	if results[0].EvalDecision != "allowed" {
		t.Fatalf("EvalDecision = %q, want allowed", results[0].EvalDecision)
	}
	if results[0].MatchedStatements[0].SourcePolicyType != "Group" {
		t.Fatalf("SourcePolicyType = %q, want Group", results[0].MatchedStatements[0].SourcePolicyType)
	}
}

func TestSimulatePrincipalPolicy_roleSource_allowed(t *testing.T) {
	// Given: a role with an inline policy
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "task-role")
	putRolePolicy(t, srv, "task-role", "role-read", allowS3ReadDoc)

	// When: the role is simulated
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:role/task-role"},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: the role's policies are evaluated
	if results[0].EvalDecision != "allowed" {
		t.Fatalf("EvalDecision = %q, want allowed", results[0].EvalDecision)
	}
}

func TestSimulatePrincipalPolicy_unknownPrincipal_noSuchEntity(t *testing.T) {
	// Given: no such user
	srv := helpers.NewTestServer(t)

	// When: it is simulated
	resp := iamCall(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":      {"arn:aws:iam::000000000000:user/ghost"},
		"ActionNames.member.1": {"s3:GetObject"},
	})
	defer resp.Body.Close()

	// Then: AWS's NoSuchEntity comes back rather than a fabricated decision
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, resp, "NoSuchEntity")
}

func TestSimulatePrincipalPolicy_missingActionNames_missingParameter(t *testing.T) {
	// Given: a user
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "param-user")

	// When: ActionNames is omitted
	resp := iamCall(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn": {"arn:aws:iam::000000000000:user/param-user"},
	})
	defer resp.Body.Close()

	// Then: the required parameter is reported
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestSimulatePrincipalPolicy_multipleResources_perResourceResults(t *testing.T) {
	// Given: a user allowed on one bucket only
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "multi-resource-user")
	putUserPolicy(t, srv, "multi-resource-user", "read-reports", allowS3ReadDoc)

	// When: two resources are simulated in one call
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:user/multi-resource-user"},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
		"ResourceArns.member.2": {"arn:aws:s3:::secrets/keys.txt"},
	})

	// Then: each resource gets its own decision
	if len(results[0].ResourceSpecific) != 2 {
		t.Fatalf("ResourceSpecificResults = %#v, want 2", results[0].ResourceSpecific)
	}
	if results[0].ResourceSpecific[0].EvalResourceDecision != "allowed" {
		t.Fatalf("first resource decision = %q, want allowed", results[0].ResourceSpecific[0].EvalResourceDecision)
	}
	if results[0].ResourceSpecific[1].EvalResourceDecision != "implicitDeny" {
		t.Fatalf("second resource decision = %q, want implicitDeny", results[0].ResourceSpecific[1].EvalResourceDecision)
	}
}

func TestSimulatePrincipalPolicy_resourcePolicyGrants_allowed(t *testing.T) {
	// Given: a user with no identity policy, and a resource policy naming them
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "bucket-reader")

	// When: the bucket policy is supplied to the simulation
	results := simulate(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":       {"arn:aws:iam::000000000000:user/bucket-reader"},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
		"ResourcePolicy": {`{"Statement":[{"Effect":"Allow",` +
			`"Principal":{"AWS":"arn:aws:iam::000000000000:user/bucket-reader"},` +
			`"Action":"s3:GetObject","Resource":"arn:aws:s3:::reports/*"}]}`},
	})

	// Then: within one account the resource policy alone grants access
	if results[0].EvalDecision != "allowed" {
		t.Fatalf("EvalDecision = %q, want allowed", results[0].EvalDecision)
	}
}

// ─── SimulateCustomPolicy ────────────────────────────────────────────────────

func TestSimulateCustomPolicy_allowStatement_allowed(t *testing.T) {
	// Given: a policy supplied inline, with no IAM entity involved
	srv := helpers.NewTestServer(t)

	// When: a covered action is simulated
	results := simulate(t, srv, "SimulateCustomPolicy", url.Values{
		"PolicyInputList.member.1": {allowS3ReadDoc},
		"ActionNames.member.1":     {"s3:GetObject"},
		"ResourceArns.member.1":    {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: it is allowed
	if results[0].EvalDecision != "allowed" {
		t.Fatalf("EvalDecision = %q, want allowed", results[0].EvalDecision)
	}
	if results[0].MatchedStatements[0].SourcePolicyId != "PolicyInputList.1" {
		t.Fatalf("SourcePolicyId = %q, want PolicyInputList.1", results[0].MatchedStatements[0].SourcePolicyId)
	}
}

func TestSimulateCustomPolicy_multipleStatementsSameDocument_distinctPositions(t *testing.T) {
	// Given: one policy document carrying two statements — an allow and a
	// deny — the shape that used to make MatchedStatements report two
	// identical entries, since SourcePolicyId/SourcePolicyType alone cannot
	// tell statements within the same document apart
	srv := helpers.NewTestServer(t)
	doc := `{"Version":"2012-10-17","Statement":[` +
		`{"Sid":"AllowAll","Effect":"Allow","Action":"s3:*","Resource":"*"},` +
		`{"Sid":"DenyDelete","Effect":"Deny","Action":"s3:DeleteObject","Resource":"arn:aws:s3:::iamrc-bucket/*"}]}`

	// When: the denied action is simulated
	results := simulate(t, srv, "SimulateCustomPolicy", url.Values{
		"PolicyInputList.member.1": {doc},
		"ActionNames.member.1":     {"s3:DeleteObject"},
		"ResourceArns.member.1":    {"arn:aws:s3:::iamrc-bucket/file.txt"},
	})

	// Then: the explicit deny wins, both statements are reported (AWS lists
	// everything that applied, not just the winner) with the same
	// SourcePolicyId (one input document) — and distinct positions, without
	// which there is no way to tell which one actually decided the outcome
	if results[0].EvalDecision != "explicitDeny" {
		t.Fatalf("EvalDecision = %q, want explicitDeny", results[0].EvalDecision)
	}
	if len(results[0].MatchedStatements) != 2 {
		t.Fatalf("MatchedStatements = %#v, want 2", results[0].MatchedStatements)
	}
	first, second := results[0].MatchedStatements[0], results[0].MatchedStatements[1]
	if first.SourcePolicyId != "PolicyInputList.1" || second.SourcePolicyId != "PolicyInputList.1" {
		t.Fatalf("SourcePolicyId = %+v / %+v, want both PolicyInputList.1", first, second)
	}
	if first.StartPosition == nil || second.StartPosition == nil {
		t.Fatalf("StartPosition = %+v / %+v, want both set", first.StartPosition, second.StartPosition)
	}
	if *first.StartPosition == *second.StartPosition {
		t.Fatalf("both statements report StartPosition %+v — cannot tell which one decided the outcome",
			*first.StartPosition)
	}
}

func TestSimulateCustomPolicy_missingPolicyInputList_missingParameter(t *testing.T) {
	// Given: no policies supplied
	srv := helpers.NewTestServer(t)

	// When: the simulation runs
	resp := iamCall(t, srv, "SimulateCustomPolicy", url.Values{
		"ActionNames.member.1": {"s3:GetObject"},
	})
	defer resp.Body.Close()

	// Then: the required parameter is reported
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestSimulateCustomPolicy_malformedPolicy_invalidInput(t *testing.T) {
	// Given: a policy document that is not valid JSON
	srv := helpers.NewTestServer(t)

	// When: the simulation runs
	resp := iamCall(t, srv, "SimulateCustomPolicy", url.Values{
		"PolicyInputList.member.1": {"{not json"},
		"ActionNames.member.1":     {"s3:GetObject"},
	})
	defer resp.Body.Close()

	// Then: AWS's InvalidInput comes back, not a decision derived from nothing
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidInput")
}

func TestSimulateCustomPolicy_conditionKeyMissing_reportsMissingContextValues(t *testing.T) {
	// Given: a policy conditional on a context key the call does not supply
	srv := helpers.NewTestServer(t)

	// When: the simulation runs
	results := simulate(t, srv, "SimulateCustomPolicy", url.Values{
		"PolicyInputList.member.1": {`{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*",
			"Condition":{"StringEquals":{"aws:SourceVpc":"vpc-123"}}}]}`},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: the missing key is reported rather than silently ignored
	if results[0].EvalDecision != "implicitDeny" {
		t.Fatalf("EvalDecision = %q, want implicitDeny", results[0].EvalDecision)
	}
	if len(results[0].MissingContextValues) != 1 || results[0].MissingContextValues[0] != "aws:SourceVpc" {
		t.Fatalf("MissingContextValues = %#v, want [aws:SourceVpc]", results[0].MissingContextValues)
	}
}

func TestSimulateCustomPolicy_contextEntrySatisfiesCondition_allowed(t *testing.T) {
	// Given: a policy conditional on a context key, and that key supplied
	srv := helpers.NewTestServer(t)

	// When: the simulation runs with a matching ContextEntry
	results := simulate(t, srv, "SimulateCustomPolicy", url.Values{
		"PolicyInputList.member.1": {`{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*",
			"Condition":{"StringEquals":{"aws:RequestedRegion":"eu-west-1"}}}]}`},
		"ActionNames.member.1":                              {"s3:GetObject"},
		"ResourceArns.member.1":                             {"arn:aws:s3:::reports/q1.csv"},
		"ContextEntries.member.1.ContextKeyName":            {"aws:RequestedRegion"},
		"ContextEntries.member.1.ContextKeyType":            {"string"},
		"ContextEntries.member.1.ContextKeyValues.member.1": {"eu-west-1"},
	})

	// Then: the condition is satisfied
	if results[0].EvalDecision != "allowed" {
		t.Fatalf("EvalDecision = %q, want allowed", results[0].EvalDecision)
	}
	if len(results[0].MissingContextValues) != 0 {
		t.Fatalf("MissingContextValues = %#v, want none", results[0].MissingContextValues)
	}
}

func TestSimulateCustomPolicy_unknownConditionOperator_policyEvaluationError(t *testing.T) {
	// Given: a policy using a condition operator the evaluator does not implement
	srv := helpers.NewTestServer(t)

	// When: the simulation runs
	resp := iamCall(t, srv, "SimulateCustomPolicy", url.Values{
		"PolicyInputList.member.1": {`{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*",
			"Condition":{"StringPalindrome":{"aws:RequestedRegion":"eu-west-1"}}}]}`},
		"ActionNames.member.1":                              {"s3:GetObject"},
		"ResourceArns.member.1":                             {"arn:aws:s3:::reports/q1.csv"},
		"ContextEntries.member.1.ContextKeyName":            {"aws:RequestedRegion"},
		"ContextEntries.member.1.ContextKeyValues.member.1": {"eu-west-1"},
	})
	defer resp.Body.Close()

	// Then: the gap is reported as AWS's PolicyEvaluation failure rather than
	// being resolved to an allow or a deny
	helpers.AssertStatus(t, resp, http.StatusInternalServerError)
	helpers.AssertQueryXMLError(t, resp, "PolicyEvaluation")
}

func TestSimulateCustomPolicy_permissionsBoundaryBlocks_implicitDeny(t *testing.T) {
	// Given: an identity policy allowing an action, and a boundary that does not
	srv := helpers.NewTestServer(t)

	// When: the simulation runs with the boundary supplied
	results := simulate(t, srv, "SimulateCustomPolicy", url.Values{
		"PolicyInputList.member.1": {`{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
		"PermissionsBoundaryPolicyInputList.member.1": {
			`{"Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"}]}`},
		"ActionNames.member.1":  {"s3:GetObject"},
		"ResourceArns.member.1": {"arn:aws:s3:::reports/q1.csv"},
	})

	// Then: the boundary caps the identity allow
	if results[0].EvalDecision != "implicitDeny" {
		t.Fatalf("EvalDecision = %q, want implicitDeny", results[0].EvalDecision)
	}
	if len(results[0].PermissionsBoundaries) != 1 || results[0].PermissionsBoundaries[0].AllowedByPermissionsBoundary {
		t.Fatalf("PermissionsBoundaryDecisionDetail = %#v, want AllowedByPermissionsBoundary=false",
			results[0].PermissionsBoundaries)
	}
}

// Package iam_test — policy-document validation at the API boundary (#1717).
//
// Every IAM operation that takes a policy document parses it before storing
// it. AWS refuses a document it cannot parse with MalformedPolicyDocument
// (400) — see the Errors section of each operation in the IAM API Reference,
// e.g. https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreatePolicy.html
// — and Overcast used to accept anything, so a policy AWS would reject
// outright survived into a template or a bootstrap script and only failed at
// the first real deploy.
//
// The two halves of this file matter equally: the rejections, and the
// acceptances. The regression risk of adding validation is rejecting a shape
// AWS accepts, so every legal spelling the grammar allows — a single-object
// Statement, Action as a string or a list, NotAction, Condition, Sid,
// Resource/NotResource, an omitted Version, a Principal on a trust policy —
// has a test that says it still works.
//
// Run: go test ./tests/integration/iam/...
package iam_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// validAssumePolicy is the trust policy the fixtures below attach to a role.
const validAssumePolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

// validIdentityPolicy is a minimal well-formed identity policy.
const validIdentityPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`

// malformedDocuments are the documents AWS refuses with
// MalformedPolicyDocument. Each is a real authoring mistake: a truncated or
// hand-edited document, a template whose variable never got substituted, a
// Statement that ended up the wrong JSON type, a statement missing an element
// the grammar requires.
var malformedDocuments = map[string]string{
	"not json":              `{"Version":"2012-10-17","Statement":[`,
	"trailing comma":        `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*",}]}`,
	"unsubstituted":         `{"Version":"2012-10-17","Statement":${POLICY_STATEMENTS}}`,
	"json array":            `[{"Effect":"Allow","Action":"s3:*"}]`,
	"json string":           `"a policy"`,
	"empty object":          `{}`,
	"no statement":          `{"Version":"2012-10-17"}`,
	"empty statement list":  `{"Version":"2012-10-17","Statement":[]}`,
	"statement is a string": `{"Version":"2012-10-17","Statement":"s3:GetObject"}`,
	"statement missing effect": `{"Version":"2012-10-17","Statement":[` +
		`{"Action":"s3:GetObject","Resource":"*"}]}`,
	"statement bad effect": `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Permit","Action":"s3:GetObject","Resource":"*"}]}`,
	"statement missing action": `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Resource":"*"}]}`,
	"statement action and notaction": `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Action":"s3:*","NotAction":"s3:DeleteObject","Resource":"*"}]}`,
	"second statement malformed": `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"},` +
		`{"Effect":"Allow","Resource":"*"}]}`,
}

// wellFormedDocuments are the shapes AWS accepts. Rejecting any of them would
// be the regression this validation risks.
var wellFormedDocuments = map[string]string{
	"statement list":    validIdentityPolicy,
	"single object":     `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}}`,
	"action list":       `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":"*"}]}`,
	"not action":        `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","NotAction":"s3:GetObject","Resource":"*"}]}`,
	"not resource":      `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:*","NotResource":"arn:aws:s3:::public/*"}]}`,
	"no resource":       `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject"}]}`,
	"no version":        `{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`,
	"legacy version":    `{"Version":"2008-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`,
	"sid":               `{"Version":"2012-10-17","Statement":[{"Sid":"ReadOnly","Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`,
	"condition":         `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*","Condition":{"StringEquals":{"aws:PrincipalTag/team":"platform"}}}]}`,
	"numeric condition": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*","Condition":{"NumericLessThan":{"s3:max-keys":10}}}]}`,
	"multiple statements": `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Action":"s3:*","Resource":"*"},` +
		`{"Effect":"Deny","Action":"s3:DeleteBucket","Resource":"*"}]}`,
	"pretty printed": "{\n  \"Version\": \"2012-10-17\",\n  \"Statement\": [\n    {\n" +
		"      \"Effect\": \"Allow\",\n      \"Action\": \"s3:GetObject\",\n" +
		"      \"Resource\": \"*\"\n    }\n  ]\n}",
}

// wellFormedTrustDocuments are the extra shapes a trust policy carries: a
// Principal block, which an identity policy may not have but a role's
// AssumeRolePolicyDocument always does.
var wellFormedTrustDocuments = map[string]string{
	"service principal":   validAssumePolicy,
	"aws principal":       `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"sts:AssumeRole"}]}`,
	"wildcard principal":  `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"sts:AssumeRole"}]}`,
	"principal list":      `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":["ec2.amazonaws.com","lambda.amazonaws.com"]},"Action":"sts:AssumeRole"}]}`,
	"federated principal": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Federated":"cognito-identity.amazonaws.com"},"Action":"sts:AssumeRoleWithWebIdentity"}]}`,
	"with condition": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},` +
		`"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"abc123"}}}]}`,
}

// assertMalformedPolicyDocument asserts AWS's boundary refusal: a 400 carrying
// the MalformedPolicyDocument code in the Query error envelope.
func assertMalformedPolicyDocument(t *testing.T, srv *helpers.TestServer, action string, params url.Values) {
	t.Helper()
	resp := iamCall(t, srv, action, params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "MalformedPolicyDocument")
}

// assertIAMOK asserts the call succeeded.
func assertIAMOK(t *testing.T, srv *helpers.TestServer, action string, params url.Values) {
	t.Helper()
	resp := iamCall(t, srv, action, params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── CreatePolicy ─────────────────────────────────────────────────────────────

func TestCreatePolicy_rejectsMalformedDocument(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for name, doc := range malformedDocuments {
		t.Run(name, func(t *testing.T) {
			assertMalformedPolicyDocument(t, srv, "CreatePolicy", url.Values{
				"PolicyName":     {"rejected-" + strings.ReplaceAll(name, " ", "-")},
				"PolicyDocument": {doc},
			})
		})
	}
}

func TestCreatePolicy_rejectionCreatesNothing(t *testing.T) {
	// Given: a CreatePolicy the validator refuses
	srv := helpers.NewTestServer(t)
	assertMalformedPolicyDocument(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     {"never-created"},
		"PolicyDocument": {`{"Version":"2012-10-17"}`},
	})

	// Then: the policy does not exist — the refusal is not a 400 over a
	// resource that got written anyway.
	resp := iamCall(t, srv, "GetPolicy", url.Values{
		"PolicyArn": {"arn:aws:iam::000000000000:policy/never-created"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, resp, "NoSuchEntity")
}

func TestCreatePolicy_acceptsWellFormedDocuments(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for name, doc := range wellFormedDocuments {
		t.Run(name, func(t *testing.T) {
			assertIAMOK(t, srv, "CreatePolicy", url.Values{
				"PolicyName":     {"accepted-" + strings.ReplaceAll(name, " ", "-")},
				"PolicyDocument": {doc},
			})
		})
	}
}

// ─── CreatePolicyVersion ──────────────────────────────────────────────────────

func TestCreatePolicyVersion_rejectsMalformedDocument(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "versioned-policy")

	assertMalformedPolicyDocument(t, srv, "CreatePolicyVersion", url.Values{
		"PolicyArn":      {arn},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[]}`},
		"SetAsDefault":   {"true"},
	})

	// And: the operative document is untouched.
	resp := iamCall(t, srv, "GetPolicy", url.Values{"PolicyArn": {arn}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if body := helpers.ReadBody(t, resp); !strings.Contains(body, "<DefaultVersionId>v1</DefaultVersionId>") {
		t.Errorf("refused CreatePolicyVersion still bumped the default version: %s", body)
	}
}

func TestCreatePolicyVersion_acceptsWellFormedDocument(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "versioned-ok")

	assertIAMOK(t, srv, "CreatePolicyVersion", url.Values{
		"PolicyArn":      {arn},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":{"Effect":"Deny","NotAction":"s3:GetObject","Resource":"*"}}`},
		"SetAsDefault":   {"true"},
	})
}

// ─── CreateRole / UpdateAssumeRolePolicy ──────────────────────────────────────

func TestCreateRole_rejectsMalformedAssumeRolePolicyDocument(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for name, doc := range malformedDocuments {
		t.Run(name, func(t *testing.T) {
			assertMalformedPolicyDocument(t, srv, "CreateRole", url.Values{
				"RoleName":                 {"rejected-" + strings.ReplaceAll(name, " ", "-")},
				"AssumeRolePolicyDocument": {doc},
			})
		})
	}
}

func TestCreateRole_acceptsWellFormedTrustDocuments(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for name, doc := range wellFormedTrustDocuments {
		t.Run(name, func(t *testing.T) {
			assertIAMOK(t, srv, "CreateRole", url.Values{
				"RoleName":                 {"trusted-" + strings.ReplaceAll(name, " ", "-")},
				"AssumeRolePolicyDocument": {doc},
			})
		})
	}
}

func TestUpdateAssumeRolePolicy_rejectsMalformedDocument(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "trust-role")

	assertMalformedPolicyDocument(t, srv, "UpdateAssumeRolePolicy", url.Values{
		"RoleName":       {"trust-role"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Action":"sts:AssumeRole"}]}`},
	})

	// And: the role keeps the trust policy it was created with.
	resp := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"trust-role"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if body := helpers.ReadBody(t, resp); !strings.Contains(body, "ec2.amazonaws.com") {
		t.Errorf("refused UpdateAssumeRolePolicy overwrote the trust policy: %s", body)
	}
}

func TestUpdateAssumeRolePolicy_acceptsWellFormedDocument(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "trust-role")

	assertIAMOK(t, srv, "UpdateAssumeRolePolicy", url.Values{
		"RoleName":       {"trust-role"},
		"PolicyDocument": {wellFormedTrustDocuments["aws principal"]},
	})
}

// ─── Put*Policy ───────────────────────────────────────────────────────────────

// TestPutInlinePolicy_rejectsMalformedDocument covers all three inline-policy
// writers: the issue's "scope worth confirming while fixing".
func TestPutInlinePolicy_rejectsMalformedDocument(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "inline-role")
	createUser(t, srv, "inline-user")
	createGroup(t, srv, "inline-group")

	for _, tc := range []struct{ action, nameKey, entity string }{
		{"PutRolePolicy", "RoleName", "inline-role"},
		{"PutUserPolicy", "UserName", "inline-user"},
		{"PutGroupPolicy", "GroupName", "inline-group"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			for name, doc := range malformedDocuments {
				t.Run(name, func(t *testing.T) {
					assertMalformedPolicyDocument(t, srv, tc.action, url.Values{
						tc.nameKey:       {tc.entity},
						"PolicyName":     {"rejected"},
						"PolicyDocument": {doc},
					})
				})
			}
		})
	}
}

func TestPutInlinePolicy_acceptsWellFormedDocument(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "inline-role")
	createUser(t, srv, "inline-user")
	createGroup(t, srv, "inline-group")

	for _, tc := range []struct{ action, nameKey, entity string }{
		{"PutRolePolicy", "RoleName", "inline-role"},
		{"PutUserPolicy", "UserName", "inline-user"},
		{"PutGroupPolicy", "GroupName", "inline-group"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			for name, doc := range wellFormedDocuments {
				t.Run(name, func(t *testing.T) {
					assertIAMOK(t, srv, tc.action, url.Values{
						tc.nameKey:       {tc.entity},
						"PolicyName":     {"accepted-" + strings.ReplaceAll(name, " ", "-")},
						"PolicyDocument": {doc},
					})
				})
			}
		})
	}
}

// TestPutRolePolicy_rejectionStoresNothing pins that a refused write leaves no
// inline policy behind — the failure mode that makes a broken document survive
// into a later deploy.
func TestPutRolePolicy_rejectionStoresNothing(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "inline-role")

	assertMalformedPolicyDocument(t, srv, "PutRolePolicy", url.Values{
		"RoleName":       {"inline-role"},
		"PolicyName":     {"never-stored"},
		"PolicyDocument": {`not json at all`},
	})

	resp := iamCall(t, srv, "GetRolePolicy", url.Values{
		"RoleName": {"inline-role"}, "PolicyName": {"never-stored"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, resp, "NoSuchEntity")
}

package lambda_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// resourcePolicyURL builds a GetResourcePolicy/PutResourcePolicy/
// DeleteResourcePolicy URL. The 2026-07-09 API version is its own vintage —
// see internal/awsapi/manifest.gen.go's lambda ResourcePolicy bindings.
func resourcePolicyURL(srv *helpers.TestServer, resourceARN string) string {
	return srv.URL + "/2026-07-09/resource-policy/" + url.PathEscape(resourceARN)
}

// resourcePolicyEnvelope is the shared GetResourcePolicy/PutResourcePolicy
// response shape.
type resourcePolicyEnvelope struct {
	Policy     string `json:"Policy"`
	RevisionID string `json:"RevisionId"`
}

// assertSamePolicyDocument compares two policy documents structurally, so a
// re-serialised document with different key order still matches but a dropped
// member does not.
func assertSamePolicyDocument(t *testing.T, got, want string) {
	t.Helper()
	var gotDoc, wantDoc any
	if err := json.Unmarshal([]byte(got), &gotDoc); err != nil {
		t.Fatalf("decode returned policy: %v (policy=%s)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &wantDoc); err != nil {
		t.Fatalf("decode expected policy: %v", err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Errorf("policy document round-trip lost or changed members:\n got: %s\nwant: %s", got, want)
	}
}

// fullJSONPolicy is a policy of the shape PutResourcePolicy exists for: an
// explicit Deny alongside an Allow, list-valued Action and Resource, and a
// NotPrincipal — none of which AddPermission can express.
func fullJSONPolicy(functionARN string) string {
	return `{"Version":"2012-10-17","Id":"default","Statement":[` +
		`{"Sid":"allow-s3","Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},` +
		`"Action":"lambda:InvokeFunction","Resource":"` + functionARN + `",` +
		`"Condition":{"StringEquals":{"aws:SourceAccount":"000000000000"}}},` +
		`{"Sid":"deny-everyone-else","Effect":"Deny","NotPrincipal":{"Service":"s3.amazonaws.com"},` +
		`"Action":["lambda:InvokeFunction","lambda:GetFunction"],` +
		`"Resource":["` + functionARN + `","` + functionARN + `:*"]}]}`
}

func TestResourcePolicy_PutGetRoundTrip(t *testing.T) {
	// Given: a function and a full JSON resource policy for its ARN.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "rp-roundtrip")
	policy := fullJSONPolicy(fn.FunctionArn)

	// When: PutResourcePolicy stores it.
	putResp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, fn.FunctionArn),
		map[string]any{"Policy": policy})
	helpers.AssertStatus(t, putResp, http.StatusOK)
	var put resourcePolicyEnvelope
	decodeJSON(t, putResp, &put)

	// Then: the response echoes the stored policy and a fresh revision ID.
	assertSamePolicyDocument(t, put.Policy, policy)
	if len(put.RevisionID) != 36 {
		t.Errorf("PutResourcePolicy RevisionId = %q, want a 36-character revision ID", put.RevisionID)
	}

	// And: GetResourcePolicy returns the same document and revision.
	getResp := doJSON(t, http.MethodGet, resourcePolicyURL(srv, fn.FunctionArn), nil)
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var got resourcePolicyEnvelope
	decodeJSON(t, getResp, &got)
	assertSamePolicyDocument(t, got.Policy, policy)
	if got.RevisionID != put.RevisionID {
		t.Errorf("GetResourcePolicy RevisionId = %q, want %q", got.RevisionID, put.RevisionID)
	}

	// And: a second Put replaces the policy wholesale and mints a new revision.
	replacement := `{"Version":"2012-10-17","Id":"default","Statement":[` +
		`{"Sid":"only-statement","Effect":"Allow","Principal":{"Service":"sns.amazonaws.com"},` +
		`"Action":"lambda:InvokeFunction","Resource":"` + fn.FunctionArn + `"}]}`
	replaceResp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, fn.FunctionArn),
		map[string]any{"Policy": replacement, "RevisionId": put.RevisionID})
	helpers.AssertStatus(t, replaceResp, http.StatusOK)
	var replaced resourcePolicyEnvelope
	decodeJSON(t, replaceResp, &replaced)
	assertSamePolicyDocument(t, replaced.Policy, replacement)
	if replaced.RevisionID == put.RevisionID {
		t.Error("PutResourcePolicy reused the previous RevisionId")
	}
}

func TestResourcePolicy_DeleteThenGetIsNotFound(t *testing.T) {
	// Given: a function carrying a resource policy.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "rp-delete")
	putResp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, fn.FunctionArn),
		map[string]any{"Policy": fullJSONPolicy(fn.FunctionArn)})
	helpers.AssertStatus(t, putResp, http.StatusOK)
	var put resourcePolicyEnvelope
	decodeJSON(t, putResp, &put)

	// When: DeleteResourcePolicy removes it with the current revision.
	deleteResp := doJSON(t, http.MethodDelete,
		resourcePolicyURL(srv, fn.FunctionArn)+"?RevisionId="+url.QueryEscape(put.RevisionID), nil)
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)

	// Then: GetResourcePolicy reports the policy as gone.
	getResp := doJSON(t, http.MethodGet, resourcePolicyURL(srv, fn.FunctionArn), nil)
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
	helpers.AssertJSONError(t, getResp, "ResourceNotFoundException")

	// And: so does a second delete, and GetPolicy over the same policy.
	repeatResp := doJSON(t, http.MethodDelete, resourcePolicyURL(srv, fn.FunctionArn), nil)
	defer repeatResp.Body.Close()
	helpers.AssertStatus(t, repeatResp, http.StatusNotFound)
	helpers.AssertJSONError(t, repeatResp, "ResourceNotFoundException")

	legacyResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/rp-delete/policy"), nil)
	defer legacyResp.Body.Close()
	helpers.AssertStatus(t, legacyResp, http.StatusNotFound)
	helpers.AssertJSONError(t, legacyResp, "ResourceNotFoundException")
}

func TestResourcePolicy_InvalidOrUnknownResourceArn(t *testing.T) {
	// Given: a server with one function, so only ARN shape decides the answer.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "rp-arn-scope")

	// ResourceArn is modeled as a complete function ARN. Every other Lambda
	// resource type — layers, event source mappings, code signing configs —
	// fails the pattern, which is the answer AWS gives for them too.
	malformed := []string{
		"rp-arn-scope",
		"arn:aws:lambda:us-east-1:000000000000:layer:my-layer:1",
		"arn:aws:lambda:us-east-1:000000000000:event-source-mapping:0fd0f1c2-3456-4789-abcd-ef0123456789",
		"arn:aws:lambda:us-east-1:000000000000:code-signing-config:csc-0123456789abcdefg",
		"arn:aws:lambda:us-east-1:000000000000:function:*",
		"arn:aws:sqs:us-east-1:000000000000:function:rp-arn-scope",
	}
	for _, arn := range malformed {
		t.Run("malformed/"+arn, func(t *testing.T) {
			// When: any of the three operations is called with it.
			for _, probe := range []struct {
				method string
				body   any
			}{
				{http.MethodGet, nil},
				{http.MethodPut, map[string]any{"Policy": `{"Version":"2012-10-17","Statement":[]}`}},
				{http.MethodDelete, nil},
			} {
				resp := doJSON(t, probe.method, resourcePolicyURL(srv, arn), probe.body)
				// Then: it is a validation failure, not a not-found and not a 501.
				helpers.AssertStatus(t, resp, http.StatusBadRequest)
				helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
				resp.Body.Close()
			}
		})
	}

	// A well-formed ARN naming something this account and region does not hold
	// is a not-found instead.
	unknown := []string{
		"arn:aws:lambda:us-east-1:000000000000:function:no-such-function",
		"arn:aws:lambda:us-east-1:000000000000:function:rp-arn-scope:no-such-alias",
		"arn:aws:lambda:us-west-2:000000000000:function:rp-arn-scope",
		"arn:aws:lambda:us-east-1:123456789012:function:rp-arn-scope",
	}
	for _, arn := range unknown {
		t.Run("unknown/"+arn, func(t *testing.T) {
			// When: GetResourcePolicy addresses it.
			resp := doJSON(t, http.MethodGet, resourcePolicyURL(srv, arn), nil)
			defer resp.Body.Close()
			// Then: Lambda's ResourceNotFoundException answers.
			helpers.AssertStatus(t, resp, http.StatusNotFound)
			helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
		})
	}
}

func TestResourcePolicy_RevisionIdMismatchIsPreconditionFailed(t *testing.T) {
	// Given: a function whose resource policy is at a known revision.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "rp-revision")
	policy := fullJSONPolicy(fn.FunctionArn)
	putResp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, fn.FunctionArn),
		map[string]any{"Policy": policy})
	helpers.AssertStatus(t, putResp, http.StatusOK)
	var put resourcePolicyEnvelope
	decodeJSON(t, putResp, &put)

	stale := "00000000-0000-4000-8000-000000000000"

	// When: PutResourcePolicy supplies a stale revision.
	staleputResp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, fn.FunctionArn),
		map[string]any{"Policy": policy, "RevisionId": stale})
	defer staleputResp.Body.Close()
	// Then: the replacement is refused.
	helpers.AssertStatus(t, staleputResp, http.StatusPreconditionFailed)
	helpers.AssertJSONError(t, staleputResp, "PreconditionFailedException")

	// And: so is a stale DeleteResourcePolicy.
	staleDeleteResp := doJSON(t, http.MethodDelete,
		resourcePolicyURL(srv, fn.FunctionArn)+"?RevisionId="+url.QueryEscape(stale), nil)
	defer staleDeleteResp.Body.Close()
	helpers.AssertStatus(t, staleDeleteResp, http.StatusPreconditionFailed)
	helpers.AssertJSONError(t, staleDeleteResp, "PreconditionFailedException")

	// And: the policy is untouched.
	getResp := doJSON(t, http.MethodGet, resourcePolicyURL(srv, fn.FunctionArn), nil)
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var got resourcePolicyEnvelope
	decodeJSON(t, getResp, &got)
	if got.RevisionID != put.RevisionID {
		t.Errorf("RevisionId = %q after two refused writes, want %q", got.RevisionID, put.RevisionID)
	}
}

func TestResourcePolicy_IsTheSamePolicyAsAddPermission(t *testing.T) {
	// Given: a function whose policy was built statement-by-statement.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "rp-shared")
	addResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/rp-shared/policy"), map[string]any{
		"StatementId": "from-add-permission",
		"Action":      "lambda:InvokeFunction",
		"Principal":   "sns.amazonaws.com",
	})
	defer addResp.Body.Close()
	helpers.AssertStatus(t, addResp, http.StatusCreated)

	// When: GetResourcePolicy reads it.
	getResp := doJSON(t, http.MethodGet, resourcePolicyURL(srv, fn.FunctionArn), nil)
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var viaResourcePolicy resourcePolicyEnvelope
	decodeJSON(t, getResp, &viaResourcePolicy)

	// Then: it is the document GetPolicy returns, at the same revision.
	legacyResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/rp-shared/policy"), nil)
	helpers.AssertStatus(t, legacyResp, http.StatusOK)
	var viaGetPolicy resourcePolicyEnvelope
	decodeJSON(t, legacyResp, &viaGetPolicy)
	assertSamePolicyDocument(t, viaResourcePolicy.Policy, viaGetPolicy.Policy)
	if viaResourcePolicy.RevisionID != viaGetPolicy.RevisionID {
		t.Errorf("GetResourcePolicy RevisionId = %q, GetPolicy RevisionId = %q — one policy, one revision",
			viaResourcePolicy.RevisionID, viaGetPolicy.RevisionID)
	}

	// When: PutResourcePolicy replaces the whole policy.
	replacement := `{"Version":"2012-10-17","Id":"default","Statement":[` +
		`{"Sid":"from-put-resource-policy","Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},` +
		`"Action":"lambda:InvokeFunction","Resource":"` + fn.FunctionArn + `"}]}`
	putResp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, fn.FunctionArn),
		map[string]any{"Policy": replacement})
	helpers.AssertStatus(t, putResp, http.StatusOK)
	putResp.Body.Close()

	// Then: AddPermission's statement is gone — Put replaces, it does not merge.
	afterResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/rp-shared/policy"), nil)
	helpers.AssertStatus(t, afterResp, http.StatusOK)
	var after resourcePolicyEnvelope
	decodeJSON(t, afterResp, &after)
	assertSamePolicyDocument(t, after.Policy, replacement)

	// And: RemovePermission still addresses a statement Put wrote.
	removeResp := doJSON(t, http.MethodDelete,
		lambdaURL(srv, "/functions/rp-shared/policy/from-put-resource-policy"), nil)
	defer removeResp.Body.Close()
	helpers.AssertStatus(t, removeResp, http.StatusNoContent)

	goneResp := doJSON(t, http.MethodGet, resourcePolicyURL(srv, fn.FunctionArn), nil)
	defer goneResp.Body.Close()
	helpers.AssertStatus(t, goneResp, http.StatusNotFound)
	helpers.AssertJSONError(t, goneResp, "ResourceNotFoundException")
}

func TestResourcePolicy_AddPermissionAppendsToAPutPolicy(t *testing.T) {
	// Given: a function whose policy arrived as a full JSON document.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "rp-append")
	putResp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, fn.FunctionArn),
		map[string]any{"Policy": fullJSONPolicy(fn.FunctionArn)})
	helpers.AssertStatus(t, putResp, http.StatusOK)
	var put resourcePolicyEnvelope
	decodeJSON(t, putResp, &put)

	// When: AddPermission adds one more statement.
	addResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/rp-append/policy"), map[string]any{
		"StatementId": "appended",
		"Action":      "lambda:InvokeFunction",
		"Principal":   "sns.amazonaws.com",
	})
	defer addResp.Body.Close()
	helpers.AssertStatus(t, addResp, http.StatusCreated)

	// Then: GetResourcePolicy shows three statements and a new revision.
	getResp := doJSON(t, http.MethodGet, resourcePolicyURL(srv, fn.FunctionArn), nil)
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var got resourcePolicyEnvelope
	decodeJSON(t, getResp, &got)
	var document struct {
		Statement []struct {
			Sid string `json:"Sid"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(got.Policy), &document); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	var sids []string
	for _, statement := range document.Statement {
		sids = append(sids, statement.Sid)
	}
	if want := []string{"allow-s3", "deny-everyone-else", "appended"}; !reflect.DeepEqual(sids, want) {
		t.Errorf("statement IDs = %v, want %v", sids, want)
	}
	if got.RevisionID == put.RevisionID {
		t.Error("AddPermission left the resource policy revision unchanged")
	}
}

func TestResourcePolicy_PutValidation(t *testing.T) {
	// Given: a function to address.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "rp-validation")
	target := resourcePolicyURL(srv, fn.FunctionArn)

	cases := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"missing policy", map[string]any{}, http.StatusBadRequest, "InvalidParameterValueException"},
		{"empty policy", map[string]any{"Policy": ""}, http.StatusBadRequest, "InvalidParameterValueException"},
		{"policy is not JSON", map[string]any{"Policy": "not a policy"}, http.StatusBadRequest, "InvalidParameterValueException"},
		{"policy has no statements", map[string]any{"Policy": `{"Version":"2012-10-17","Statement":[]}`},
			http.StatusBadRequest, "InvalidParameterValueException"},
		{"revision id is not a revision", map[string]any{
			"Policy":     fullJSONPolicy(fn.FunctionArn),
			"RevisionId": "not-a-revision-id",
		}, http.StatusBadRequest, "InvalidParameterValueException"},
		{"policy too long", map[string]any{
			"Policy": `{"Version":"2012-10-17","Id":"` + strings.Repeat("x", 20481) + `","Statement":[` +
				`{"Sid":"s","Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},` +
				`"Action":"lambda:InvokeFunction","Resource":"` + fn.FunctionArn + `"}]}`,
		}, http.StatusBadRequest, "PolicyLengthExceededException"},
		// Lambda blocks a public policy by default, and a statement with a
		// wildcard principal and no condition is its definition of public.
		{"policy grants public access", map[string]any{
			"Policy": `{"Version":"2012-10-17","Statement":[{"Sid":"everyone","Effect":"Allow",` +
				`"Principal":"*","Action":"lambda:InvokeFunction","Resource":"` + fn.FunctionArn + `"}]}`,
		}, http.StatusBadRequest, "PublicPolicyException"},
		{"policy grants public access through AWS", map[string]any{
			"Policy": `{"Version":"2012-10-17","Statement":[{"Sid":"everyone","Effect":"Allow",` +
				`"Principal":{"AWS":["*"]},"Action":"lambda:InvokeFunction","Resource":"` + fn.FunctionArn + `"}]}`,
		}, http.StatusBadRequest, "PublicPolicyException"},
		{"an empty Condition narrows nothing", map[string]any{
			"Policy": `{"Version":"2012-10-17","Statement":[{"Sid":"everyone","Effect":"Allow",` +
				`"Principal":"*","Action":"lambda:InvokeFunction","Resource":"` + fn.FunctionArn + `","Condition":{}}]}`,
		}, http.StatusBadRequest, "PublicPolicyException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: PutResourcePolicy is called with it.
			resp := doJSON(t, http.MethodPut, target, tc.body)
			defer resp.Body.Close()
			// Then: AWS's error answers.
			helpers.AssertStatus(t, resp, tc.status)
			helpers.AssertJSONError(t, resp, tc.code)
		})
	}
}

func TestResourcePolicy_WildcardPrincipalNarrowedByAConditionIsAccepted(t *testing.T) {
	// Given: AWS's own organization-access example, which names every principal
	// but narrows it with a condition — public only without the condition.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "rp-org-access")
	// The document also omits Version and Id, which Lambda names for you.
	statement := `{"Sid":"org-access","Effect":"Allow",` +
		`"Principal":"*","Action":"lambda:InvokeFunction","Resource":"` + fn.FunctionArn + `",` +
		`"Condition":{"StringEquals":{"aws:PrincipalOrgID":"o-a1b2c3d4e5"}}}`

	// When: PutResourcePolicy stores it.
	resp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, fn.FunctionArn),
		map[string]any{"Policy": `{"Statement":[` + statement + `]}`})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var put resourcePolicyEnvelope
	decodeJSON(t, resp, &put)

	// Then: the statement is stored as given, under the identity a Lambda
	// function policy always carries.
	assertSamePolicyDocument(t, put.Policy,
		`{"Version":"2012-10-17","Id":"default","Statement":[`+statement+`]}`)
}

func TestResourcePolicy_QualifiedArnsAreSeparatePolicies(t *testing.T) {
	// Given: a function with a published version and an alias over it.
	srv := helpers.NewTestServer(t)
	fn := createFunction(t, srv, "rp-qualified")
	publishResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/rp-qualified/versions"), map[string]any{})
	helpers.AssertStatus(t, publishResp, http.StatusCreated)
	publishResp.Body.Close()
	aliasResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/rp-qualified/aliases"), map[string]any{
		"Name": "live", "FunctionVersion": "1",
	})
	helpers.AssertStatus(t, aliasResp, http.StatusCreated)
	aliasResp.Body.Close()

	aliasARN := fn.FunctionArn + ":live"
	aliasPolicy := `{"Version":"2012-10-17","Id":"default","Statement":[` +
		`{"Sid":"alias-only","Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},` +
		`"Action":"lambda:InvokeFunction","Resource":"` + aliasARN + `"}]}`

	// When: a policy is put on the alias ARN.
	putResp := doJSON(t, http.MethodPut, resourcePolicyURL(srv, aliasARN), map[string]any{"Policy": aliasPolicy})
	helpers.AssertStatus(t, putResp, http.StatusOK)
	putResp.Body.Close()

	// Then: the alias carries it.
	aliasGet := doJSON(t, http.MethodGet, resourcePolicyURL(srv, aliasARN), nil)
	helpers.AssertStatus(t, aliasGet, http.StatusOK)
	var alias resourcePolicyEnvelope
	decodeJSON(t, aliasGet, &alias)
	assertSamePolicyDocument(t, alias.Policy, aliasPolicy)

	// And: the unqualified function does not.
	unqualifiedGet := doJSON(t, http.MethodGet, resourcePolicyURL(srv, fn.FunctionArn), nil)
	defer unqualifiedGet.Body.Close()
	helpers.AssertStatus(t, unqualifiedGet, http.StatusNotFound)
	helpers.AssertJSONError(t, unqualifiedGet, "ResourceNotFoundException")
}

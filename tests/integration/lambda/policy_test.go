package lambda_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestFunctionPolicy_AddGetRemoveLifecycle(t *testing.T) {
	// Given: a Lambda function and an AWS-shaped service permission request.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "policy-fn")
	permission := map[string]any{
		"Action":        "lambda:InvokeFunction",
		"Principal":     "sns.amazonaws.com",
		"SourceArn":     "arn:aws:sns:us-east-1:000000000000:policy-topic",
		"SourceAccount": "000000000000",
		"StatementId":   "allow-sns",
	}

	// When: AddPermission is called.
	addResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-fn/policy"), permission)
	helpers.AssertStatus(t, addResp, http.StatusCreated)
	var addEnvelope struct {
		Statement string `json:"Statement"`
	}
	decodeJSON(t, addResp, &addEnvelope)
	var added struct {
		Sid       string                       `json:"Sid"`
		Effect    string                       `json:"Effect"`
		Principal map[string]string            `json:"Principal"`
		Action    string                       `json:"Action"`
		Resource  string                       `json:"Resource"`
		Condition map[string]map[string]string `json:"Condition"`
	}
	if err := json.Unmarshal([]byte(addEnvelope.Statement), &added); err != nil {
		t.Fatalf("decode AddPermission Statement: %v", err)
	}
	if added.Sid != "allow-sns" || added.Effect != "Allow" || added.Principal["Service"] != "sns.amazonaws.com" || added.Action != "lambda:InvokeFunction" {
		t.Errorf("unexpected AddPermission statement: %+v", added)
	}
	if added.Resource != "arn:aws:lambda:us-east-1:000000000000:function:policy-fn" {
		t.Errorf("Resource = %q", added.Resource)
	}
	if added.Condition["ArnLike"]["AWS:SourceArn"] != "arn:aws:sns:us-east-1:000000000000:policy-topic" || added.Condition["StringEquals"]["AWS:SourceAccount"] != "000000000000" {
		t.Errorf("Condition = %+v", added.Condition)
	}

	// Then: duplicate statement IDs use Lambda's conflict response.
	duplicateResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-fn/policy"), permission)
	defer duplicateResp.Body.Close()
	helpers.AssertStatus(t, duplicateResp, http.StatusConflict)
	helpers.AssertJSONError(t, duplicateResp, "ResourceConflictException")

	// And: GetPolicy returns the serialized policy document and revision ID.
	getResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/policy-fn/policy"), nil)
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var getEnvelope struct {
		Policy     string `json:"Policy"`
		RevisionID string `json:"RevisionId"`
	}
	decodeJSON(t, getResp, &getEnvelope)
	if getEnvelope.RevisionID == "" {
		t.Fatal("GetPolicy returned an empty RevisionId")
	}

	// When: RemovePermission supplies a stale revision, Lambda rejects it.
	badRevision := url.QueryEscape("not-the-current-revision")
	staleResp := doJSON(t, http.MethodDelete,
		lambdaURL(srv, "/functions/policy-fn/policy/allow-sns")+"?RevisionId="+badRevision, nil)
	defer staleResp.Body.Close()
	helpers.AssertStatus(t, staleResp, http.StatusPreconditionFailed)
	helpers.AssertJSONError(t, staleResp, "PreconditionFailedException")

	// When: the current revision is supplied, RemovePermission deletes it.
	removeResp := doJSON(t, http.MethodDelete,
		lambdaURL(srv, "/functions/policy-fn/policy/allow-sns")+"?RevisionId="+url.QueryEscape(getEnvelope.RevisionID), nil)
	defer removeResp.Body.Close()
	helpers.AssertStatus(t, removeResp, http.StatusNoContent)

	// Then: a function with no policy reports ResourceNotFoundException.
	missingResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/policy-fn/policy"), nil)
	defer missingResp.Body.Close()
	helpers.AssertStatus(t, missingResp, http.StatusNotFound)
	helpers.AssertJSONError(t, missingResp, "ResourceNotFoundException")
}

func TestFunctionPolicy_ForeignARNCannotTargetLocalFunction(t *testing.T) {
	// Given: a local function with a policy statement.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "policy-arn-scope")
	localPermission := map[string]any{
		"StatementId": "local", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
	}
	localAdd := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-arn-scope/policy"), localPermission)
	helpers.AssertStatus(t, localAdd, http.StatusCreated)
	localAdd.Body.Close()

	foreignARNs := []string{
		"arn:aws-us-gov:lambda:us-east-1:000000000000:function:policy-arn-scope",
		"arn:aws:lambda:us-west-2:000000000000:function:policy-arn-scope",
		"arn:aws:lambda:us-east-1:123456789012:function:policy-arn-scope",
		"us-west-2:000000000000:function:policy-arn-scope",
		"123456789012:function:policy-arn-scope",
	}
	for _, arn := range foreignARNs {
		t.Run(arn, func(t *testing.T) {
			// When: Add/Get/Remove address the same local name through a foreign ARN.
			add := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/"+arn+"/policy"), map[string]any{
				"StatementId": "foreign", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
			})
			helpers.AssertStatus(t, add, http.StatusNotFound)
			helpers.AssertJSONError(t, add, "ResourceNotFoundException")
			add.Body.Close()

			get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/"+arn+"/policy"), nil)
			helpers.AssertStatus(t, get, http.StatusNotFound)
			helpers.AssertJSONError(t, get, "ResourceNotFoundException")
			get.Body.Close()

			remove := doJSON(t, http.MethodDelete, lambdaURL(srv, "/functions/"+arn+"/policy/local"), nil)
			helpers.AssertStatus(t, remove, http.StatusNotFound)
			helpers.AssertJSONError(t, remove, "ResourceNotFoundException")
			remove.Body.Close()
		})
	}

	// Accepted partial references in the modeled FunctionName pattern still
	// resolve when every explicitly supplied scope component is local.
	localReferences := []string{
		"function:policy-arn-scope",
		"000000000000:function:policy-arn-scope",
		"us-east-1:000000000000:function:policy-arn-scope",
		"000000000000:policy-arn-scope",
		"us-east-1:000000000000:policy-arn-scope",
	}
	for _, reference := range localReferences {
		get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/"+reference+"/policy"), nil)
		helpers.AssertStatus(t, get, http.StatusOK)
		get.Body.Close()
	}

	// Then: the local policy still contains only its original statement.
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/policy-arn-scope/policy"), nil)
	var envelope struct {
		Policy string `json:"Policy"`
	}
	decodeJSON(t, get, &envelope)
	if strings.Count(envelope.Policy, `"Sid"`) != 1 || !strings.Contains(envelope.Policy, `"Sid":"local"`) {
		t.Fatalf("local policy mutated by foreign ARN calls: %s", envelope.Policy)
	}
}

func TestAddPermission_ModeledLengthBoundariesAndAccountPrincipal(t *testing.T) {
	// Given: a function and AddPermission values at the pinned model maxima.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "policy-model-boundaries")
	action := "lambda:" + strings.Repeat("A", 10000-len("lambda:"))
	principal := strings.Repeat("p", 2048)
	resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-model-boundaries/policy"), map[string]any{
		"StatementId": "maxima", "Action": action, "Principal": principal,
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// When: a 12-digit account ID is used as Principal.
	resp = doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-model-boundaries/policy"), map[string]any{
		"StatementId": "account", "Action": "lambda:InvokeFunction", "Principal": "123456789012",
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var add struct {
		Statement string `json:"Statement"`
	}
	decodeJSON(t, resp, &add)
	var statement struct {
		Principal map[string]string `json:"Principal"`
	}
	if err := json.Unmarshal([]byte(add.Statement), &statement); err != nil {
		t.Fatalf("decode statement: %v", err)
	}
	if got := statement.Principal["AWS"]; got != "arn:aws:iam::123456789012:root" {
		t.Fatalf("account principal = %q", got)
	}

	// Then: GetPolicy persists the normalized principal too.
	get := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/policy-model-boundaries/policy"), nil)
	var policy struct {
		Policy string `json:"Policy"`
	}
	decodeJSON(t, get, &policy)
	if !strings.Contains(policy.Policy, `"AWS":"arn:aws:iam::123456789012:root"`) {
		t.Fatalf("stored policy principal was not normalized: %s", policy.Policy)
	}
}

func TestFunctionPolicy_ValidationQualificationAndConcurrentMutation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "policy-race-fn")

	invalidFunctionName := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/"+strings.Repeat("a", 65)+"/policy"), map[string]any{
			"StatementId": "invalid-function", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
		})
	helpers.AssertStatus(t, invalidFunctionName, http.StatusBadRequest)
	helpers.AssertJSONError(t, invalidFunctionName, "InvalidParameterValueException")
	invalidFunctionName.Body.Close()

	malformedPrefixedName := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/garbage:function:policy-race-fn/policy"), map[string]any{
			"StatementId": "malformed-prefix", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
		})
	helpers.AssertStatus(t, malformedPrefixedName, http.StatusBadRequest)
	helpers.AssertJSONError(t, malformedPrefixedName, "InvalidParameterValueException")
	malformedPrefixedName.Body.Close()

	invalidQualifier := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/policy-race-fn/policy")+"?Qualifier=bad%21", map[string]any{
			"StatementId": "invalid-qualifier", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
		})
	helpers.AssertStatus(t, invalidQualifier, http.StatusBadRequest)
	helpers.AssertJSONError(t, invalidQualifier, "InvalidParameterValueException")
	invalidQualifier.Body.Close()

	invalid := []map[string]any{
		{"StatementId": "bad id", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com"},
		{"StatementId": "bad-action", "Action": "s3:GetObject", "Principal": "sns.amazonaws.com"},
		{"StatementId": "bad-account", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com", "SourceAccount": "123"},
		{"StatementId": "bad-arn", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com", "SourceArn": "not-an-arn"},
		{"StatementId": "missing-service", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com", "SourceArn": "arn:aws::us-east-1:000000000000:resource"},
		{"StatementId": "bad-region", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com", "SourceArn": "arn:aws:sns:not-a-region:000000000000:topic"},
		{"StatementId": "bad-account", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com", "SourceArn": "arn:aws:sns:us-east-1:123:topic"},
		{"StatementId": "bad-org", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com", "PrincipalOrgID": "org"},
		{"StatementId": "bad-token", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com", "EventSourceToken": "bad token"},
	}
	for _, request := range invalid {
		resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-race-fn/policy"), request)
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
		resp.Body.Close()
	}

	missingQualifier := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-race-fn:missing/policy"), map[string]any{
		"StatementId": "qualified", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
	})
	helpers.AssertStatus(t, missingQualifier, http.StatusNotFound)
	helpers.AssertJSONError(t, missingQualifier, "ResourceNotFoundException")
	missingQualifier.Body.Close()

	// $LATEST.PUBLISHED is a documented qualifier form. It is syntactically
	// valid even though this function has no resource under that qualifier.
	publishedQualifier := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/policy-race-fn/policy")+"?Qualifier=%24LATEST.PUBLISHED", map[string]any{
			"StatementId": "published-qualified", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
		})
	helpers.AssertStatus(t, publishedQualifier, http.StatusNotFound)
	helpers.AssertJSONError(t, publishedQualifier, "ResourceNotFoundException")
	publishedQualifier.Body.Close()

	versionResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-race-fn/versions"), publishVersionReq{})
	helpers.AssertStatus(t, versionResp, http.StatusCreated)
	versionResp.Body.Close()
	aliasResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-race-fn/aliases"), createAliasReq{
		Name: "live", FunctionVersion: "1",
	})
	helpers.AssertStatus(t, aliasResp, http.StatusCreated)
	aliasResp.Body.Close()
	for qualifier, statementID := range map[string]string{"1": "version-qualified", "live": "alias-qualified"} {
		qualifiedResp := doJSON(t, http.MethodPost,
			lambdaURL(srv, "/functions/policy-race-fn/policy")+"?Qualifier="+qualifier, map[string]any{
				"StatementId": statementID, "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
			})
		helpers.AssertStatus(t, qualifiedResp, http.StatusCreated)
		qualifiedResp.Body.Close()
	}

	const additions = 24
	var wg sync.WaitGroup
	errs := make(chan string, additions)
	for i := 0; i < additions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-race-fn/policy"), map[string]any{
				"StatementId": fmt.Sprintf("statement-%02d", i), "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
			})
			if resp.StatusCode != http.StatusCreated {
				errs <- fmt.Sprintf("addition %d: status %d", i, resp.StatusCode)
			}
			resp.Body.Close()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	getResp := doJSON(t, http.MethodGet, lambdaURL(srv, "/functions/policy-race-fn/policy"), nil)
	var envelope struct {
		Policy string `json:"Policy"`
	}
	decodeJSON(t, getResp, &envelope)
	if got := strings.Count(envelope.Policy, `"Sid"`); got != additions {
		t.Fatalf("concurrent statements = %d, want %d: %s", got, additions, envelope.Policy)
	}

	policyLimitObserved := false
	for i := 0; i < 30; i++ {
		resp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-race-fn/policy"), map[string]any{
			"StatementId": fmt.Sprintf("large-%02d", i), "Action": "lambda:InvokeFunction", "Principal": "s3.amazonaws.com",
			"SourceArn": "arn:aws:s3:::" + strings.Repeat("a", 900),
		})
		if resp.StatusCode == http.StatusBadRequest {
			helpers.AssertJSONError(t, resp, "PolicyLengthExceededException")
			policyLimitObserved = true
			resp.Body.Close()
			break
		}
		helpers.AssertStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}
	if !policyLimitObserved {
		t.Fatal("AddPermission never enforced the 20 KB resource-policy limit")
	}
}

// TestFunctionPolicy_QualifiedTargetExistenceAcrossAddGetRemove is the
// AddPermission-only "policy-race-fn:missing" coverage above, generalized:
// AWS validates that a qualified target actually exists — a published
// version or an alias — before AddPermission, GetPolicy, and RemovePermission
// touch the policy, per
// https://docs.aws.amazon.com/lambda/latest/api/API_AddPermission.html and
// https://docs.aws.amazon.com/lambda/latest/api/API_GetPolicy.html and
// https://docs.aws.amazon.com/lambda/latest/api/API_RemovePermission.html
// (each returns ResourceNotFoundException for a nonexistent qualifier).
func TestFunctionPolicy_QualifiedTargetExistenceAcrossAddGetRemove(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "policy-qualified-existence")
	versionResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-qualified-existence/versions"), publishVersionReq{})
	helpers.AssertStatus(t, versionResp, http.StatusCreated)
	versionResp.Body.Close()
	aliasResp := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/policy-qualified-existence/aliases"), createAliasReq{
		Name: "live", FunctionVersion: "1",
	})
	helpers.AssertStatus(t, aliasResp, http.StatusCreated)
	aliasResp.Body.Close()

	missingQualifiers := []string{"99", "missing-alias"}
	for _, qualifier := range missingQualifiers {
		t.Run("missing/"+qualifier, func(t *testing.T) {
			policyPath := lambdaURL(srv, "/functions/policy-qualified-existence/policy") + "?Qualifier=" + qualifier

			add := doJSON(t, http.MethodPost, policyPath, map[string]any{
				"StatementId": "add-" + qualifier, "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
			})
			helpers.AssertStatus(t, add, http.StatusNotFound)
			helpers.AssertJSONError(t, add, "ResourceNotFoundException")
			add.Body.Close()

			get := doJSON(t, http.MethodGet, policyPath, nil)
			helpers.AssertStatus(t, get, http.StatusNotFound)
			helpers.AssertJSONError(t, get, "ResourceNotFoundException")
			get.Body.Close()

			remove := doJSON(t, http.MethodDelete,
				lambdaURL(srv, "/functions/policy-qualified-existence/policy/some-statement")+"?Qualifier="+qualifier, nil)
			helpers.AssertStatus(t, remove, http.StatusNotFound)
			helpers.AssertJSONError(t, remove, "ResourceNotFoundException")
			remove.Body.Close()
		})
	}

	// Then: the same three operations succeed against the real version and
	// alias qualifiers — a regression control so a future change can't fix
	// the missing-target case by rejecting every qualifier.
	existingQualifiers := map[string]string{"1": "existing-version", "live": "existing-alias"}
	for qualifier, statementID := range existingQualifiers {
		t.Run("existing/"+qualifier, func(t *testing.T) {
			policyPath := lambdaURL(srv, "/functions/policy-qualified-existence/policy") + "?Qualifier=" + qualifier

			add := doJSON(t, http.MethodPost, policyPath, map[string]any{
				"StatementId": statementID, "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
			})
			helpers.AssertStatus(t, add, http.StatusCreated)
			add.Body.Close()

			get := doJSON(t, http.MethodGet, policyPath, nil)
			helpers.AssertStatus(t, get, http.StatusOK)
			get.Body.Close()

			remove := doJSON(t, http.MethodDelete,
				lambdaURL(srv, "/functions/policy-qualified-existence/policy/"+statementID)+"?Qualifier="+qualifier, nil)
			helpers.AssertStatus(t, remove, http.StatusNoContent)
			remove.Body.Close()
		})
	}
}

func TestFunctionPolicy_DeleteAliasRemovesQualifiedPolicy(t *testing.T) {
	// Given: an alias with a qualified resource policy.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "policy-alias-delete-fn")
	versionResp := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/policy-alias-delete-fn/versions"), publishVersionReq{})
	helpers.AssertStatus(t, versionResp, http.StatusCreated)
	versionResp.Body.Close()
	aliasResp := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/policy-alias-delete-fn/aliases"), createAliasReq{
			Name: "live", FunctionVersion: "1",
		})
	helpers.AssertStatus(t, aliasResp, http.StatusCreated)
	aliasResp.Body.Close()
	permissionResp := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/policy-alias-delete-fn/policy")+"?Qualifier=live", map[string]any{
			"StatementId": "alias-policy", "Action": "lambda:InvokeFunction", "Principal": "sns.amazonaws.com",
		})
	helpers.AssertStatus(t, permissionResp, http.StatusCreated)
	permissionResp.Body.Close()

	// When: the alias is deleted and recreated with the same name.
	deleteResp := doJSON(t, http.MethodDelete,
		lambdaURL(srv, "/functions/policy-alias-delete-fn/aliases/live"), nil)
	helpers.AssertStatus(t, deleteResp, http.StatusNoContent)
	deleteResp.Body.Close()
	recreateResp := doJSON(t, http.MethodPost,
		lambdaURL(srv, "/functions/policy-alias-delete-fn/aliases"), createAliasReq{
			Name: "live", FunctionVersion: "1",
		})
	helpers.AssertStatus(t, recreateResp, http.StatusCreated)
	recreateResp.Body.Close()

	// Then: the deleted alias's policy does not reappear on the new resource.
	getResp := doJSON(t, http.MethodGet,
		lambdaURL(srv, "/functions/policy-alias-delete-fn/policy")+"?Qualifier=live", nil)
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
	helpers.AssertJSONError(t, getResp, "ResourceNotFoundException")
}

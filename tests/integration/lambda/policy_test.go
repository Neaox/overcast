package lambda_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
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

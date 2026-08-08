package cloudformation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func seedKMSPolicyUser(t *testing.T, srv *helpers.TestServer, userName string) string {
	t.Helper()
	arn := "arn:aws:iam::" + srv.Config.AccountID + ":user/" + userName
	raw, err := json.Marshal(map[string]string{"UserName": userName, "Arn": arn})
	if err != nil {
		t.Fatalf("marshal IAM user: %v", err)
	}
	if err := srv.Store.Set(context.Background(), "iam:users", userName, string(raw)); err != nil {
		t.Fatalf("seed IAM user: %v", err)
	}
	return arn
}

func kmsPolicyTemplate(principal string, bypass bool) string {
	return fmt.Sprintf(`{"Resources":{"Key":{"Type":"AWS::KMS::Key","Properties":{"BypassPolicyLockoutSafetyCheck":%t,"KeyPolicy":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":%q},"Action":"kms:PutKeyPolicy","Resource":"*"}]}}}}}`, bypass, principal)
}

func TestCreateStack_KMSKeyPolicyLockoutRollsBackWithoutLeakingKey(t *testing.T) {
	// Given: a key policy that excludes CloudFormation's effective local caller
	srv := helpers.NewTestServer(t)
	template := kmsPolicyTemplate(seedKMSPolicyUser(t, srv, "other"), false)

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {"kms-policy-create-rollback"}, "TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: KMS rejects the resource before persistence and the stack rolls back
	waitForStackStatus(t, srv, "kms-policy-create-rollback", "ROLLBACK_COMPLETE")
	list := kmsJSONCall(t, srv, "ListKeys", map[string]any{})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var listed struct {
		Keys []any `json:"Keys"`
	}
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil {
		t.Fatalf("decode ListKeys: %v", err)
	}
	if len(listed.Keys) != 0 {
		t.Fatalf("keys after rollback = %d, want 0", len(listed.Keys))
	}
}

func TestCreateStack_KMSKeyPolicyBypassPreservesUnsafePolicy(t *testing.T) {
	// Given: a key policy that excludes the caller but explicitly opts out of the safety check
	srv := helpers.NewTestServer(t)
	stackName := "kms-policy-create-bypass"
	otherARN := seedKMSPolicyUser(t, srv, "other")
	template := kmsPolicyTemplate(otherARN, true)

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: KMS stores the supplied policy instead of replacing it with a default
	keyID := kmsKeyPhysicalID(t, srv, stackName)
	var policy struct {
		Statement []struct {
			Principal map[string]string `json:"Principal"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(kmsPolicyDocument(t, srv, keyID)), &policy); err != nil {
		t.Fatalf("decode stored policy: %v", err)
	}
	want := otherARN
	if len(policy.Statement) != 1 || policy.Statement[0].Principal["AWS"] != want {
		t.Fatalf("stored policy principal = %#v, want %q", policy.Statement, want)
	}
}

func TestUpdateStack_KMSKeyPolicyLockoutRollsBackWithoutMutation(t *testing.T) {
	// Given: a stack whose policy permits the local account caller
	srv := helpers.NewTestServer(t)
	otherARN := seedKMSPolicyUser(t, srv, "other")
	stackName := "kms-policy-update-rollback"
	originalTemplate := kmsPolicyTemplate("arn:aws:iam::"+srv.Config.AccountID+":root", false)
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {originalTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	keyID := kmsKeyPhysicalID(t, srv, stackName)
	originalPolicy := kmsPolicyDocument(t, srv, keyID)

	// When: an update replaces it with a policy that excludes the caller
	unsafeTemplate := kmsPolicyTemplate(otherARN, false)
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {unsafeTemplate},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)

	// Then: update rollback completes and the rejected policy was never stored
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")
	if got := kmsPolicyDocument(t, srv, keyID); got != originalPolicy {
		t.Fatalf("policy mutated by rejected update: got %q, want %q", got, originalPolicy)
	}
}

// Real CloudFormation calls PutKeyPolicy only when the KeyPolicy property
// changed. A caller-locking policy created with BypassPolicyLockoutSafetyCheck
// must survive an unrelated update (here: a Description change) instead of
// being re-put without bypass and failing the stack update. The Description
// change itself must be applied through UpdateKeyDescription.
func TestUpdateStack_KMSKeyUnrelatedChangeKeepsBypassedPolicyAndAppliesDescription(t *testing.T) {
	// Given: a stack whose key policy excludes the caller, created with bypass
	srv := helpers.NewTestServer(t)
	otherARN := seedKMSPolicyUser(t, srv, "other")
	stackName := "kms-unrelated-update"
	template := func(description string) string {
		return fmt.Sprintf(`{"Resources":{"Key":{"Type":"AWS::KMS::Key","Properties":{"Description":%q,"BypassPolicyLockoutSafetyCheck":true,"KeyPolicy":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":%q},"Action":"kms:PutKeyPolicy","Resource":"*"}]}}}}}`, description, otherARN)
	}
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {template("before")},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	keyID := kmsKeyPhysicalID(t, srv, stackName)
	originalPolicy := kmsPolicyDocument(t, srv, keyID)

	// When: an update changes only the Description
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {template("after")},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)

	// Then: the update completes, the policy is untouched, the description applied
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")
	if got := kmsPolicyDocument(t, srv, keyID); got != originalPolicy {
		t.Fatalf("policy changed by unrelated update: got %q, want %q", got, originalPolicy)
	}
	desc := kmsJSONCall(t, srv, "DescribeKey", map[string]any{"KeyId": keyID})
	defer desc.Body.Close()
	helpers.AssertStatus(t, desc, http.StatusOK)
	var out struct {
		KeyMetadata struct {
			Description string `json:"Description"`
		} `json:"KeyMetadata"`
	}
	if err := json.NewDecoder(desc.Body).Decode(&out); err != nil {
		t.Fatalf("decode DescribeKey: %v", err)
	}
	if out.KeyMetadata.Description != "after" {
		t.Fatalf("Description = %q, want %q", out.KeyMetadata.Description, "after")
	}
}

func kmsPolicyDocument(t *testing.T, srv *helpers.TestServer, keyID string) string {
	t.Helper()
	resp := kmsJSONCall(t, srv, "GetKeyPolicy", map[string]any{"KeyId": keyID, "PolicyName": "default"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Policy string `json:"Policy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode GetKeyPolicy: %v", err)
	}
	return out.Policy
}

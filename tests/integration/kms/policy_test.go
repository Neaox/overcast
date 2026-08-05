package kms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/tests/helpers"
)

const lockoutMessage = "The new key policy will not allow you to update the key policy in the future."

func callerPolicy(principal string) string {
	return fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":%q},"Action":"kms:PutKeyPolicy","Resource":"*"}]}`, principal)
}

func kmsCallAs(t *testing.T, srv *helpers.TestServer, action string, body map[string]any, accessKey string) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "TrentService."+action)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/20260806/us-east-1/kms/aws4_request, SignedHeaders=host;x-amz-date, Signature=test")
	req.Header.Set("X-Amz-Date", "20260806T000000Z")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s request: %v", action, err)
	}
	return resp
}

func kmsCBORCallAs(t *testing.T, srv *helpers.TestServer, action string, body map[string]any, accessKey string) *http.Response {
	t.Helper()
	payload, err := cborlib.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/kms/operation/"+action, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/20260806/us-east-1/kms/aws4_request, SignedHeaders=host;x-amz-date, Signature=test")
	req.Header.Set("X-Amz-Date", "20260806T000000Z")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do CBOR %s request: %v", action, err)
	}
	return resp
}

func seedIAMCaller(t *testing.T, srv *helpers.TestServer, userName, accessKey string) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:iam::%s:user/%s", srv.Config.AccountID, userName)
	record := map[string]any{
		"UserName": userName,
		"UserId":   "AIDA" + userName,
		"Arn":      arn,
		"AccessKeys": []map[string]string{{
			"AccessKeyId": accessKey,
		}},
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal IAM caller: %v", err)
	}
	if err := srv.Store.Set(context.Background(), "iam:users", userName, string(raw)); err != nil {
		t.Fatalf("seed IAM caller: %v", err)
	}
	return arn
}

func seedIAMRole(t *testing.T, srv *helpers.TestServer, roleName string) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:iam::%s:role/%s", srv.Config.AccountID, roleName)
	raw, err := json.Marshal(map[string]any{"RoleName": roleName, "Arn": arn})
	if err != nil {
		t.Fatalf("marshal IAM role: %v", err)
	}
	if err := srv.Store.Set(context.Background(), "iam:roles", roleName, string(raw)); err != nil {
		t.Fatalf("seed IAM role: %v", err)
	}
	return arn
}

func assertPolicyError(t *testing.T, resp *http.Response, cbor bool, wantMessage string) {
	t.Helper()
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var out map[string]string
	if cbor {
		if err := cborlib.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode CBOR error: %v", err)
		}
	} else {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode JSON error: %v", err)
		}
	}
	if out["__type"] != "MalformedPolicyDocumentException" {
		t.Fatalf("error type = %q, want MalformedPolicyDocumentException", out["__type"])
	}
	if wantMessage != "" && out["message"] != wantMessage {
		t.Fatalf("error message = %q, want %q", out["message"], wantMessage)
	}
}

func keyCount(t *testing.T, srv *helpers.TestServer) int {
	t.Helper()
	resp := kmsCall(t, srv, "ListKeys", map[string]any{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Keys []any `json:"Keys"`
	}
	decodeJSON(t, resp, &out)
	return len(out.Keys)
}

func getPolicy(t *testing.T, srv *helpers.TestServer, keyID string) string {
	t.Helper()
	resp := kmsCall(t, srv, "GetKeyPolicy", map[string]any{"KeyId": keyID, "PolicyName": "default"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Policy string `json:"Policy"`
	}
	decodeJSON(t, resp, &out)
	return out.Policy
}

func TestCreateKey_policyLockoutSafety_protocols(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*testing.T, *helpers.TestServer, string, map[string]any, string) *http.Response
		cbor bool
	}{
		{name: "aws-json", call: kmsCallAs},
		{name: "rpc-v2-cbor", call: kmsCBORCallAs, cbor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a caller whose ARN is known to IAM and a policy that grants only another principal
			srv := helpers.NewTestServer(t)
			seedIAMCaller(t, srv, "alice", "AKIAALICE")
			bobARN := seedIAMCaller(t, srv, "bob", "")
			policy := callerPolicy(bobARN)

			// When: the caller creates a key without bypassing lockout safety
			resp := tc.call(t, srv, "CreateKey", map[string]any{"Policy": policy}, "AKIAALICE")

			// Then: KMS rejects the policy and does not persist a key
			assertPolicyError(t, resp, tc.cbor, lockoutMessage)
			if got := keyCount(t, srv); got != 0 {
				t.Fatalf("key count = %d, want 0 after rejected CreateKey", got)
			}
		})
	}
}

func TestCreateKey_policyAllowsCallerAndPreservesDocument(t *testing.T) {
	// Given: a known IAM caller and a policy that directly grants it PutKeyPolicy
	srv := helpers.NewTestServer(t)
	callerARN := seedIAMCaller(t, srv, "alice", "AKIAALICE")
	policy := callerPolicy(callerARN)

	// When: the caller creates the key with that policy
	resp := kmsCallAs(t, srv, "CreateKey", map[string]any{"Policy": policy}, "AKIAALICE")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		KeyMetadata struct {
			KeyID string `json:"KeyId"`
		} `json:"KeyMetadata"`
	}
	decodeJSON(t, resp, &out)

	// Then: the supplied policy is stored byte-for-byte
	if got := getPolicy(t, srv, out.KeyMetadata.KeyID); got != policy {
		t.Fatalf("stored policy = %q, want %q", got, policy)
	}
}

func TestCreateKey_bypassSkipsOnlyLockoutSafety(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*testing.T, *helpers.TestServer, string, map[string]any, string) *http.Response
		cbor bool
	}{
		{name: "aws-json", call: kmsCallAs},
		{name: "rpc-v2-cbor", call: kmsCBORCallAs, cbor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a policy that deliberately excludes the caller
			srv := helpers.NewTestServer(t)
			seedIAMCaller(t, srv, "alice", "AKIAALICE")
			bobARN := seedIAMCaller(t, srv, "bob", "")
			policy := callerPolicy(bobARN)

			// When: lockout safety is explicitly bypassed
			resp := tc.call(t, srv, "CreateKey", map[string]any{
				"Policy": policy, "BypassPolicyLockoutSafetyCheck": true,
			}, "AKIAALICE")
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusOK)

			// Then: malformed policy syntax is still rejected even with bypass
			bad := tc.call(t, srv, "CreateKey", map[string]any{
				"Policy": `{`, "BypassPolicyLockoutSafetyCheck": true,
			}, "AKIAALICE")
			assertPolicyError(t, bad, tc.cbor, "")
			if got := keyCount(t, srv); got != 1 {
				t.Fatalf("key count = %d, want only the bypassed key", got)
			}
		})
	}
}

func TestPutKeyPolicy_rejectionDoesNotMutate_protocols(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*testing.T, *helpers.TestServer, string, map[string]any, string) *http.Response
		cbor bool
	}{
		{name: "aws-json", call: kmsCallAs},
		{name: "rpc-v2-cbor", call: kmsCBORCallAs, cbor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a key with a caller-safe policy
			srv := helpers.NewTestServer(t)
			callerARN := seedIAMCaller(t, srv, "alice", "AKIAALICE")
			bobARN := seedIAMCaller(t, srv, "bob", "")
			original := callerPolicy(callerARN)
			create := kmsCallAs(t, srv, "CreateKey", map[string]any{"Policy": original}, "AKIAALICE")
			defer create.Body.Close()
			helpers.AssertStatus(t, create, http.StatusOK)
			var created struct {
				KeyMetadata struct {
					KeyID string `json:"KeyId"`
				} `json:"KeyMetadata"`
			}
			decodeJSON(t, create, &created)
			unsafe := callerPolicy(bobARN)

			// When: the caller tries to replace it with a locking policy
			resp := tc.call(t, srv, "PutKeyPolicy", map[string]any{
				"KeyId": created.KeyMetadata.KeyID, "PolicyName": "default", "Policy": unsafe,
			}, "AKIAALICE")

			// Then: the request fails and the previous policy remains unchanged
			assertPolicyError(t, resp, tc.cbor, lockoutMessage)
			if got := getPolicy(t, srv, created.KeyMetadata.KeyID); got != original {
				t.Fatalf("policy mutated after rejection: got %q, want %q", got, original)
			}
		})
	}
}

func TestPutKeyPolicy_bypassPreservesPolicy_protocols(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*testing.T, *helpers.TestServer, string, map[string]any, string) *http.Response
	}{
		{name: "aws-json", call: kmsCallAs},
		{name: "rpc-v2-cbor", call: kmsCBORCallAs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a key and a caller excluded by the replacement policy
			srv := helpers.NewTestServer(t)
			seedIAMCaller(t, srv, "alice", "AKIAALICE")
			bobARN := seedIAMCaller(t, srv, "bob", "")
			keyID := createKey(t, srv, "put-policy-bypass")
			unsafe := callerPolicy(bobARN)

			// When: the caller explicitly bypasses lockout safety
			resp := tc.call(t, srv, "PutKeyPolicy", map[string]any{
				"KeyId": keyID, "PolicyName": "default", "Policy": unsafe,
				"BypassPolicyLockoutSafetyCheck": true,
			}, "AKIAALICE")
			defer resp.Body.Close()

			// Then: the supplied policy is preserved by either protocol
			helpers.AssertStatus(t, resp, http.StatusOK)
			if got := getPolicy(t, srv, keyID); got != unsafe {
				t.Fatalf("stored policy = %q, want %q", got, unsafe)
			}
		})
	}
}

func TestPutKeyPolicy_acceptsIneffectiveStatementAlongsideCallerGrant(t *testing.T) {
	// Given: a key and a policy containing both a caller grant and an ineffective statement
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "ineffective-statement")
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::%s:root"},"Action":"kms:PutKeyPolicy","Resource":"*"},{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::%s:root"},"Resource":"*"}]}`, srv.Config.AccountID, srv.Config.AccountID)

	// When: PutKeyPolicy receives the document
	resp := kmsCall(t, srv, "PutKeyPolicy", map[string]any{
		"KeyId": keyID, "PolicyName": "default", "Policy": policy,
	})
	defer resp.Body.Close()

	// Then: AWS's API behavior is preserved: the missing-Action statement has no effect but is accepted
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := getPolicy(t, srv, keyID); got != policy {
		t.Fatalf("stored policy = %q, want %q", got, policy)
	}
}

func TestPutKeyPolicy_acceptsPolicyWithoutVersion(t *testing.T) {
	// CDK's standard bootstrap policy omits Version; AWS accepts that shape.
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "no-version")
	policy := fmt.Sprintf(`{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::%s:root"},"Action":"kms:PutKeyPolicy","Resource":"*"}]}`, srv.Config.AccountID)
	resp := kmsCall(t, srv, "PutKeyPolicy", map[string]any{
		"KeyId": keyID, "PolicyName": "default", "Policy": policy,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := getPolicy(t, srv, keyID); got != policy {
		t.Fatalf("stored policy = %q, want %q", got, policy)
	}
}

func TestPutKeyPolicy_emptyStatementListIsMalformed(t *testing.T) {
	// Given: an existing key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "empty-policy")
	original := getPolicy(t, srv, keyID)

	// When: an empty key policy is submitted with bypass enabled
	resp := kmsCall(t, srv, "PutKeyPolicy", map[string]any{
		"KeyId": keyID, "PolicyName": "default",
		"Policy":                         `{"Version":"2012-10-17","Statement":[]}`,
		"BypassPolicyLockoutSafetyCheck": true,
	})

	// Then: schema validation still rejects it without changing the key
	assertPolicyError(t, resp, false, "")
	if got := getPolicy(t, srv, keyID); got != original {
		t.Fatalf("policy mutated after malformed request: got %q, want %q", got, original)
	}
}

func TestPutKeyPolicy_bypassStillRejectsInvalidPrincipal(t *testing.T) {
	// Given: an existing key and a syntactically invalid AWS principal
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "invalid-principal")
	original := getPolicy(t, srv, keyID)
	policy := callerPolicy("not-an-aws-principal")

	// When: policy lockout safety is bypassed
	resp := kmsCall(t, srv, "PutKeyPolicy", map[string]any{
		"KeyId": keyID, "PolicyName": "default", "Policy": policy,
		"BypassPolicyLockoutSafetyCheck": true,
	})

	// Then: principal validation still applies and state is unchanged
	assertPolicyError(t, resp, false, "Policy contains a statement with one or more invalid principals")
	if got := getPolicy(t, srv, keyID); got != original {
		t.Fatalf("policy mutated after invalid principal: got %q, want %q", got, original)
	}
}

func TestPutKeyPolicy_bypassRejectsMissingLocalPrincipal_protocols(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*testing.T, *helpers.TestServer, string, map[string]any, string) *http.Response
		cbor bool
	}{
		{name: "aws-json", call: kmsCallAs},
		{name: "rpc-v2-cbor", call: kmsCBORCallAs, cbor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			keyID := createKey(t, srv, "missing-local-principal")
			original := getPolicy(t, srv, keyID)
			policy := callerPolicy("arn:aws:iam::" + srv.Config.AccountID + ":user/missing")

			resp := tc.call(t, srv, "PutKeyPolicy", map[string]any{
				"KeyId": keyID, "PolicyName": "default", "Policy": policy,
				"BypassPolicyLockoutSafetyCheck": true,
			}, "AKIAUNKNOWN")

			assertPolicyError(t, resp, tc.cbor, "Policy contains a statement with one or more invalid principals")
			if got := getPolicy(t, srv, keyID); got != original {
				t.Fatalf("policy mutated after missing principal: got %q, want %q", got, original)
			}
		})
	}
}

func TestPutKeyPolicy_bypassAcceptsExistingLocalRole(t *testing.T) {
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "existing-role")
	policy := callerPolicy(seedIAMRole(t, srv, "operator"))
	resp := kmsCall(t, srv, "PutKeyPolicy", map[string]any{
		"KeyId": keyID, "PolicyName": "default", "Policy": policy,
		"BypassPolicyLockoutSafetyCheck": true,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestPutKeyPolicy_missingResourceCannotSatisfyLockoutSafety_protocols(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*testing.T, *helpers.TestServer, string, map[string]any, string) *http.Response
		cbor bool
	}{
		{name: "aws-json", call: kmsCallAs},
		{name: "rpc-v2-cbor", call: kmsCBORCallAs, cbor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			callerARN := seedIAMCaller(t, srv, "alice", "AKIAALICE")
			keyID := createKey(t, srv, "missing-resource")
			original := getPolicy(t, srv, keyID)
			policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":%q},"Action":"kms:PutKeyPolicy"}]}`, callerARN)

			resp := tc.call(t, srv, "PutKeyPolicy", map[string]any{
				"KeyId": keyID, "PolicyName": "default", "Policy": policy,
			}, "AKIAALICE")

			assertPolicyError(t, resp, tc.cbor, lockoutMessage)
			if got := getPolicy(t, srv, keyID); got != original {
				t.Fatalf("policy mutated after ineffective grant: got %q, want %q", got, original)
			}
		})
	}
}

func TestPutKeyPolicy_callerAccountCondition_protocols(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*testing.T, *helpers.TestServer, string, map[string]any, string) *http.Response
	}{
		{name: "aws-json", call: kmsCallAs},
		{name: "rpc-v2-cbor", call: kmsCBORCallAs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			seedIAMCaller(t, srv, "alice", "AKIAALICE")
			keyID := createKey(t, srv, "caller-account")
			policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"kms:PutKeyPolicy","Resource":"*","Condition":{"StringEquals":{"kms:CallerAccount":%q}}}]}`, srv.Config.AccountID)
			resp := tc.call(t, srv, "PutKeyPolicy", map[string]any{
				"KeyId": keyID, "PolicyName": "default", "Policy": policy,
			}, "AKIAALICE")
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusOK)
		})
	}
}

func TestPutKeyPolicy_bypassRejectsStrictSchemaWithoutMutation_protocols(t *testing.T) {
	cases := []struct {
		name   string
		policy string
	}{
		{name: "bogus version", policy: `{"Version":"2026-08-06","Statement":[{"Effect":"Allow","Principal":"*","Action":"kms:*","Resource":"*"}]}`},
		{name: "invalid effect", policy: `{"Version":"2012-10-17","Statement":[{"Effect":"Permit","Principal":"*","Action":"kms:*","Resource":"*"}]}`},
		{name: "unsupported principal", policy: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Federated":"example.com"},"Action":"kms:*","Resource":"*"}]}`},
		{name: "unknown principal", policy: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Custom":"value"},"Action":"kms:*","Resource":"*"}]}`},
	}
	for _, protocol := range []struct {
		name string
		call func(*testing.T, *helpers.TestServer, string, map[string]any, string) *http.Response
		cbor bool
	}{
		{name: "aws-json", call: kmsCallAs},
		{name: "rpc-v2-cbor", call: kmsCBORCallAs, cbor: true},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					srv := helpers.NewTestServer(t)
					keyID := createKey(t, srv, "strict-schema")
					original := getPolicy(t, srv, keyID)
					resp := protocol.call(t, srv, "PutKeyPolicy", map[string]any{
						"KeyId": keyID, "PolicyName": "default", "Policy": tc.policy,
						"BypassPolicyLockoutSafetyCheck": true,
					}, "AKIAUNKNOWN")
					assertPolicyError(t, resp, protocol.cbor, "")
					if got := getPolicy(t, srv, keyID); got != original {
						t.Fatalf("policy mutated after %s: got %q, want %q", tc.name, got, original)
					}
				})
			}
		})
	}
}

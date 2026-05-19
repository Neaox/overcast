// Package secretsmanager_test contains integration tests for the Secrets Manager emulator.
//
// TDD contract: every handler in internal/services/secretsmanager/ must have a
// corresponding failing test here before implementation begins.
//
// Run: go test ./tests/integration/secretsmanager/...
package secretsmanager_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// smCall performs a Secrets Manager X-Amz-Target dispatch request.
func smCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager."+operation)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("smCall %s: %v", operation, err)
	}
	return resp
}

// smCBORCall performs a Smithy RPC v2 CBOR dispatch request.
func smCBORCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := cborlib.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR %s body: %v", operation, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/secretsmanager/operation/"+operation, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR request: %v", err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("smCBORCall %s: %v", operation, err)
	}
	return resp
}

// createSecret is a test setup helper that creates a secret and fails the test
// immediately if the call does not succeed.
func createSecret(t *testing.T, srv *helpers.TestServer, name, value string) {
	t.Helper()
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         name,
		"SecretString": value,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── CreateSecret ────────────────────────────────────────────────────────────

func TestCreateSecret_success(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: CreateSecret is called with a valid name and string value
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "prod/db-password",
		"SecretString": `{"username":"admin","password":"s3cr3t"}`,
		"Description":  "database credentials",
	})
	defer resp.Body.Close()

	// Then: 200 with ARN, Name, and VersionId
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ARN       string `json:"ARN"`
		Name      string `json:"Name"`
		VersionId string `json:"VersionId"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.ARN == "" {
		t.Error("expected ARN to be set")
	}
	if result.Name != "prod/db-password" {
		t.Errorf("expected Name='prod/db-password', got %q", result.Name)
	}
	if result.VersionId == "" {
		t.Error("expected VersionId to be set")
	}
}

func TestCreateSecret_missingName(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: CreateSecret is called without Name
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"SecretString": "value",
	})
	defer resp.Body.Close()

	// Then: 400 InvalidParameterException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

func TestCreateSecret_duplicate(t *testing.T) {
	// Given: a secret already exists
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "my-secret", "v1")

	// When: CreateSecret is called again with the same name
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "my-secret",
		"SecretString": "v2",
	})
	defer resp.Body.Close()

	// Then: 400 ResourceExistsException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceExistsException")
}

func TestCreateSecret_withBinary(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: CreateSecret is called with SecretBinary (base64-encoded)
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "binary-secret",
		"SecretBinary": "aGVsbG8gd29ybGQ=", // "hello world" in base64
	})
	defer resp.Body.Close()

	// Then: 200 with ARN
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ARN string `json:"ARN"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.ARN == "" {
		t.Error("expected ARN to be set")
	}
}

func TestCreateSecret_withTags(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: CreateSecret is called with tags
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "tagged-secret",
		"SecretString": "value",
		"Tags": []map[string]string{
			{"Key": "env", "Value": "test"},
			{"Key": "team", "Value": "backend"},
		},
	})
	defer resp.Body.Close()

	// Then: 200 success
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestRPCv2CBOR_CreateAndGetSecretValue(t *testing.T) {
	// Given: the Secrets Manager service is empty.
	srv := helpers.NewTestServer(t)

	// When: CreateSecret is called over Smithy RPC v2 CBOR.
	resp := smCBORCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "cbor-secret",
		"SecretString": `{"token":"abc"}`,
		"Description":  "created over cbor",
	})
	defer resp.Body.Close()

	// Then: Secrets Manager responds with a CBOR create result.
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")
	helpers.AssertHeader(t, resp, "Smithy-Protocol", "rpc-v2-cbor")
	var created struct {
		ARN       string `cbor:"ARN"`
		Name      string `cbor:"Name"`
		VersionId string `cbor:"VersionId"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode CBOR CreateSecret response: %v", err)
	}
	if created.ARN == "" || created.VersionId == "" {
		t.Fatalf("CreateSecret returned ARN=%q VersionId=%q", created.ARN, created.VersionId)
	}
	if created.Name != "cbor-secret" {
		t.Fatalf("Name = %q, want cbor-secret", created.Name)
	}

	// When: GetSecretValue is called over Smithy RPC v2 CBOR.
	resp = smCBORCall(t, srv, "GetSecretValue", map[string]any{
		"SecretId": "cbor-secret",
	})
	defer resp.Body.Close()

	// Then: the current version and secret value are returned as CBOR.
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")
	helpers.AssertHeader(t, resp, "Smithy-Protocol", "rpc-v2-cbor")
	var value struct {
		Name          string   `cbor:"Name"`
		VersionId     string   `cbor:"VersionId"`
		VersionStages []string `cbor:"VersionStages"`
		SecretString  string   `cbor:"SecretString"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&value); err != nil {
		t.Fatalf("decode CBOR GetSecretValue response: %v", err)
	}
	if value.Name != "cbor-secret" || value.VersionId != created.VersionId {
		t.Fatalf("GetSecretValue returned Name=%q VersionId=%q, want cbor-secret/%s", value.Name, value.VersionId, created.VersionId)
	}
	if value.SecretString != `{"token":"abc"}` {
		t.Fatalf("SecretString = %q", value.SecretString)
	}
	if len(value.VersionStages) != 1 || value.VersionStages[0] != "AWSCURRENT" {
		t.Fatalf("VersionStages = %#v, want [AWSCURRENT]", value.VersionStages)
	}
}

func TestRPCv2CBOR_ListSecretsAndBatchGet(t *testing.T) {
	// Given: two secrets exist.
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "cbor-a", "alpha")
	createSecret(t, srv, "cbor-b", "bravo")

	// When: ListSecrets is called over Smithy RPC v2 CBOR.
	resp := smCBORCall(t, srv, "ListSecrets", map[string]any{})
	defer resp.Body.Close()

	// Then: the secret summaries are returned in CBOR.
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")
	var listed struct {
		SecretList []struct {
			Name string `cbor:"Name"`
		} `cbor:"SecretList"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode CBOR ListSecrets response: %v", err)
	}
	if len(listed.SecretList) != 2 || listed.SecretList[0].Name != "cbor-a" || listed.SecretList[1].Name != "cbor-b" {
		t.Fatalf("SecretList = %#v, want cbor-a/cbor-b", listed.SecretList)
	}

	// When: BatchGetSecretValue mixes existing and missing IDs over CBOR.
	resp = smCBORCall(t, srv, "BatchGetSecretValue", map[string]any{
		"SecretIdList": []string{"cbor-a", "missing-cbor"},
	})
	defer resp.Body.Close()

	// Then: partial success and per-secret errors are encoded as CBOR.
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")
	var batch struct {
		SecretValues []struct {
			Name         string `cbor:"Name"`
			SecretString string `cbor:"SecretString"`
		} `cbor:"SecretValues"`
		Errors []struct {
			SecretId  string `cbor:"SecretId"`
			ErrorCode string `cbor:"ErrorCode"`
		} `cbor:"Errors"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&batch); err != nil {
		t.Fatalf("decode CBOR BatchGetSecretValue response: %v", err)
	}
	if len(batch.SecretValues) != 1 || batch.SecretValues[0].Name != "cbor-a" || batch.SecretValues[0].SecretString != "alpha" {
		t.Fatalf("SecretValues = %#v, want cbor-a alpha", batch.SecretValues)
	}
	if len(batch.Errors) != 1 || batch.Errors[0].SecretId != "missing-cbor" || batch.Errors[0].ErrorCode != "ResourceNotFoundException" {
		t.Fatalf("Errors = %#v, want missing-cbor ResourceNotFoundException", batch.Errors)
	}
}

// ─── GetSecretValue ──────────────────────────────────────────────────────────

func TestGetSecretValue_success(t *testing.T) {
	// Given: a secret exists with a string value
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "my-secret", `{"key":"val"}`)

	// When: GetSecretValue is called
	resp := smCall(t, srv, "GetSecretValue", map[string]any{
		"SecretId": "my-secret",
	})
	defer resp.Body.Close()

	// Then: 200 with SecretString, Name, ARN, VersionId
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ARN           string   `json:"ARN"`
		Name          string   `json:"Name"`
		SecretString  string   `json:"SecretString"`
		VersionId     string   `json:"VersionId"`
		VersionStages []string `json:"VersionStages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.SecretString != `{"key":"val"}` {
		t.Errorf("expected SecretString=%q, got %q", `{"key":"val"}`, result.SecretString)
	}
	if result.Name != "my-secret" {
		t.Errorf("expected Name='my-secret', got %q", result.Name)
	}
	if result.VersionId == "" {
		t.Error("expected VersionId to be set")
	}
}

func TestGetSecretValue_notFound(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: GetSecretValue is called for a non-existent secret
	resp := smCall(t, srv, "GetSecretValue", map[string]any{
		"SecretId": "does-not-exist",
	})
	defer resp.Body.Close()

	// Then: 400 ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestGetSecretValue_binary(t *testing.T) {
	// Given: a secret exists with binary data
	srv := helpers.NewTestServer(t)
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "bin-secret",
		"SecretBinary": "aGVsbG8=",
	})
	resp.Body.Close()

	// When: GetSecretValue is called
	resp = smCall(t, srv, "GetSecretValue", map[string]any{
		"SecretId": "bin-secret",
	})
	defer resp.Body.Close()

	// Then: 200 with SecretBinary, no SecretString
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		SecretBinary string `json:"SecretBinary"`
		SecretString string `json:"SecretString"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.SecretBinary != "aGVsbG8=" {
		t.Errorf("expected SecretBinary='aGVsbG8=', got %q", result.SecretBinary)
	}
	if result.SecretString != "" {
		t.Errorf("expected no SecretString, got %q", result.SecretString)
	}
}

// ─── DescribeSecret ──────────────────────────────────────────────────────────

func TestDescribeSecret_success(t *testing.T) {
	// Given: a secret exists with a description and tags
	srv := helpers.NewTestServer(t)
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "described-secret",
		"SecretString": "val",
		"Description":  "my description",
		"Tags": []map[string]string{
			{"Key": "env", "Value": "test"},
		},
	})
	resp.Body.Close()

	// When: DescribeSecret is called
	resp = smCall(t, srv, "DescribeSecret", map[string]any{
		"SecretId": "described-secret",
	})
	defer resp.Body.Close()

	// Then: 200 with metadata (no secret value)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ARN         string `json:"ARN"`
		Name        string `json:"Name"`
		Description string `json:"Description"`
		Tags        []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
		VersionIdsToStages map[string][]string `json:"VersionIdsToStages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Name != "described-secret" {
		t.Errorf("expected Name='described-secret', got %q", result.Name)
	}
	if result.Description != "my description" {
		t.Errorf("expected Description='my description', got %q", result.Description)
	}
	if len(result.Tags) != 1 || result.Tags[0].Key != "env" {
		t.Errorf("expected tags [{env test}], got %v", result.Tags)
	}
}

func TestDescribeSecret_notFound(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: DescribeSecret is called for a non-existent secret
	resp := smCall(t, srv, "DescribeSecret", map[string]any{
		"SecretId": "nope",
	})
	defer resp.Body.Close()

	// Then: 400 ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// ─── PutSecretValue ──────────────────────────────────────────────────────────

func TestPutSecretValue_success(t *testing.T) {
	// Given: a secret exists
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "my-secret", "v1")

	// When: PutSecretValue is called with a new value
	resp := smCall(t, srv, "PutSecretValue", map[string]any{
		"SecretId":     "my-secret",
		"SecretString": "v2",
	})
	defer resp.Body.Close()

	// Then: 200 with new VersionId
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ARN       string `json:"ARN"`
		Name      string `json:"Name"`
		VersionId string `json:"VersionId"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.VersionId == "" {
		t.Error("expected VersionId to be set")
	}
}

func TestPutSecretValue_notFound(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: PutSecretValue is called for a non-existent secret
	resp := smCall(t, srv, "PutSecretValue", map[string]any{
		"SecretId":     "nope",
		"SecretString": "val",
	})
	defer resp.Body.Close()

	// Then: 400 ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestPutSecretValue_createsNewVersion(t *testing.T) {
	// Given: a secret exists with one version
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "versioned", "v1")

	// When: PutSecretValue is called twice
	resp1 := smCall(t, srv, "PutSecretValue", map[string]any{
		"SecretId":     "versioned",
		"SecretString": "v2",
	})
	var r1 struct{ VersionId string }
	helpers.DecodeJSON(t, resp1, &r1)
	resp1.Body.Close()

	resp2 := smCall(t, srv, "PutSecretValue", map[string]any{
		"SecretId":     "versioned",
		"SecretString": "v3",
	})
	var r2 struct{ VersionId string }
	helpers.DecodeJSON(t, resp2, &r2)
	resp2.Body.Close()

	// Then: version IDs must differ
	if r1.VersionId == r2.VersionId {
		t.Error("expected different VersionIds for different PutSecretValue calls")
	}

	// And: GetSecretValue returns the latest
	resp := smCall(t, srv, "GetSecretValue", map[string]any{
		"SecretId": "versioned",
	})
	defer resp.Body.Close()
	var got struct{ SecretString string }
	helpers.DecodeJSON(t, resp, &got)
	if got.SecretString != "v3" {
		t.Errorf("expected latest value='v3', got %q", got.SecretString)
	}
}

// ─── UpdateSecret ────────────────────────────────────────────────────────────

func TestUpdateSecret_description(t *testing.T) {
	// Given: a secret exists
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "up-secret", "val")

	// When: UpdateSecret is called to change the description
	resp := smCall(t, srv, "UpdateSecret", map[string]any{
		"SecretId":    "up-secret",
		"Description": "new desc",
	})
	defer resp.Body.Close()

	// Then: 200 success
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: DescribeSecret shows the updated description
	desc := smCall(t, srv, "DescribeSecret", map[string]any{"SecretId": "up-secret"})
	defer desc.Body.Close()
	var result struct{ Description string }
	helpers.DecodeJSON(t, desc, &result)
	if result.Description != "new desc" {
		t.Errorf("expected description='new desc', got %q", result.Description)
	}
}

func TestUpdateSecret_notFound(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: UpdateSecret is called for a non-existent secret
	resp := smCall(t, srv, "UpdateSecret", map[string]any{
		"SecretId":    "nope",
		"Description": "x",
	})
	defer resp.Body.Close()

	// Then: 400 ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// ─── ListSecrets ─────────────────────────────────────────────────────────────

func TestListSecrets_empty(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: ListSecrets is called
	resp := smCall(t, srv, "ListSecrets", map[string]any{})
	defer resp.Body.Close()

	// Then: 200 with empty SecretList
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		SecretList []any `json:"SecretList"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.SecretList) != 0 {
		t.Errorf("expected empty SecretList, got %d", len(result.SecretList))
	}
}

func TestListSecrets_withSecrets(t *testing.T) {
	// Given: two secrets exist
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "secret-a", "a")
	createSecret(t, srv, "secret-b", "b")

	// When: ListSecrets is called
	resp := smCall(t, srv, "ListSecrets", map[string]any{})
	defer resp.Body.Close()

	// Then: 200 with 2 items (no secret values exposed)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		SecretList []struct {
			Name string `json:"Name"`
			ARN  string `json:"ARN"`
		} `json:"SecretList"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.SecretList) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(result.SecretList))
	}
}

// ─── ListSecretVersionIds ────────────────────────────────────────────────────

func TestListSecretVersionIds_success(t *testing.T) {
	// Given: a secret exists with multiple versions
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "ver-secret", "v1")
	resp := smCall(t, srv, "PutSecretValue", map[string]any{
		"SecretId":     "ver-secret",
		"SecretString": "v2",
	})
	resp.Body.Close()

	// When: ListSecretVersionIds is called
	resp = smCall(t, srv, "ListSecretVersionIds", map[string]any{
		"SecretId": "ver-secret",
	})
	defer resp.Body.Close()

	// Then: 200 with at least 2 versions
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Versions []struct {
			VersionId     string   `json:"VersionId"`
			VersionStages []string `json:"VersionStages"`
		} `json:"Versions"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Versions) < 2 {
		t.Errorf("expected >=2 versions, got %d", len(result.Versions))
	}
}

// ─── TagResource ─────────────────────────────────────────────────────────────

func TestTagResource_success(t *testing.T) {
	// Given: a secret exists
	srv := helpers.NewTestServer(t)
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "tag-me",
		"SecretString": "val",
	})
	var created struct{ ARN string }
	helpers.DecodeJSON(t, resp, &created)
	resp.Body.Close()

	// When: TagResource is called
	resp = smCall(t, srv, "TagResource", map[string]any{
		"SecretId": created.ARN,
		"Tags": []map[string]string{
			{"Key": "env", "Value": "prod"},
		},
	})
	defer resp.Body.Close()

	// Then: 200 success
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: DescribeSecret shows the tag
	desc := smCall(t, srv, "DescribeSecret", map[string]any{"SecretId": "tag-me"})
	defer desc.Body.Close()
	var result struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, desc, &result)
	found := false
	for _, tag := range result.Tags {
		if tag.Key == "env" && tag.Value == "prod" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected tag {env:prod}, got %v", result.Tags)
	}
}

// ─── DeleteSecret ────────────────────────────────────────────────────────────

func TestDeleteSecret_success(t *testing.T) {
	// Given: a secret exists
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "del-secret", "val")

	// When: DeleteSecret is called with ForceDeleteWithoutRecovery
	resp := smCall(t, srv, "DeleteSecret", map[string]any{
		"SecretId":                   "del-secret",
		"ForceDeleteWithoutRecovery": true,
	})
	defer resp.Body.Close()

	// Then: 200 with ARN, Name, DeletionDate
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ARN          string  `json:"ARN"`
		Name         string  `json:"Name"`
		DeletionDate float64 `json:"DeletionDate"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Name != "del-secret" {
		t.Errorf("expected Name='del-secret', got %q", result.Name)
	}

	// And: GetSecretValue returns ResourceNotFoundException
	get := smCall(t, srv, "GetSecretValue", map[string]any{"SecretId": "del-secret"})
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusBadRequest)
	helpers.AssertJSONError(t, get, "ResourceNotFoundException")
}

func TestDeleteSecret_notFound(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: DeleteSecret is called for a non-existent secret
	resp := smCall(t, srv, "DeleteSecret", map[string]any{
		"SecretId":                   "nope",
		"ForceDeleteWithoutRecovery": true,
	})
	defer resp.Body.Close()

	// Then: 400 ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// ─── RotateSecret ────────────────────────────────────────────────────────────

func TestRotateSecret_configOnly(t *testing.T) {
	// Given: a secret exists
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "rotate-me", "val")

	// When: RotateSecret is called without a Lambda ARN (config-only)
	resp := smCall(t, srv, "RotateSecret", map[string]any{
		"SecretId":      "rotate-me",
		"RotationRules": map[string]any{"AutomaticallyAfterDays": 30},
	})
	defer resp.Body.Close()

	// Then: 200 with ARN (rotation config saved, no actual rotation)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ARN  string `json:"ARN"`
		Name string `json:"Name"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.ARN == "" {
		t.Error("expected ARN to be set")
	}
}

// ─── UntagResource ───────────────────────────────────────────────────────────

func TestUntagResource_success(t *testing.T) {
	// Given: a secret exists with two tags
	srv := helpers.NewTestServer(t)
	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "tagged-secret",
		"SecretString": "value",
		"Tags": []map[string]string{
			{"Key": "env", "Value": "prod"},
			{"Key": "team", "Value": "platform"},
		},
	})
	resp.Body.Close()

	// When: UntagResource is called to remove the "team" tag
	resp = smCall(t, srv, "UntagResource", map[string]any{
		"SecretId": "tagged-secret",
		"TagKeys":  []string{"team"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: DescribeSecret shows only the "env" tag remains
	resp2 := smCall(t, srv, "DescribeSecret", map[string]any{
		"SecretId": "tagged-secret",
	})
	defer resp2.Body.Close()
	var result struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp2, &result)
	if len(result.Tags) != 1 || result.Tags[0].Key != "env" {
		t.Errorf("expected only env tag, got %+v", result.Tags)
	}
}

func TestUntagResource_notFound(t *testing.T) {
	// Given: no secrets exist
	srv := helpers.NewTestServer(t)

	// When: UntagResource is called on a non-existent secret
	resp := smCall(t, srv, "UntagResource", map[string]any{
		"SecretId": "no-such-secret",
		"TagKeys":  []string{"key"},
	})
	defer resp.Body.Close()

	// Then: 400 ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// ─── GetRandomPassword ───────────────────────────────────────────────────────

func TestGetRandomPassword_default(t *testing.T) {
	// Given: no setup needed
	srv := helpers.NewTestServer(t)

	// When: GetRandomPassword is called with no parameters
	resp := smCall(t, srv, "GetRandomPassword", map[string]any{})
	defer resp.Body.Close()

	// Then: 200 with a non-empty RandomPassword
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		RandomPassword string `json:"RandomPassword"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.RandomPassword == "" {
		t.Error("expected non-empty RandomPassword")
	}
	// Default length is 32
	if len(result.RandomPassword) != 32 {
		t.Errorf("expected default length 32, got %d", len(result.RandomPassword))
	}
}

func TestGetRandomPassword_withLength(t *testing.T) {
	// Given: no setup needed
	srv := helpers.NewTestServer(t)

	// When: GetRandomPassword is called with PasswordLength=16
	resp := smCall(t, srv, "GetRandomPassword", map[string]any{
		"PasswordLength": 16,
	})
	defer resp.Body.Close()

	// Then: 200 with a password of exactly 16 characters
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		RandomPassword string `json:"RandomPassword"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.RandomPassword) != 16 {
		t.Errorf("expected length 16, got %d", len(result.RandomPassword))
	}
}

// ─── BatchGetSecretValue ─────────────────────────────────────────────────────

func TestBatchGetSecretValue_success(t *testing.T) {
	// Given: two secrets exist
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "batch-secret-1", "val1")
	createSecret(t, srv, "batch-secret-2", "val2")

	// When: BatchGetSecretValue is called with both names
	resp := smCall(t, srv, "BatchGetSecretValue", map[string]any{
		"SecretIdList": []string{"batch-secret-1", "batch-secret-2"},
	})
	defer resp.Body.Close()

	// Then: 200 with both secrets returned, no errors
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		SecretValues []struct {
			Name         string `json:"Name"`
			SecretString string `json:"SecretString"`
		} `json:"SecretValues"`
		Errors []any `json:"Errors"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.SecretValues) != 2 {
		t.Errorf("expected 2 SecretValues, got %d", len(result.SecretValues))
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no Errors, got %d", len(result.Errors))
	}
}

func TestBatchGetSecretValue_partialMiss(t *testing.T) {
	// Given: only one secret exists
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "exists-secret", "value")

	// When: BatchGetSecretValue is called, one ID is valid, one is not
	resp := smCall(t, srv, "BatchGetSecretValue", map[string]any{
		"SecretIdList": []string{"exists-secret", "missing-secret"},
	})
	defer resp.Body.Close()

	// Then: 200 with one SecretValue and one Error entry
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		SecretValues []struct {
			Name string `json:"Name"`
		} `json:"SecretValues"`
		Errors []struct {
			SecretId  string `json:"SecretId"`
			ErrorCode string `json:"ErrorCode"`
		} `json:"Errors"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.SecretValues) != 1 {
		t.Errorf("expected 1 SecretValue, got %d", len(result.SecretValues))
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 Error, got %d", len(result.Errors))
	}
	if result.Errors[0].ErrorCode == "" {
		t.Error("expected non-empty ErrorCode")
	}
}

// ─── CancelRotateSecret ─────────────────────────────────────────────────────

func TestCancelRotateSecret_success(t *testing.T) {
	// Given: a secret exists with rotation configured
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "cancel-rot", "val")
	resp := smCall(t, srv, "RotateSecret", map[string]any{
		"SecretId":      "cancel-rot",
		"RotationRules": map[string]any{"AutomaticallyAfterDays": 7},
	})
	resp.Body.Close()

	// When: CancelRotateSecret is called
	resp = smCall(t, srv, "CancelRotateSecret", map[string]any{
		"SecretId": "cancel-rot",
	})
	defer resp.Body.Close()

	// Then: 200 with ARN, Name
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ARN  string `json:"ARN"`
		Name string `json:"Name"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Name != "cancel-rot" {
		t.Errorf("expected Name='cancel-rot', got %q", result.Name)
	}
}

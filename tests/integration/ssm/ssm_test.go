// Package ssm_test contains integration tests for the SSM Parameter Store emulator.
//
// Run: go test ./tests/integration/ssm/...
package ssm_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

func ssmCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AmazonSSM."+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s: %v", action, err)
	}
	return resp
}

func ssmCBORCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := cborlib.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/ssm/operation/"+action, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR request: %v", err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do CBOR request %s: %v", action, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("decodeJSON: read: %v", err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("decodeJSON: unmarshal: %v\nbody: %s", err, b)
	}
}

func putParam(t *testing.T, srv *helpers.TestServer, name, value, typ string, overwrite bool) {
	t.Helper()
	resp := ssmCall(t, srv, "PutParameter", map[string]any{
		"Name": name, "Value": value, "Type": typ, "Overwrite": overwrite,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func putParamCBOR(t *testing.T, srv *helpers.TestServer, name, value, typ string, overwrite bool) {
	t.Helper()
	resp := ssmCBORCall(t, srv, "PutParameter", map[string]any{
		"Name": name, "Value": value, "Type": typ, "Overwrite": overwrite,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── PutParameter ────────────────────────────────────────────────────────────

func TestPutParameter_success(t *testing.T) {
	// Given: an SSM service
	srv := helpers.NewTestServer(t)

	// When: putting a parameter
	resp := ssmCall(t, srv, "PutParameter", map[string]any{
		"Name": "/test/db/host", "Value": "db.example.com", "Type": "String", "Overwrite": false,
	})
	defer resp.Body.Close()

	// Then: returns Version 1
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["Version"].(float64) != 1 {
		t.Errorf("expected Version=1, got %v", result["Version"])
	}
}

func TestPutParameter_alreadyExists_noOverwrite(t *testing.T) {
	// Given: a parameter that already exists
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/test/key", "v1", "String", false)

	// When: trying to put again without Overwrite
	resp := ssmCall(t, srv, "PutParameter", map[string]any{
		"Name": "/test/key", "Value": "v2", "Type": "String", "Overwrite": false,
	})
	defer resp.Body.Close()

	// Then: 400 ParameterAlreadyExists
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestPutParameter_overwrite_incrementsVersion(t *testing.T) {
	// Given: an existing parameter
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/test/key", "v1", "String", false)

	// When: overwriting
	resp := ssmCall(t, srv, "PutParameter", map[string]any{
		"Name": "/test/key", "Value": "v2", "Type": "String", "Overwrite": true,
	})
	defer resp.Body.Close()

	// Then: returns Version 2
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	decodeJSON(t, resp, &result)
	if result["Version"].(float64) != 2 {
		t.Errorf("expected Version=2, got %v", result["Version"])
	}
}

func TestRPCv2CBOR_PutGetAndGetParameters(t *testing.T) {
	// Given: an SSM service.
	srv := helpers.NewTestServer(t)

	// When: PutParameter is called over Smithy RPC v2 CBOR.
	resp := ssmCBORCall(t, srv, "PutParameter", map[string]any{
		"Name": "/cbor/db/host", "Value": "db.example.com", "Type": "String",
	})
	defer resp.Body.Close()

	// Then: SSM returns a CBOR version response.
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")
	helpers.AssertHeader(t, resp, "Smithy-Protocol", "rpc-v2-cbor")
	var putOut struct {
		Version int64  `cbor:"Version"`
		Tier    string `cbor:"Tier"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&putOut); err != nil {
		t.Fatalf("decode CBOR PutParameter response: %v", err)
	}
	if putOut.Version != 1 || putOut.Tier != "Standard" {
		t.Fatalf("PutParameter = %+v, want version 1 standard", putOut)
	}

	// When: GetParameter is called over CBOR.
	resp = ssmCBORCall(t, srv, "GetParameter", map[string]any{"Name": "/cbor/db/host"})
	defer resp.Body.Close()

	// Then: the parameter value is returned with SSM member names.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var getOut struct {
		Parameter struct {
			Name  string `cbor:"Name"`
			Type  string `cbor:"Type"`
			Value string `cbor:"Value"`
		} `cbor:"Parameter"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&getOut); err != nil {
		t.Fatalf("decode CBOR GetParameter response: %v", err)
	}
	if getOut.Parameter.Name != "/cbor/db/host" || getOut.Parameter.Value != "db.example.com" {
		t.Fatalf("GetParameter = %+v", getOut.Parameter)
	}

	putParamCBOR(t, srv, "/cbor/db/port", "5432", "String", false)
	resp = ssmCBORCall(t, srv, "GetParameters", map[string]any{
		"Names": []string{"/cbor/db/host", "/missing"},
	})
	defer resp.Body.Close()

	// Then: found and invalid parameters are split in the CBOR response.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var many struct {
		Parameters []struct {
			Name string `cbor:"Name"`
		} `cbor:"Parameters"`
		InvalidParameters []string `cbor:"InvalidParameters"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&many); err != nil {
		t.Fatalf("decode CBOR GetParameters response: %v", err)
	}
	if len(many.Parameters) != 1 || many.Parameters[0].Name != "/cbor/db/host" {
		t.Fatalf("Parameters = %#v, want /cbor/db/host", many.Parameters)
	}
	if len(many.InvalidParameters) != 1 || many.InvalidParameters[0] != "/missing" {
		t.Fatalf("InvalidParameters = %#v, want /missing", many.InvalidParameters)
	}
}

// ─── GetParameter ────────────────────────────────────────────────────────────

func TestGetParameter_success(t *testing.T) {
	// Given: a stored parameter
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/test/db/host", "db.example.com", "String", false)

	// When: getting it
	resp := ssmCall(t, srv, "GetParameter", map[string]any{"Name": "/test/db/host"})
	defer resp.Body.Close()

	// Then: returns correct value
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameter struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
			Type  string `json:"Type"`
		} `json:"Parameter"`
	}
	decodeJSON(t, resp, &result)
	if result.Parameter.Value != "db.example.com" {
		t.Errorf("expected db.example.com, got %q", result.Parameter.Value)
	}
}

func TestGetParameter_notFound(t *testing.T) {
	// Given: empty SSM
	srv := helpers.NewTestServer(t)

	// When: getting a non-existent parameter
	resp := ssmCall(t, srv, "GetParameter", map[string]any{"Name": "/nonexistent"})
	defer resp.Body.Close()

	// Then: 400 ParameterNotFound
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── SecureString ────────────────────────────────────────────────────────────

func TestGetParameter_secureString_withDecryption(t *testing.T) {
	// Given: a SecureString parameter
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/test/secret", "super-secret", "SecureString", false)

	// When: getting with WithDecryption=true
	resp := ssmCall(t, srv, "GetParameter", map[string]any{
		"Name": "/test/secret", "WithDecryption": true,
	})
	defer resp.Body.Close()

	// Then: returns plaintext value
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameter struct {
			Value string `json:"Value"`
		} `json:"Parameter"`
	}
	decodeJSON(t, resp, &result)
	if result.Parameter.Value != "super-secret" {
		t.Errorf("expected plaintext, got %q", result.Parameter.Value)
	}
}

func TestGetParameter_secureString_withoutDecryption(t *testing.T) {
	// Given: a SecureString parameter
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/test/secret", "super-secret", "SecureString", false)

	// When: getting with WithDecryption=false
	resp := ssmCall(t, srv, "GetParameter", map[string]any{
		"Name": "/test/secret", "WithDecryption": false,
	})
	defer resp.Body.Close()

	// Then: does NOT return plaintext
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameter struct {
			Value string `json:"Value"`
		} `json:"Parameter"`
	}
	decodeJSON(t, resp, &result)
	if result.Parameter.Value == "super-secret" {
		t.Error("expected masked value, got plaintext")
	}
}

func TestRPCv2CBOR_SecureStringMaskAndPath(t *testing.T) {
	// Given: a SecureString and path parameters exist.
	srv := helpers.NewTestServer(t)
	putParamCBOR(t, srv, "/cbor/secret", "super-secret", "SecureString", false)
	putParamCBOR(t, srv, "/cbor/app/db/host", "h", "String", false)
	putParamCBOR(t, srv, "/cbor/app/db/port", "5432", "String", false)

	// When: GetParameter is called without decryption over CBOR.
	resp := ssmCBORCall(t, srv, "GetParameter", map[string]any{"Name": "/cbor/secret"})
	defer resp.Body.Close()

	// Then: SecureString is masked.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var masked struct {
		Parameter struct {
			Value string `cbor:"Value"`
		} `cbor:"Parameter"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&masked); err != nil {
		t.Fatalf("decode CBOR GetParameter response: %v", err)
	}
	if masked.Parameter.Value == "super-secret" {
		t.Fatal("expected SecureString to be masked without decryption")
	}

	// When: GetParametersByPath is called over CBOR.
	resp = ssmCBORCall(t, srv, "GetParametersByPath", map[string]any{
		"Path": "/cbor/app/db", "Recursive": false, "MaxResults": 1,
	})
	defer resp.Body.Close()

	// Then: pagination is preserved.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var page struct {
		Parameters []struct {
			Name string `cbor:"Name"`
		} `cbor:"Parameters"`
		NextToken string `cbor:"NextToken"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode CBOR GetParametersByPath response: %v", err)
	}
	if len(page.Parameters) != 1 || page.NextToken == "" {
		t.Fatalf("page = %#v, want one parameter and NextToken", page)
	}
}

// ─── GetParameters ───────────────────────────────────────────────────────────

func TestGetParameters_success(t *testing.T) {
	// Given: two parameters
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/test/a", "val-a", "String", false)
	putParam(t, srv, "/test/b", "val-b", "String", false)

	// When: getting both
	resp := ssmCall(t, srv, "GetParameters", map[string]any{
		"Names": []string{"/test/a", "/test/b"},
	})
	defer resp.Body.Close()

	// Then: returns both
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameters        []map[string]any `json:"Parameters"`
		InvalidParameters []string         `json:"InvalidParameters"`
	}
	decodeJSON(t, resp, &result)
	if len(result.Parameters) != 2 {
		t.Errorf("expected 2 params, got %d", len(result.Parameters))
	}
}

func TestGetParameters_invalidNames(t *testing.T) {
	// Given: empty SSM
	srv := helpers.NewTestServer(t)

	// When: requesting non-existent parameters
	resp := ssmCall(t, srv, "GetParameters", map[string]any{
		"Names": []string{"/missing/a", "/missing/b"},
	})
	defer resp.Body.Close()

	// Then: returns empty Parameters array + invalid names
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameters        []map[string]any `json:"Parameters"`
		InvalidParameters []string         `json:"InvalidParameters"`
	}
	decodeJSON(t, resp, &result)
	if len(result.InvalidParameters) != 2 {
		t.Errorf("expected 2 invalid params, got %d", len(result.InvalidParameters))
	}
}

// ─── GetParametersByPath ─────────────────────────────────────────────────────

func TestGetParametersByPath_nonRecursive(t *testing.T) {
	// Given: parameters under /app/db/
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/app/db/host", "h", "String", false)
	putParam(t, srv, "/app/db/port", "5432", "String", false)
	putParam(t, srv, "/app/db/user", "admin", "String", false)

	// When: GetParametersByPath with Recursive=false
	resp := ssmCall(t, srv, "GetParametersByPath", map[string]any{
		"Path": "/app/db", "Recursive": false,
	})
	defer resp.Body.Close()

	// Then: returns 3 direct children
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameters []map[string]any `json:"Parameters"`
	}
	decodeJSON(t, resp, &result)
	if len(result.Parameters) != 3 {
		t.Errorf("expected 3 params, got %d", len(result.Parameters))
	}
}

func TestGetParametersByPath_recursive(t *testing.T) {
	// Given: parameters under multiple sub-paths
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/app/db/host", "h", "String", false)
	putParam(t, srv, "/app/db/port", "5432", "String", false)
	putParam(t, srv, "/app/cache/host", "c", "String", false)

	// When: GetParametersByPath with Recursive=true
	resp := ssmCall(t, srv, "GetParametersByPath", map[string]any{
		"Path": "/app", "Recursive": true,
	})
	defer resp.Body.Close()

	// Then: returns all 3
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameters []map[string]any `json:"Parameters"`
	}
	decodeJSON(t, resp, &result)
	if len(result.Parameters) < 3 {
		t.Errorf("expected ≥3 params, got %d", len(result.Parameters))
	}
}

func TestGetParametersByPath_pagination(t *testing.T) {
	// Given: 3 parameters
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/pg/a", "1", "String", false)
	putParam(t, srv, "/pg/b", "2", "String", false)
	putParam(t, srv, "/pg/c", "3", "String", false)

	// When: page1 with MaxResults=2
	resp1 := ssmCall(t, srv, "GetParametersByPath", map[string]any{
		"Path": "/pg", "Recursive": false, "MaxResults": 2,
	})
	defer resp1.Body.Close()
	helpers.AssertStatus(t, resp1, http.StatusOK)

	var page1 struct {
		Parameters []map[string]any `json:"Parameters"`
		NextToken  string           `json:"NextToken"`
	}
	decodeJSON(t, resp1, &page1)
	if len(page1.Parameters) != 2 {
		t.Errorf("page1: expected 2, got %d", len(page1.Parameters))
	}
	if page1.NextToken == "" {
		t.Error("page1: expected NextToken")
	}

	// When: page2 using NextToken
	resp2 := ssmCall(t, srv, "GetParametersByPath", map[string]any{
		"Path": "/pg", "Recursive": false, "MaxResults": 2, "NextToken": page1.NextToken,
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var page2 struct {
		Parameters []map[string]any `json:"Parameters"`
	}
	decodeJSON(t, resp2, &page2)
	if len(page2.Parameters) != 1 {
		t.Errorf("page2: expected 1, got %d", len(page2.Parameters))
	}
}

// ─── DescribeParameters ──────────────────────────────────────────────────────

func TestDescribeParameters_filterBeginsWith(t *testing.T) {
	// Given: parameters with a common prefix
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/db/host", "h", "String", false)
	putParam(t, srv, "/db/port", "5432", "String", false)
	putParam(t, srv, "/cache/host", "c", "String", false)

	// When: DescribeParameters with BeginsWith filter
	resp := ssmCall(t, srv, "DescribeParameters", map[string]any{
		"ParameterFilters": []map[string]any{
			{"Key": "Name", "Option": "BeginsWith", "Values": []string{"/db"}},
		},
	})
	defer resp.Body.Close()

	// Then: returns only /db parameters
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameters []map[string]any `json:"Parameters"`
	}
	decodeJSON(t, resp, &result)
	if len(result.Parameters) != 2 {
		t.Errorf("expected 2 /db params, got %d", len(result.Parameters))
	}
}

func TestRPCv2CBOR_DescribeHistoryTagsAndDelete(t *testing.T) {
	// Given: parameters with history and tags exist.
	srv := helpers.NewTestServer(t)
	putParamCBOR(t, srv, "/cbor/hist/key", "v1", "String", false)
	putParamCBOR(t, srv, "/cbor/hist/key", "v2", "String", true)
	putParamCBOR(t, srv, "/cbor/hist/other", "v", "String", false)

	// When: DescribeParameters filters by name prefix over CBOR.
	resp := ssmCBORCall(t, srv, "DescribeParameters", map[string]any{
		"ParameterFilters": []map[string]any{
			{"Key": "Name", "Option": "BeginsWith", "Values": []string{"/cbor/hist"}},
		},
	})
	defer resp.Body.Close()

	// Then: matching metadata is returned.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var described struct {
		Parameters []struct {
			Name string `cbor:"Name"`
			Tier string `cbor:"Tier"`
		} `cbor:"Parameters"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&described); err != nil {
		t.Fatalf("decode CBOR DescribeParameters response: %v", err)
	}
	if len(described.Parameters) != 2 {
		t.Fatalf("DescribeParameters returned %d params, want 2", len(described.Parameters))
	}

	// When: history is requested over CBOR.
	resp = ssmCBORCall(t, srv, "GetParameterHistory", map[string]any{"Name": "/cbor/hist/key"})
	defer resp.Body.Close()

	// Then: both versions are returned.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var hist struct {
		Parameters []struct {
			Value   string `cbor:"Value"`
			Version int64  `cbor:"Version"`
		} `cbor:"Parameters"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&hist); err != nil {
		t.Fatalf("decode CBOR GetParameterHistory response: %v", err)
	}
	if len(hist.Parameters) != 2 || hist.Parameters[1].Value != "v2" || hist.Parameters[1].Version != 2 {
		t.Fatalf("history = %#v, want v1/v2", hist.Parameters)
	}

	// When: tags are added and listed over CBOR.
	resp = ssmCBORCall(t, srv, "AddTagsToResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/cbor/hist/key",
		"Tags":         []map[string]string{{"Key": "env", "Value": "test"}},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	resp = ssmCBORCall(t, srv, "ListTagsForResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/cbor/hist/key",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var tags struct {
		TagList []struct {
			Key   string `cbor:"Key"`
			Value string `cbor:"Value"`
		} `cbor:"TagList"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&tags); err != nil {
		t.Fatalf("decode CBOR ListTagsForResource response: %v", err)
	}
	if len(tags.TagList) != 1 || tags.TagList[0].Key != "env" || tags.TagList[0].Value != "test" {
		t.Fatalf("TagList = %#v, want env=test", tags.TagList)
	}

	// When: DeleteParameters mixes existing and missing names over CBOR.
	resp = ssmCBORCall(t, srv, "DeleteParameters", map[string]any{
		"Names": []string{"/cbor/hist/key", "/cbor/missing"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var deleted struct {
		DeletedParameters []string `cbor:"DeletedParameters"`
		InvalidParameters []string `cbor:"InvalidParameters"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&deleted); err != nil {
		t.Fatalf("decode CBOR DeleteParameters response: %v", err)
	}
	if len(deleted.DeletedParameters) != 1 || deleted.DeletedParameters[0] != "/cbor/hist/key" {
		t.Fatalf("DeletedParameters = %#v", deleted.DeletedParameters)
	}
	if len(deleted.InvalidParameters) != 1 || deleted.InvalidParameters[0] != "/cbor/missing" {
		t.Fatalf("InvalidParameters = %#v", deleted.InvalidParameters)
	}
}

// ─── GetParameterHistory ─────────────────────────────────────────────────────

func TestGetParameterHistory_multipleVersions(t *testing.T) {
	// Given: a parameter with 2 versions
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/hist/key", "v1", "String", false)
	putParam(t, srv, "/hist/key", "v2", "String", true)

	// When: GetParameterHistory
	resp := ssmCall(t, srv, "GetParameterHistory", map[string]any{"Name": "/hist/key"})
	defer resp.Body.Close()

	// Then: returns 2 versions
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Parameters []map[string]any `json:"Parameters"`
	}
	decodeJSON(t, resp, &result)
	if len(result.Parameters) < 2 {
		t.Errorf("expected ≥2 versions, got %d", len(result.Parameters))
	}
}

// ─── AddTagsToResource / ListTagsForResource ─────────────────────────────────

func TestAddAndListTagsForResource(t *testing.T) {
	// Given: a stored parameter
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/tagged/key", "val", "String", false)

	// When: adding a tag
	addResp := ssmCall(t, srv, "AddTagsToResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/tagged/key",
		"Tags":         []map[string]string{{"Key": "env", "Value": "test"}},
	})
	defer addResp.Body.Close()
	helpers.AssertStatus(t, addResp, http.StatusOK)

	// When: listing tags
	listResp := ssmCall(t, srv, "ListTagsForResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/tagged/key",
	})
	defer listResp.Body.Close()
	helpers.AssertStatus(t, listResp, http.StatusOK)

	// Then: tag is present
	var result struct {
		TagList []map[string]string `json:"TagList"`
	}
	decodeJSON(t, listResp, &result)
	found := false
	for _, tag := range result.TagList {
		if tag["Key"] == "env" {
			found = true
		}
	}
	if !found {
		t.Error("expected env tag to be present")
	}
}

// ─── DeleteParameter / DeleteParameters ──────────────────────────────────────

func TestDeleteParameter_success(t *testing.T) {
	// Given: a parameter
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/del/single", "v", "String", false)

	// When: deleting it
	resp := ssmCall(t, srv, "DeleteParameter", map[string]any{"Name": "/del/single"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: getting it returns not found
	getResp := ssmCall(t, srv, "GetParameter", map[string]any{"Name": "/del/single"})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusBadRequest)
}

func TestDeleteParameters_batch(t *testing.T) {
	// Given: 3 parameters
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/batch/a", "1", "String", false)
	putParam(t, srv, "/batch/b", "2", "String", false)
	putParam(t, srv, "/batch/c", "3", "String", false)

	// When: deleting all 3
	resp := ssmCall(t, srv, "DeleteParameters", map[string]any{
		"Names": []string{"/batch/a", "/batch/b", "/batch/c"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: returns 3 deleted
	var result struct {
		DeletedParameters []string `json:"DeletedParameters"`
		InvalidParameters []string `json:"InvalidParameters"`
	}
	decodeJSON(t, resp, &result)
	if len(result.DeletedParameters) != 3 {
		t.Errorf("expected 3 deleted, got %d", len(result.DeletedParameters))
	}
}

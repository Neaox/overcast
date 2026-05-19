package dynamodb_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestProtocolDispatchTypedOperation_ListTables(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "ListTables", map[string]any{})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("X-Amz-Crc32"); got == "" {
		t.Fatal("expected X-Amz-Crc32 header")
	}
	var out struct {
		TableNames []string `json:"TableNames"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if out.TableNames == nil {
		t.Fatalf("TableNames = nil, want empty list")
	}
}

func TestRPCv2CBOR_DynamoDBListTables(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := dynamodbCBORCall(t, srv, "ListTables", map[string]any{})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("Content-Type"); got != "application/cbor" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("X-Amz-Crc32"); got == "" {
		t.Fatal("expected X-Amz-Crc32 header")
	}
	var out struct {
		TableNames []string `cbor:"TableNames"`
	}
	if err := cbor.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode CBOR response: %v", err)
	}
	if out.TableNames == nil {
		t.Fatalf("TableNames = nil, want empty list")
	}
}

func dynamodbCBORCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := cbor.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/DynamoDB/operation/"+operation, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR request: %v", err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do CBOR request: %v", err)
	}
	return resp
}

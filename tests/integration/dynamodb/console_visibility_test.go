package dynamodb_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// Console visibility of CLI-created tables.
//
// A report had the web console's DynamoDB page answering ListTables = [] for
// tables the AWS CLI had just created against the same endpoint, and an audit
// pinned it on an account split: the CLI's SigV4 credential scope resolving to
// one account partition, the console's requests to another. There is no such
// partition — nothing in the store keys on the access key, and the console
// signs its own requests anyway — and the tests here pin that down so the
// diagnosis cannot become true by accident. What the report actually
// described was a region mismatch (the CLI in AWS_REGION, the console in the
// server default), which the region preflight explains; its DynamoDB kind is
// covered at the bottom.

func jsonBody(t *testing.T, body map[string]any) io.Reader {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewReader(b)
}

// signedAs builds a SigV4 Authorization header with the given access key and
// credential-scope region. The signature itself is not verified (validation is
// off in TestServer), which is also the production default.
func signedAs(accessKey, region string) string {
	return "AWS4-HMAC-SHA256 Credential=" + accessKey + "/20250101/" + region +
		"/dynamodb/aws4_request, SignedHeaders=host;x-amz-date, Signature=fake"
}

// listTablesWith issues ListTables with exactly the headers given, the way a
// particular client would.
func listTablesWith(t *testing.T, srv *helpers.TestServer, headers map[string]string) []string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", jsonBody(t, map[string]any{}))
	if err != nil {
		t.Fatalf("build ListTables request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		TableNames []string `json:"TableNames"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode ListTables: %v", err)
	}
	return out.TableNames
}

// createTableSignedAs creates a hash-only table the way a CLI with the given
// credentials and region would.
func createTableSignedAs(t *testing.T, srv *helpers.TestServer, accessKey, region, name string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", jsonBody(t, map[string]any{
		"TableName":            name,
		"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
		"BillingMode":          "PAY_PER_REQUEST",
	}))
	if err != nil {
		t.Fatalf("build CreateTable request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.CreateTable")
	req.Header.Set("X-Amz-Date", "20250101T000000Z")
	req.Header.Set("Authorization", signedAs(accessKey, region))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CreateTable %q: %v", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTable %q: status %d: %s", name, resp.StatusCode, helpers.ReadBody(t, resp))
	}
}

// Given: tables created by two differently-credentialled signed clients — a
// long-term key and an STS-style temporary one — in the default region.
// When: the console lists them, in each of the shapes its requests take.
// Then: every shape sees both tables; credentials are not a partition key.
func TestListTables_consoleSeesTablesCreatedBySignedClients(t *testing.T) {
	srv := helpers.NewTestServer(t)
	region := srv.Config.Region
	createTableSignedAs(t, srv, "AKIAIOSFODNN7EXAMPLE", region, "from-long-term-key")
	createTableSignedAs(t, srv, "ASIAIOSFODNN7EXAMPLE", region, "from-session-key")
	want := []string{"from-long-term-key", "from-session-key"}

	consoleShapes := map[string]map[string]string{
		// The browser SDK: signed with the console's own fixed credentials.
		"browser SDK, signed as overcast": {
			"Authorization": signedAs("overcast", region),
			"X-Amz-Date":    "20250101T000000Z",
		},
		// The BFF: unsigned, the region forwarded as a header.
		"BFF, unsigned with X-Overcast-Region": {"X-Overcast-Region": region},
		// A bare unsigned request, which falls back to the default region.
		"unsigned, no region evidence at all": {},
	}
	for name, headers := range consoleShapes {
		t.Run(name, func(t *testing.T) {
			got := listTablesWith(t, srv, headers)
			if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
				t.Fatalf("expected %v, got %v", want, got)
			}
		})
	}
}

// Given: a table created in ap-southeast-2 (what a CLI does when AWS_REGION
// says so) and nothing in us-east-1 (where the console starts).
// When: the console's DynamoDB page, empty, asks the region preflight.
// Then: it is told where the tables are — this is the report's real symptom.
func TestPreflightRegion_explainsAnEmptyDynamoDBTableList(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableSignedAs(t, srv, "AKIAIOSFODNN7EXAMPLE", "ap-southeast-2", "orders")
	createTableSignedAs(t, srv, "AKIAIOSFODNN7EXAMPLE", "ap-southeast-2", "payments")

	if got := listTablesWith(t, srv, map[string]string{"X-Overcast-Region": "us-east-1"}); len(got) != 0 {
		t.Fatalf("expected us-east-1 to be empty, got %v", got)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/_overcast/preflight/region?kind=dynamodb-tables", nil)
	if err != nil {
		t.Fatalf("build preflight request: %v", err)
	}
	req.Header.Set("X-Overcast-Region", "us-east-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var advisory struct {
		Kind      string `json:"kind"`
		Region    string `json:"region"`
		Count     int    `json:"count"`
		Elsewhere []struct {
			Region string `json:"region"`
			Count  int    `json:"count"`
		} `json:"elsewhere"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&advisory); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	if advisory.Kind != "dynamodb-tables" || advisory.Region != "us-east-1" || advisory.Count != 0 {
		t.Fatalf("unexpected advisory header: %+v", advisory)
	}
	if len(advisory.Elsewhere) != 1 || advisory.Elsewhere[0].Region != "ap-southeast-2" || advisory.Elsewhere[0].Count != 2 {
		t.Fatalf("expected ap-southeast-2 with 2 tables, got %+v", advisory.Elsewhere)
	}
}

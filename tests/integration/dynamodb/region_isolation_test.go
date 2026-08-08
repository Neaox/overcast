package dynamodb_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// Region isolation — issue #673.
//
// On AWS a DynamoDB table is a regional resource: a table created in
// us-east-1 is invisible from eu-west-1, the same table name can exist
// independently in both, and each table has its own stream. Overcast keyed
// table *metadata* by region from the start, but item rows, GSI index entries
// and stream records were keyed by table name alone, so two same-named tables
// in different regions read and wrote one another's data.
//
// Every call below carries a SigV4 Authorization header whose credential scope
// names the region (the same mechanism a real SDK client uses, and the same one
// TestExecuteGraphQL_hostBasedInvokeInNonDefaultRegion relies on for AppSync).

const (
	regionA = "us-east-1"
	regionB = "eu-west-1"
)

// ddbSigV4In builds a credential-scoped Authorization header so the request
// lands in region's partition of the store.
func ddbSigV4In(region, service string) string {
	return "AWS4-HMAC-SHA256 Credential=test/20250101/" + region +
		"/" + service + "/aws4_request, SignedHeaders=host, Signature=fake"
}

// ddbCallIn is ddbCall pinned to a region.
func ddbCallIn(t *testing.T, srv *helpers.TestServer, region, operation string, body map[string]any) *http.Response {
	t.Helper()
	return targetCallIn(t, srv, region, "dynamodb", "DynamoDB_20120810."+operation, body)
}

// streamsCallIn is streamsCall pinned to a region.
func streamsCallIn(t *testing.T, srv *helpers.TestServer, region, operation string, body map[string]any) *http.Response {
	t.Helper()
	return targetCallIn(t, srv, region, "dynamodb", "DynamoDBStreams_20120810."+operation, body)
}

func targetCallIn(t *testing.T, srv *helpers.TestServer, region, service, target string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", target, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build %s request: %v", target, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Authorization", ddbSigV4In(region, service))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s in %s: %v", target, region, err)
	}
	return resp
}

// createTableIn creates a hash-only table named name in region.
func createTableIn(t *testing.T, srv *helpers.TestServer, region, name string) {
	t.Helper()
	resp := ddbCallIn(t, srv, region, "CreateTable", map[string]any{
		"TableName":            name,
		"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
		"BillingMode":          "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateTable %q in %s: status %d: %s", name, region, resp.StatusCode, helpers.ReadBody(t, resp))
	}
}

// putItemIn writes {id, tag} into name in region.
func putItemIn(t *testing.T, srv *helpers.TestServer, region, name, id, tag string) {
	t.Helper()
	resp := ddbCallIn(t, srv, region, "PutItem", map[string]any{
		"TableName": name,
		"Item": map[string]any{
			"id":  map[string]any{"S": id},
			"tag": map[string]any{"S": tag},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PutItem %q/%q in %s: status %d: %s", name, id, region, resp.StatusCode, helpers.ReadBody(t, resp))
	}
}

// listTableNamesIn returns ListTables' TableNames for region.
func listTableNamesIn(t *testing.T, srv *helpers.TestServer, region string) []string {
	t.Helper()
	resp := ddbCallIn(t, srv, region, "ListTables", map[string]any{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		TableNames []string `json:"TableNames"`
	}
	helpers.DecodeJSON(t, resp, &out)
	return out.TableNames
}

// scanIDsIn returns the "id" values Scan reports for name in region.
func scanIDsIn(t *testing.T, srv *helpers.TestServer, region, name string) []string {
	t.Helper()
	resp := ddbCallIn(t, srv, region, "Scan", map[string]any{"TableName": name})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Items []map[string]map[string]any `json:"Items"`
		Count int                         `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		if v, ok := item["id"]["S"].(string); ok {
			ids = append(ids, v)
		}
	}
	return ids
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", what, got, want)
		}
	}
}

// ---- Table metadata --------------------------------------------------------

// TestRegionIsolation_TableInvisibleInOtherRegion is the baseline guard: a
// table created in one region must not be described or listed in another.
func TestRegionIsolation_TableInvisibleInOtherRegion(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Given: a table created only in us-east-1
	createTableIn(t, srv, regionA, "orders")

	// Then: eu-west-1 does not list it
	assertStrings(t, "ListTables in "+regionB, listTableNamesIn(t, srv, regionB), []string{})

	// And: DescribeTable there is a ResourceNotFoundException
	resp := ddbCallIn(t, srv, regionB, "DescribeTable", map[string]any{"TableName": "orders"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// ---- Items -----------------------------------------------------------------

// TestRegionIsolation_ItemsAreIndependent is the core of #673: the same table
// name in two regions must hold entirely separate items.
func TestRegionIsolation_ItemsAreIndependent(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Given: the same table name created in both regions, each with its own item
	createTableIn(t, srv, regionA, "shared")
	createTableIn(t, srv, regionB, "shared")
	putItemIn(t, srv, regionA, "shared", "only-in-a", "a")
	putItemIn(t, srv, regionB, "shared", "only-in-b", "b")

	// Then: each region scans only its own item
	assertStrings(t, "Scan in "+regionA, scanIDsIn(t, srv, regionA, "shared"), []string{"only-in-a"})
	assertStrings(t, "Scan in "+regionB, scanIDsIn(t, srv, regionB, "shared"), []string{"only-in-b"})

	// And: GetItem cannot reach across regions
	resp := ddbCallIn(t, srv, regionB, "GetItem", map[string]any{
		"TableName": "shared",
		"Key":       map[string]any{"id": map[string]any{"S": "only-in-a"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got struct {
		Item map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, resp, &got)
	if len(got.Item) != 0 {
		t.Errorf("GetItem in %s returned %s's item: %v", regionB, regionA, got.Item)
	}

	// And: a same-keyed item in both regions keeps its own value
	putItemIn(t, srv, regionA, "shared", "dup", "value-a")
	putItemIn(t, srv, regionB, "shared", "dup", "value-b")
	for _, tc := range []struct{ region, want string }{{regionA, "value-a"}, {regionB, "value-b"}} {
		r := ddbCallIn(t, srv, tc.region, "GetItem", map[string]any{
			"TableName": "shared",
			"Key":       map[string]any{"id": map[string]any{"S": "dup"}},
		})
		var out struct {
			Item map[string]map[string]any `json:"Item"`
		}
		helpers.DecodeJSON(t, r, &out)
		r.Body.Close()
		if out.Item["tag"]["S"] != tc.want {
			t.Errorf("GetItem dup in %s: tag=%v, want %q", tc.region, out.Item["tag"]["S"], tc.want)
		}
	}

	// And: DescribeTable's ItemCount counts only that region's items
	for _, tc := range []struct {
		region string
		want   float64
	}{{regionA, 2}, {regionB, 2}} {
		r := ddbCallIn(t, srv, tc.region, "DescribeTable", map[string]any{"TableName": "shared"})
		var out struct {
			Table struct {
				ItemCount float64 `json:"ItemCount"`
			} `json:"Table"`
		}
		helpers.DecodeJSON(t, r, &out)
		r.Body.Close()
		if out.Table.ItemCount != tc.want {
			t.Errorf("DescribeTable ItemCount in %s: got %v, want %v", tc.region, out.Table.ItemCount, tc.want)
		}
	}
}

// TestRegionIsolation_DeleteTableLeavesOtherRegionIntact proves DeleteTable
// tears down one region's items and stream without touching the same-named
// table in the other region.
func TestRegionIsolation_DeleteTableLeavesOtherRegionIntact(t *testing.T) {
	srv := helpers.NewTestServer(t)

	createTableIn(t, srv, regionA, "shared")
	createTableIn(t, srv, regionB, "shared")
	putItemIn(t, srv, regionA, "shared", "a1", "a")
	putItemIn(t, srv, regionB, "shared", "b1", "b")

	resp := ddbCallIn(t, srv, regionA, "DeleteTable", map[string]any{"TableName": "shared"})
	resp.Body.Close()

	// The other region still has its table and its item.
	assertStrings(t, "ListTables in "+regionB, listTableNamesIn(t, srv, regionB), []string{"shared"})
	assertStrings(t, "Scan in "+regionB, scanIDsIn(t, srv, regionB, "shared"), []string{"b1"})
}

// ---- GSI index entries -----------------------------------------------------

// TestRegionIsolation_IndexEntriesAreIndependent covers the GSI index storage,
// which is a second, separate keyspace from the base items.
func TestRegionIsolation_IndexEntriesAreIndependent(t *testing.T) {
	srv := helpers.NewTestServer(t)

	createGSITableIn := func(region string) {
		t.Helper()
		resp := ddbCallIn(t, srv, region, "CreateTable", map[string]any{
			"TableName": "indexed",
			"AttributeDefinitions": []map[string]any{
				{"AttributeName": "id", "AttributeType": "S"},
				{"AttributeName": "tag", "AttributeType": "S"},
			},
			"KeySchema":   []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
			"BillingMode": "PAY_PER_REQUEST",
			"GlobalSecondaryIndexes": []map[string]any{{
				"IndexName":  "by-tag",
				"KeySchema":  []map[string]any{{"AttributeName": "tag", "KeyType": "HASH"}},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			}},
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("CreateTable indexed in %s: status %d: %s", region, resp.StatusCode, helpers.ReadBody(t, resp))
		}
	}

	// Given: the same GSI-bearing table in both regions, each with one item
	// sharing the same index key value
	createGSITableIn(regionA)
	createGSITableIn(regionB)
	putItemIn(t, srv, regionA, "indexed", "a1", "shared-tag")
	putItemIn(t, srv, regionB, "indexed", "b1", "shared-tag")

	// Then: a GSI Query in each region returns only that region's entry
	for _, tc := range []struct{ region, want string }{{regionA, "a1"}, {regionB, "b1"}} {
		resp := ddbCallIn(t, srv, tc.region, "Query", map[string]any{
			"TableName":                 "indexed",
			"IndexName":                 "by-tag",
			"KeyConditionExpression":    "tag = :t",
			"ExpressionAttributeValues": map[string]any{":t": map[string]any{"S": "shared-tag"}},
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var out struct {
			Items []map[string]map[string]any `json:"Items"`
		}
		helpers.DecodeJSON(t, resp, &out)
		resp.Body.Close()
		if len(out.Items) != 1 {
			t.Fatalf("GSI Query in %s: got %d items, want 1: %v", tc.region, len(out.Items), out.Items)
		}
		if got := out.Items[0]["id"]["S"]; got != tc.want {
			t.Errorf("GSI Query in %s: got id=%v, want %q", tc.region, got, tc.want)
		}
	}
}

// ---- Streams ---------------------------------------------------------------

// TestRegionIsolation_StreamsAreIndependent proves the stream half: same-named
// tables in two regions get distinct stream ARNs, and each stream replays only
// its own region's records.
func TestRegionIsolation_StreamsAreIndependent(t *testing.T) {
	srv := helpers.NewTestServer(t)

	createStreamTableIn := func(region string) string {
		t.Helper()
		resp := ddbCallIn(t, srv, region, "CreateTable", map[string]any{
			"TableName":            "events",
			"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
			"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
			"BillingMode":          "PAY_PER_REQUEST",
			"StreamSpecification": map[string]any{
				"StreamEnabled":  true,
				"StreamViewType": "NEW_AND_OLD_IMAGES",
			},
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("CreateTable events in %s: status %d: %s", region, resp.StatusCode, helpers.ReadBody(t, resp))
		}
		var out struct {
			TableDescription struct {
				LatestStreamArn string `json:"LatestStreamArn"`
			} `json:"TableDescription"`
		}
		helpers.DecodeJSON(t, resp, &out)
		return out.TableDescription.LatestStreamArn
	}

	// Given: the same stream-enabled table name in both regions
	arnA := createStreamTableIn(regionA)
	arnB := createStreamTableIn(regionB)
	if arnA == arnB {
		t.Fatalf("stream ARNs must differ across regions: %q", arnA)
	}

	// And: interleaved writes, so a shared sequence space would mix them up
	putItemIn(t, srv, regionA, "events", "a1", "a")
	putItemIn(t, srv, regionB, "events", "b1", "b")
	putItemIn(t, srv, regionA, "events", "a2", "a")
	putItemIn(t, srv, regionB, "events", "b2", "b")

	// Then: ListStreams in each region reports only its own stream
	for _, tc := range []struct{ region, wantARN string }{{regionA, arnA}, {regionB, arnB}} {
		resp := streamsCallIn(t, srv, tc.region, "ListStreams", map[string]any{})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var out struct {
			Streams []struct {
				StreamArn string `json:"StreamArn"`
			} `json:"Streams"`
		}
		helpers.DecodeJSON(t, resp, &out)
		resp.Body.Close()
		if len(out.Streams) != 1 {
			t.Fatalf("ListStreams in %s: got %d streams, want 1: %v", tc.region, len(out.Streams), out.Streams)
		}
		if out.Streams[0].StreamArn != tc.wantARN {
			t.Errorf("ListStreams in %s: got %q, want %q", tc.region, out.Streams[0].StreamArn, tc.wantARN)
		}
	}

	// And: DescribeStream refuses another region's stream ARN
	resp := streamsCallIn(t, srv, regionB, "DescribeStream", map[string]any{"StreamArn": arnA})
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
	resp.Body.Close()

	// And: GetRecords for each stream replays only that region's records
	for _, tc := range []struct {
		region, arn string
		wantIDs     []string
	}{
		{regionA, arnA, []string{"a1", "a2"}},
		{regionB, arnB, []string{"b1", "b2"}},
	} {
		iterResp := streamsCallIn(t, srv, tc.region, "GetShardIterator", map[string]any{
			"StreamArn":         tc.arn,
			"ShardId":           "shardId-00000000000000000001-events",
			"ShardIteratorType": "TRIM_HORIZON",
		})
		helpers.AssertStatus(t, iterResp, http.StatusOK)
		var iter struct {
			ShardIterator string `json:"ShardIterator"`
		}
		helpers.DecodeJSON(t, iterResp, &iter)
		iterResp.Body.Close()

		recResp := streamsCallIn(t, srv, tc.region, "GetRecords", map[string]any{
			"ShardIterator": iter.ShardIterator,
		})
		helpers.AssertStatus(t, recResp, http.StatusOK)
		var recs struct {
			Records []struct {
				AWSRegion string `json:"awsRegion"`
				Dynamodb  struct {
					Keys map[string]map[string]any `json:"Keys"`
				} `json:"dynamodb"`
			} `json:"Records"`
		}
		helpers.DecodeJSON(t, recResp, &recs)
		recResp.Body.Close()

		got := make([]string, 0, len(recs.Records))
		for _, r := range recs.Records {
			if v, ok := r.Dynamodb.Keys["id"]["S"].(string); ok {
				got = append(got, v)
			}
			if r.AWSRegion != tc.region {
				t.Errorf("GetRecords in %s: record awsRegion=%q, want %q", tc.region, r.AWSRegion, tc.region)
			}
		}
		assertStrings(t, "GetRecords in "+tc.region, got, tc.wantIDs)
	}
}

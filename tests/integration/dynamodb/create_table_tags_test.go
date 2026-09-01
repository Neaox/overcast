// create_table_tags_test.go — CreateTable's inline Tags never reached the
// shared validator (#1052), unlike TagResource which already did (dynamoTagCfg
// was wired there, but not into the create path). A reserved `aws:` key
// prefix had to be rejected the way real AWS rejects it, and it was not.
package dynamodb_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestCreateTable_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "invalid-tag-create",
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
		},
		"BillingMode": "PAY_PER_REQUEST",
		"Tags": []map[string]string{
			{"Key": "aws:reserved", "Value": "x"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")

	// And: no table was left behind for the rejected tags to strand.
	describe := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "invalid-tag-create"})
	defer describe.Body.Close()
	if describe.StatusCode == http.StatusOK {
		t.Fatalf("a table named invalid-tag-create exists despite the rejected create")
	}
}

func TestCreateTable_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "valid-tag-create",
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
		},
		"BillingMode": "PAY_PER_REQUEST",
		"Tags": []map[string]string{
			{"Key": "env", "Value": "prod"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	tagResp := ddbCall(t, srv, "ListTagsOfResource", map[string]any{
		"ResourceArn": "arn:aws:dynamodb:us-east-1:000000000000:table/valid-tag-create",
	})
	defer tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, tagResp, &out)
	found := false
	for _, tag := range out.Tags {
		if tag.Key == "env" && tag.Value == "prod" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tags = %#v, want env=prod", out.Tags)
	}
}

package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// AWS::DynamoDB::Table declares BillingMode and ProvisionedThroughput with the
// same semantics as the CreateTable API: an omitted BillingMode is PROVISIONED,
// which makes ProvisionedThroughput required. CloudFormation stays a thin
// translation layer, so a template that breaks those rules fails the stack with
// DynamoDB's own modeled error instead of being rewritten to on-demand billing.

func dynamodbBillingTemplate(tableName, billingProperties string) string {
	return `{
  "Resources": {
    "Table": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "` + tableName + `",
        "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}]` + billingProperties + `
      }
    }
  }
}`
}

func TestCreateStack_DynamoDBTableBillingMode(t *testing.T) {
	tests := []struct {
		name       string
		slug       string
		properties string
		wantStatus string
		wantMode   string // BillingModeSummary.BillingMode; empty means absent
		wantReason string
	}{
		{
			name: "omitted billing mode with throughput defaults to provisioned",
			slug: "defaulted",
			properties: `,
        "ProvisionedThroughput": {"ReadCapacityUnits": 5, "WriteCapacityUnits": 5}`,
			wantStatus: "CREATE_COMPLETE",
		},
		{
			name: "explicit pay per request",
			slug: "on-demand",
			properties: `,
        "BillingMode": "PAY_PER_REQUEST"`,
			wantStatus: "CREATE_COMPLETE",
			wantMode:   "PAY_PER_REQUEST",
		},
		{
			name:       "omitted billing mode without throughput fails the stack",
			slug:       "missing-throughput",
			properties: "",
			wantStatus: "ROLLBACK_COMPLETE",
			wantReason: "ReadCapacityUnits and WriteCapacityUnits must both be specified when BillingMode is PROVISIONED",
		},
		{
			name: "pay per request with throughput fails the stack",
			slug: "forbidden-throughput",
			properties: `,
        "BillingMode": "PAY_PER_REQUEST",
        "ProvisionedThroughput": {"ReadCapacityUnits": 5, "WriteCapacityUnits": 5}`,
			wantStatus: "ROLLBACK_COMPLETE",
			wantReason: "Neither ReadCapacityUnits nor WriteCapacityUnits can be specified when BillingMode is PAY_PER_REQUEST",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a stack template declaring a DynamoDB table.
			srv := helpers.NewTestServer(t)
			stackName := "ddb-billing-" + tc.slug
			tableName := "cfn-billing-" + tc.slug

			// When: CloudFormation provisions it.
			createResp := cfnQuery(t, srv, "CreateStack", url.Values{
				"StackName":    {stackName},
				"TemplateBody": {dynamodbBillingTemplate(tableName, tc.properties)},
			})
			defer createResp.Body.Close()
			helpers.AssertStatus(t, createResp, http.StatusOK)
			waitForStackStatus(t, srv, stackName, tc.wantStatus)

			// Then: DynamoDB's own defaulting and validation decide the outcome.
			if tc.wantReason != "" {
				reasons := strings.Join(describeStackEventReasons(t, srv, stackName), "\n")
				if !strings.Contains(reasons, tc.wantReason) {
					t.Errorf("stack event reasons = %q, want it to contain %q", reasons, tc.wantReason)
				}
				return
			}

			descResp := dynamodbCall(t, srv, "DescribeTable", map[string]any{"TableName": tableName})
			var result struct {
				Table struct {
					BillingModeSummary *struct {
						BillingMode string `json:"BillingMode"`
					} `json:"BillingModeSummary"`
				} `json:"Table"`
			}
			helpers.DecodeJSON(t, descResp, &result)
			switch {
			case tc.wantMode == "" && result.Table.BillingModeSummary != nil:
				t.Errorf("BillingModeSummary = %+v, want absent for a defaulted PROVISIONED table",
					result.Table.BillingModeSummary)
			case tc.wantMode != "" && (result.Table.BillingModeSummary == nil ||
				result.Table.BillingModeSummary.BillingMode != tc.wantMode):
				t.Errorf("BillingModeSummary = %+v, want %q", result.Table.BillingModeSummary, tc.wantMode)
			}
		})
	}
}

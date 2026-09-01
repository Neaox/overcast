package cloudformation_test

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// A stack whose log group updates cleanly and whose added subscription cannot
// be provisioned: the update reaches one resource before failing on the next.
const (
	disableRollbackBaseTemplate = `{
  "Resources": {
    "Logs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {"LogGroupName": "%s", "RetentionInDays": 7}
    }
  }
}`
	disableRollbackFailingTemplate = `{
  "Resources": {
    "Logs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {"LogGroupName": "%s", "RetentionInDays": 14}
    },
    "SubB": {
      "Type": "AWS::SNS::Subscription",
      "DependsOn": "Logs",
      "Properties": {
        "TopicArn": "arn:aws:sns:us-east-1:000000000000:nonexistent-topic-xyzzy",
        "Protocol": "sqs",
        "Endpoint": "arn:aws:sqs:us-east-1:000000000000:nonexistent-queue"
      }
    }
  }
}`
)

// AWS documents UpdateStack's DisableRollback as an optional Boolean defaulting
// to False, so the member decides this operation on its own rather than
// inheriting whatever CreateStack was given.
func TestUpdateStack_disableRollback(t *testing.T) {
	cases := []struct {
		name             string
		createDisable    string
		updateDisable    []string
		wantStatus       string
		wantFailedRecord bool
	}{
		{
			name:             "enabled on update",
			updateDisable:    []string{"true"},
			wantStatus:       "UPDATE_FAILED",
			wantFailedRecord: true,
		},
		{
			name:          "explicitly disabled overrides the create value",
			createDisable: "true",
			updateDisable: []string{"false"},
			wantStatus:    "UPDATE_ROLLBACK_COMPLETE",
		},
		{
			name:          "omitted does not inherit the create value",
			createDisable: "true",
			wantStatus:    "UPDATE_ROLLBACK_COMPLETE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a provisioned stack, created with the given DisableRollback.
			srv := helpers.NewTestServer(t)
			stackName := "upd-disable-rollback"
			logGroup := "upd-disable-rollback-logs"
			createParams := url.Values{
				"StackName":    {stackName},
				"TemplateBody": {fmt.Sprintf(disableRollbackBaseTemplate, logGroup)},
			}
			if tc.createDisable != "" {
				createParams.Set("DisableRollback", tc.createDisable)
			}
			createResp := cfnQuery(t, srv, "CreateStack", createParams)
			defer createResp.Body.Close()
			helpers.AssertStatus(t, createResp, http.StatusOK)
			waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

			// When: the stack is updated with a template that cannot provision.
			updateParams := url.Values{
				"StackName":    {stackName},
				"TemplateBody": {fmt.Sprintf(disableRollbackFailingTemplate, logGroup)},
			}
			if tc.updateDisable != nil {
				updateParams["DisableRollback"] = tc.updateDisable
			}
			updateResp := cfnQuery(t, srv, "UpdateStack", updateParams)
			defer updateResp.Body.Close()
			helpers.AssertStatus(t, updateResp, http.StatusOK)

			// Then: the update's own DisableRollback decides whether it unwinds.
			waitForStackStatus(t, srv, stackName, tc.wantStatus)
			if got := describeStackStatus(t, srv, stackName); got != tc.wantStatus {
				t.Fatalf("stack status = %q, want %q", got, tc.wantStatus)
			}

			// And: with rollback disabled the stack still reports everything the
			// attempt reached — the updated resource and the failed one.
			resources := describeStackResources(t, srv, stackName)
			if !tc.wantFailedRecord {
				return
			}
			if got := resources["Logs"].Status; got != "UPDATE_COMPLETE" {
				t.Errorf("Logs status = %q, want UPDATE_COMPLETE", got)
			}
			failed, ok := resources["SubB"]
			if !ok {
				t.Fatalf("SubB missing from stack resources %+v", resources)
			}
			if failed.Status != "CREATE_FAILED" || failed.StatusReason == "" {
				t.Errorf("SubB = %+v, want CREATE_FAILED with a reason", failed)
			}
		})
	}
}

// ---- Test helpers ----------------------------------------------------------

type stackResourceRecord struct {
	Status       string
	StatusReason string
	PhysicalID   string
}

func describeStackResources(t *testing.T, srv *helpers.TestServer, stackName string) map[string]stackResourceRecord {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackResources", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var result struct {
		Resources []struct {
			LogicalID    string `xml:"LogicalResourceId"`
			PhysicalID   string `xml:"PhysicalResourceId"`
			Status       string `xml:"ResourceStatus"`
			StatusReason string `xml:"ResourceStatusReason"`
		} `xml:"DescribeStackResourcesResult>StackResources>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStackResourcesResponse: %v\nbody: %s", err, body)
	}
	records := make(map[string]stackResourceRecord, len(result.Resources))
	for _, res := range result.Resources {
		records[res.LogicalID] = stackResourceRecord{
			Status:       res.Status,
			StatusReason: res.StatusReason,
			PhysicalID:   res.PhysicalID,
		}
	}
	return records
}

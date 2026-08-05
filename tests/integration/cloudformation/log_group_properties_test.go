package cloudformation_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateStack_LogGroupRetentionInDays(t *testing.T) {
	// Given: a stack template with a log group retention policy
	srv := helpers.NewTestServer(t)
	const stackName = "log-group-retention-stack"
	const logGroupName = "/cloudformation/retention-on-create"
	const template = `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {
        "LogGroupName": "/cloudformation/retention-on-create",
        "RetentionInDays": 7
      }
    }
  }
}`

	// When: the stack is created
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: the log group reports retention without requiring a stack update
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(`{"logGroupNamePrefix":"`+logGroupName+`"}`))
	if err != nil {
		t.Fatalf("build DescribeLogGroups request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "Logs_20140328.DescribeLogGroups")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DescribeLogGroups: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var got struct {
		LogGroups []struct {
			LogGroupName    string `json:"logGroupName"`
			RetentionInDays *int   `json:"retentionInDays"`
		} `json:"logGroups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode DescribeLogGroups response: %v", err)
	}
	if len(got.LogGroups) != 1 {
		t.Fatalf("DescribeLogGroups returned %d groups, want 1: %+v", len(got.LogGroups), got.LogGroups)
	}
	group := got.LogGroups[0]
	if group.LogGroupName != logGroupName {
		t.Errorf("logGroupName = %q, want %q", group.LogGroupName, logGroupName)
	}
	if group.RetentionInDays == nil {
		t.Fatal("retentionInDays is absent after stack creation")
	}
	if *group.RetentionInDays != 7 {
		t.Errorf("retentionInDays = %d, want 7", *group.RetentionInDays)
	}
}

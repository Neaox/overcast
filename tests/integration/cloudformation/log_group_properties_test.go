package cloudformation_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
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
	retention := describeLogGroupRetention(t, srv, logGroupName)
	if retention == nil {
		t.Fatal("retentionInDays is absent after stack creation")
	}
	if *retention != 7 {
		t.Errorf("retentionInDays = %d, want 7", *retention)
	}
}

func TestUpdateStack_LogGroupRetentionInDaysRemoved(t *testing.T) {
	// Given: a stack whose log group has a retention policy
	srv := helpers.NewTestServer(t)
	const stackName = "log-group-remove-retention-stack"
	const logGroupName = "/cloudformation/remove-retention"
	const initialTemplate = `{"Resources":{"LogGroup":{"Type":"AWS::Logs::LogGroup","Properties":{"LogGroupName":"/cloudformation/remove-retention","RetentionInDays":7}}}}`
	const updatedTemplate = `{"Resources":{"LogGroup":{"Type":"AWS::Logs::LogGroup","Properties":{"LogGroupName":"/cloudformation/remove-retention"}}}}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initialTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the updated template omits RetentionInDays
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the service reports no retention policy
	if retention := describeLogGroupRetention(t, srv, logGroupName); retention != nil {
		t.Fatalf("retentionInDays = %d after removal, want omitted", *retention)
	}
}

func TestUpdateStack_LogGroupTags(t *testing.T) {
	// Given: a stack log group with CloudFormation-owned tags
	srv := helpers.NewTestServer(t)
	const stackName = "log-group-tags-stack"
	const logGroupName = "/cloudformation/tag-reconciliation"
	const initialTemplate = `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {
        "LogGroupName": "/cloudformation/tag-reconciliation",
        "Tags": [
          {"Key": "environment", "Value": "development"},
          {"Key": "owner", "Value": "platform"}
        ]
      }
    }
  }
}`
	const updatedTemplate = `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {
        "LogGroupName": "/cloudformation/tag-reconciliation",
        "Tags": [
          {"Key": "environment", "Value": "production"},
          {"Key": "project", "Value": "overcast"}
        ]
      }
    }
  }
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initialTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	if got := listLogGroupTags(t, srv, logGroupName); !reflect.DeepEqual(got, map[string]string{
		"environment": "development",
		"owner":       "platform",
	}) {
		t.Fatalf("initial tags = %#v", got)
	}

	// When: the resource tags are changed, added, and removed
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the Logs tag set matches the updated CloudFormation tags
	if got := listLogGroupTags(t, srv, logGroupName); !reflect.DeepEqual(got, map[string]string{
		"environment": "production",
		"project":     "overcast",
	}) {
		t.Fatalf("updated tags = %#v", got)
	}
}

func TestUpdateStack_LogGroupStackTags(t *testing.T) {
	// Given: a log group with stack tags and a colliding resource tag
	srv := helpers.NewTestServer(t)
	const stackName = "log-group-stack-tags"
	const logGroupName = "/cloudformation/stack-tag-reconciliation"
	const template = `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {
        "LogGroupName": "/cloudformation/stack-tag-reconciliation",
        "Tags": [
          {"Key": "environment", "Value": "resource"},
          {"Key": "project", "Value": "overcast"}
        ]
      }
    }
  }
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {template},
		"Tags.member.1.Key":   {"environment"},
		"Tags.member.1.Value": {"stack"},
		"Tags.member.2.Key":   {"team"},
		"Tags.member.2.Value": {"platform"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	if got := listLogGroupTags(t, srv, logGroupName); !reflect.DeepEqual(got, map[string]string{
		"environment": "resource",
		"project":     "overcast",
		"team":        "platform",
	}) {
		t.Fatalf("initial effective tags = %#v", got)
	}

	// When: only stack tags change while the template remains identical
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {template},
		"Tags.member.1.Key":   {"environment"},
		"Tags.member.1.Value": {"updated-stack"},
		"Tags.member.2.Key":   {"owner"},
		"Tags.member.2.Value": {"operations"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: additions and removals propagate while the resource tag keeps precedence
	if got := listLogGroupTags(t, srv, logGroupName); !reflect.DeepEqual(got, map[string]string{
		"environment": "resource",
		"owner":       "operations",
		"project":     "overcast",
	}) {
		t.Fatalf("updated effective tags = %#v", got)
	}

	// When: stack tags are explicitly cleared
	clearResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
		"Tags":         {""},
	})
	defer clearResp.Body.Close()
	helpers.AssertStatus(t, clearResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: only resource-level tags remain
	if got := listLogGroupTags(t, srv, logGroupName); !reflect.DeepEqual(got, map[string]string{
		"environment": "resource",
		"project":     "overcast",
	}) {
		t.Fatalf("tags after clearing stack tags = %#v", got)
	}
}

func describeLogGroupRetention(t *testing.T, srv *helpers.TestServer, logGroupName string) *int {
	t.Helper()
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
	return group.RetentionInDays
}

func listLogGroupTags(t *testing.T, srv *helpers.TestServer, logGroupName string) map[string]string {
	t.Helper()
	body := `{"logGroupName":` + strconv.Quote(logGroupName) + `}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build ListTagsLogGroup request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "Logs_20140328.ListTagsLogGroup")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ListTagsLogGroup: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var got struct {
		Tags map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode ListTagsLogGroup response: %v", err)
	}
	return got.Tags
}

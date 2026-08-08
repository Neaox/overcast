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

// RetentionInDays is validated by the CloudWatch Logs service, not by the
// CloudFormation adapter. A template carrying a value outside AWS's fixed set
// must therefore fail the resource through the normal dispatch path and roll
// the stack back, leaving no log group behind.
func TestCreateStack_LogGroupInvalidRetentionRollsBack(t *testing.T) {
	// Given: a stack template with a retention value AWS does not accept
	srv := helpers.NewTestServer(t)
	const stackName = "log-group-invalid-retention-stack"
	const logGroupName = "/cloudformation/invalid-retention"
	const template = `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {
        "LogGroupName": "/cloudformation/invalid-retention",
        "RetentionInDays": 2
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

	// Then: the stack rolls back rather than reporting success
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

	// And: the service error surfaces in the stack events rather than being
	// reworded by a CloudFormation-side check
	reasons := strings.Join(describeStackEventReasons(t, srv, stackName), "\n")
	if !strings.Contains(reasons, "InvalidParameterException") {
		t.Errorf("failure reasons do not carry InvalidParameterException:\n%s", reasons)
	}

	// And: the log group created before the failure was cleaned up
	if logGroupExists(t, srv, logGroupName) {
		t.Error("log group survived rollback")
	}
}

// The same validation must fail an in-place update, which rolls the stack back
// and leaves the previously applied retention policy untouched.
func TestUpdateStack_LogGroupInvalidRetentionRollsBack(t *testing.T) {
	// Given: a stack whose log group has a valid retention policy
	srv := helpers.NewTestServer(t)
	const stackName = "log-group-invalid-retention-update-stack"
	const logGroupName = "/cloudformation/invalid-retention-update"
	const initialTemplate = `{"Resources":{"LogGroup":{"Type":"AWS::Logs::LogGroup","Properties":{"LogGroupName":"/cloudformation/invalid-retention-update","RetentionInDays":7}}}}`
	const updatedTemplate = `{"Resources":{"LogGroup":{"Type":"AWS::Logs::LogGroup","Properties":{"LogGroupName":"/cloudformation/invalid-retention-update","RetentionInDays":9}}}}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initialTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the template is updated to an unsupported retention value
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)

	// Then: the stack rolls back and the original retention policy survives
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")
	retention := describeLogGroupRetention(t, srv, logGroupName)
	if retention == nil || *retention != 7 {
		t.Fatalf("retentionInDays = %v after rolled-back update, want 7", retention)
	}
}

// Tag validity is the Logs service's business too. A template carrying a
// reserved `aws:` key must fail through the normal dispatch path, roll the
// stack back, and leave no log group behind — the tags travel with
// CreateLogGroup, so there is nothing half-made to reconcile.
func TestCreateStack_LogGroupInvalidTagRollsBack(t *testing.T) {
	// Given: a stack template with a tag key AWS reserves
	srv := helpers.NewTestServer(t)
	const stackName = "log-group-invalid-tag-stack"
	const logGroupName = "/cloudformation/invalid-tag"
	const template = `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {
        "LogGroupName": "/cloudformation/invalid-tag",
        "Tags": [{"Key": "aws:created-by", "Value": "overcast"}]
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

	// Then: the stack rolls back carrying the service's own error
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")
	reasons := strings.Join(describeStackEventReasons(t, srv, stackName), "\n")
	if !strings.Contains(reasons, "InvalidParameterException") {
		t.Errorf("failure reasons do not carry InvalidParameterException:\n%s", reasons)
	}

	// And: no log group exists
	if logGroupExists(t, srv, logGroupName) {
		t.Error("log group survived rollback")
	}
}

// Create-time tags are applied by CreateLogGroup itself, so they are readable
// as soon as the stack completes.
func TestCreateStack_LogGroupTagsAppliedAtCreate(t *testing.T) {
	// Given: a stack template with resource tags
	srv := helpers.NewTestServer(t)
	const stackName = "log-group-create-tags-stack"
	const logGroupName = "/cloudformation/tags-at-create"
	const template = `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {
        "LogGroupName": "/cloudformation/tags-at-create",
        "Tags": [{"Key": "environment", "Value": "development"}]
      }
    }
  }
}`

	// When: the stack is created
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {template},
		"Tags.member.1.Key":   {"team"},
		"Tags.member.1.Value": {"platform"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: resource and stack tags are both present on the new group
	if got := listLogGroupTags(t, srv, logGroupName); !reflect.DeepEqual(got, map[string]string{
		"environment": "development",
		"team":        "platform",
	}) {
		t.Fatalf("tags = %#v", got)
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

type describedLogGroup struct {
	LogGroupName    string `json:"logGroupName"`
	RetentionInDays *int   `json:"retentionInDays"`
}

func describeLogGroups(t *testing.T, srv *helpers.TestServer, prefix string) []describedLogGroup {
	t.Helper()
	body := `{"logGroupNamePrefix":` + strconv.Quote(prefix) + `}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(body))
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
		LogGroups []describedLogGroup `json:"logGroups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode DescribeLogGroups response: %v", err)
	}
	return got.LogGroups
}

func logGroupExists(t *testing.T, srv *helpers.TestServer, logGroupName string) bool {
	t.Helper()
	for _, g := range describeLogGroups(t, srv, logGroupName) {
		if g.LogGroupName == logGroupName {
			return true
		}
	}
	return false
}

func describeLogGroupRetention(t *testing.T, srv *helpers.TestServer, logGroupName string) *int {
	t.Helper()
	groups := describeLogGroups(t, srv, logGroupName)
	if len(groups) != 1 {
		t.Fatalf("DescribeLogGroups returned %d groups, want 1: %+v", len(groups), groups)
	}
	if groups[0].LogGroupName != logGroupName {
		t.Errorf("logGroupName = %q, want %q", groups[0].LogGroupName, logGroupName)
	}
	return groups[0].RetentionInDays
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

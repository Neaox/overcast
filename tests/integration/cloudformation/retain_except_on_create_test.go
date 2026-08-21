package cloudformation_test

// retain_except_on_create_test.go — RetainExceptOnCreate, the stack-operation
// member that decides whether a rollback deletes the resources the failed
// operation created or leaves them standing.
//
// The orphan the flag exists to prevent is the expensive one. A create that
// fails partway leaves a Retain-marked resource holding a name nothing in the
// stack records any more, and every later deploy of the same template then
// collides with it — the resource cannot be created, so the stack cannot be
// created, and no amount of re-running fixes it. That is real CloudFormation's
// default too, which is why the flag has to work rather than the default having
// to change.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// retainedLogGroupTemplate creates a log group the template marks Retain, then
// fails on a resource that depends on it — the shape a CDK stack takes, where
// logs.LogGroup carries DeletionPolicy=Retain by default.
func retainedLogGroupTemplate(logGroupName string) string {
	return `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "DeletionPolicy": "Retain",
      "Properties": { "LogGroupName": "` + logGroupName + `" }
    },
    "SubB": {
      "Type": "AWS::SNS::Subscription",
      "DependsOn": "LogGroup",
      "Properties": {
        "TopicArn": "arn:aws:sns:us-east-1:000000000000:nonexistent-topic-xyzzy",
        "Protocol": "sqs",
        "Endpoint": "arn:aws:sqs:us-east-1:000000000000:nonexistent-queue"
      }
    }
  }
}`
}

func TestCreateStack_retainExceptOnCreateDeletesARetainedResourceOnRollback(t *testing.T) {
	// Given: a create that will fail after making a resource marked Retain
	srv := helpers.NewTestServer(t)
	const stackName = "reoc-create-stack"
	const logGroupName = "/cloudformation/reoc-create"

	// When: the create asks for RetainExceptOnCreate and rolls back
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":            {stackName},
		"TemplateBody":         {retainedLogGroupTemplate(logGroupName)},
		"RetainExceptOnCreate": {"true"},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

	// Then: the resource the create made is gone, so the next deploy of the
	// same template has nothing to collide with
	if groups := describeLogGroups(t, srv, logGroupName); len(groups) != 0 {
		t.Fatalf("log group survived the rollback: %+v", groups)
	}
}

// The flag is opt-in: without it, Retain still means retain, as it does on AWS.
func TestCreateStack_rollbackWithoutRetainExceptOnCreateKeepsARetainedResource(t *testing.T) {
	// Given: the same create, not asking for the flag
	srv := helpers.NewTestServer(t)
	const stackName = "reoc-default-stack"
	const logGroupName = "/cloudformation/reoc-default"

	// When: it rolls back
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {retainedLogGroupTemplate(logGroupName)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

	// Then: the resource stands, and the event history says why
	if groups := describeLogGroups(t, srv, logGroupName); len(groups) != 1 {
		t.Fatalf("DescribeLogGroups returned %d groups, want the retained one: %+v", len(groups), groups)
	}
	eventsResp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
	defer eventsResp.Body.Close()
	body := string(readBody(t, eventsResp))
	if !strings.Contains(body, "DELETE_SKIPPED") || !strings.Contains(body, "DeletionPolicy=Retain") {
		t.Fatalf("expected a DELETE_SKIPPED event naming the policy, got: %s", body)
	}
}

// cdk deploy executes a change set rather than calling CreateStack, so a flag
// only CreateStack honours is a flag that never reaches the deploy that needs
// it. AWS puts the member on ExecuteChangeSet and not on CreateChangeSet — the
// change set records no value of its own, so the execution decides alone.
func TestExecuteChangeSet_retainExceptOnCreateReachesTheRollback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		onExecute   string
		wantDeleted bool
	}{
		{name: "set on the execution", onExecute: "true", wantDeleted: true},
		{name: "explicitly declined", onExecute: "false", wantDeleted: false},
		{name: "omitted", wantDeleted: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a CREATE change set over a template that will fail
			srv := helpers.NewTestServer(t)
			stackName := "reoc-cs-" + strings.ReplaceAll(tc.name, " ", "-")
			logGroupName := "/cloudformation/" + stackName
			csResp := cfnQuery(t, srv, "CreateChangeSet", url.Values{
				"StackName":     {stackName},
				"ChangeSetName": {"cs1"},
				"ChangeSetType": {"CREATE"},
				"TemplateBody":  {retainedLogGroupTemplate(logGroupName)},
			})
			defer csResp.Body.Close()
			helpers.AssertStatus(t, csResp, http.StatusOK)

			// When: it is executed and rolls back
			execValues := url.Values{"StackName": {stackName}, "ChangeSetName": {"cs1"}}
			if tc.onExecute != "" {
				execValues.Set("RetainExceptOnCreate", tc.onExecute)
			}
			execResp := cfnQuery(t, srv, "ExecuteChangeSet", execValues)
			defer execResp.Body.Close()
			helpers.AssertStatus(t, execResp, http.StatusOK)
			waitForStackStatus(t, srv, stackName, "ROLLBACK_COMPLETE")

			// Then: the resource is deleted exactly when the flag was in force
			groups := describeLogGroups(t, srv, logGroupName)
			if tc.wantDeleted && len(groups) != 0 {
				t.Fatalf("log group survived the rollback: %+v", groups)
			}
			if !tc.wantDeleted && len(groups) != 1 {
				t.Fatalf("DescribeLogGroups returned %d groups, want the retained one: %+v", len(groups), groups)
			}
		})
	}
}

// A malformed value is a ValidationError, not a silent false.
func TestCreateStack_retainExceptOnCreateRejectsANonBoolean(t *testing.T) {
	// Given: a create carrying a RetainExceptOnCreate that is not a boolean
	srv := helpers.NewTestServer(t)

	// When: CreateStack is called
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":            {"reoc-bad-value"},
		"TemplateBody":         {retainedLogGroupTemplate("/cloudformation/reoc-bad")},
		"RetainExceptOnCreate": {"yes-please"},
	})
	defer resp.Body.Close()

	// Then: it is rejected
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	if body := string(readBody(t, resp)); !strings.Contains(body, "retainExceptOnCreate") {
		t.Fatalf("expected the error to name the member, got: %s", body)
	}
}

package cloudformation_test

// update_rollback_retain_test.go — DeletionPolicy on the resources a *failed
// update* created, and the RetainExceptOnCreate operation flag over the top of
// it.
//
// The create side of this lives in retain_except_on_create_test.go. An update
// rollback faces the same question about the same resource: a log group the
// CDK marks Retain by default, made by the update that then failed. Real
// CloudFormation keeps it unless the operation asked for the flag, and the
// orphan that leaves is the expensive one — a name nothing in the stack records
// any more, which every later deploy of the same template collides with.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// retainedLogGroupUpdateTemplate is the second version of updateBaseTemplate:
// it adds a log group marked Retain and then fails on a resource that depends
// on it, so the rollback has a freshly created retained resource to decide
// about. The queue carries over unchanged, so nothing else moves.
func retainedLogGroupUpdateTemplate(queueName, logGroupName string) string {
	return `{
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": { "QueueName": "` + queueName + `" }
    },
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

// createUpdateBaseStack creates the single-queue stack these updates start
// from, returning the queue name so the update can carry it over unchanged.
func createUpdateBaseStack(t *testing.T, srv *helpers.TestServer, stackName string) string {
	t.Helper()
	queueName := stackName + "-queue"
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": { "QueueName": "` + queueName + `" }
    }
  }
}`},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE")
	return queueName
}

func TestUpdateStack_rollbackHonoursDeletionPolicyOnAResourceTheUpdateCreated(t *testing.T) {
	for _, tc := range []struct {
		name        string
		onUpdate    string
		wantDeleted bool
	}{
		{name: "omitted", wantDeleted: false},
		{name: "explicitly declined", onUpdate: "false", wantDeleted: false},
		{name: "set on the update", onUpdate: "true", wantDeleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a stack whose update will create a Retain-marked resource
			// and then fail
			srv := helpers.NewTestServer(t)
			stackName := "reoc-upd-" + strings.ReplaceAll(tc.name, " ", "-")
			logGroupName := "/cloudformation/" + stackName
			queueName := createUpdateBaseStack(t, srv, stackName)

			// When: the update fails and rolls back
			updateValues := url.Values{
				"StackName":    {stackName},
				"TemplateBody": {retainedLogGroupUpdateTemplate(queueName, logGroupName)},
			}
			if tc.onUpdate != "" {
				updateValues.Set("RetainExceptOnCreate", tc.onUpdate)
			}
			ur := cfnQuery(t, srv, "UpdateStack", updateValues)
			defer ur.Body.Close()
			helpers.AssertStatus(t, ur, http.StatusOK)
			waitForStackStatusIn(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

			// Then: the resource is deleted exactly when the flag was in force,
			// and the rollback that kept it says on the record why
			groups := describeLogGroups(t, srv, logGroupName)
			if tc.wantDeleted {
				if len(groups) != 0 {
					t.Fatalf("log group survived the update rollback: %+v", groups)
				}
				return
			}
			if len(groups) != 1 {
				t.Fatalf("DescribeLogGroups returned %d groups, want the retained one: %+v", len(groups), groups)
			}
			eventsResp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
			defer eventsResp.Body.Close()
			body := string(readBody(t, eventsResp))
			if !strings.Contains(body, "DELETE_SKIPPED") || !strings.Contains(body, "DeletionPolicy=Retain") {
				t.Fatalf("expected a DELETE_SKIPPED event naming the policy, got: %s", body)
			}
		})
	}
}

// cdk deploy against an existing stack executes an UPDATE change set, so the
// flag has to reach the update rollback by that route as well.
func TestExecuteChangeSet_retainExceptOnCreateReachesTheUpdateRollback(t *testing.T) {
	// Given: an UPDATE change set that will create a Retain-marked resource
	// and then fail
	srv := helpers.NewTestServer(t)
	const stackName = "reoc-upd-cs"
	const logGroupName = "/cloudformation/reoc-upd-cs"
	queueName := createUpdateBaseStack(t, srv, stackName)
	csResp := cfnQuery(t, srv, "CreateChangeSet", url.Values{
		"StackName":     {stackName},
		"ChangeSetName": {"cs1"},
		"ChangeSetType": {"UPDATE"},
		"TemplateBody":  {retainedLogGroupUpdateTemplate(queueName, logGroupName)},
	})
	defer csResp.Body.Close()
	helpers.AssertStatus(t, csResp, http.StatusOK)

	// When: it is executed asking for RetainExceptOnCreate and rolls back
	execResp := cfnQuery(t, srv, "ExecuteChangeSet", url.Values{
		"StackName":            {stackName},
		"ChangeSetName":        {"cs1"},
		"RetainExceptOnCreate": {"true"},
	})
	defer execResp.Body.Close()
	helpers.AssertStatus(t, execResp, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: the resource the update made is gone
	if groups := describeLogGroups(t, srv, logGroupName); len(groups) != 0 {
		t.Fatalf("log group survived the update rollback: %+v", groups)
	}
}

// retainedLogGroupReplacementStack is one Retain-marked log group, optionally
// followed by a resource that will fail. Renaming the group is a replacement:
// LogGroupName is an identity property, so the update creates a second group
// and the original is deleted only once the update succeeds.
func retainedLogGroupReplacementStack(logGroupName, extra string) string {
	return `{
  "Resources": {
    "LogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "DeletionPolicy": "Retain",
      "Properties": { "LogGroupName": "` + logGroupName + `" }
    }` + extra + `
  }
}`
}

// The replacement a failed update created is a separate question, and stays
// unconditional. DeletionPolicy does not speak to it: the original is still
// alive and is what the stack rolls back to, so keeping the replacement as well
// would leave two resources where the template asks for one — the second under
// a name the restored stack does not record, which is the orphan the flag
// exists to prevent rather than one it should produce. UpdateReplacePolicy is
// the policy for a replacement, and it governs the original on the way forward.
func TestUpdateStack_rollbackDeletesTheReplacementItCreatedForARetainedResource(t *testing.T) {
	// Given: a stack with a Retain-marked log group, and an update that renames
	// it and then fails
	srv := helpers.NewTestServer(t)
	const stackName = "reoc-upd-replace"
	const oldName = "/cloudformation/reoc-upd-replace-v1"
	const newName = "/cloudformation/reoc-upd-replace-v2"
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {retainedLogGroupReplacementStack(oldName, "")},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE")

	// When: the update fails after creating the replacement
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {retainedLogGroupReplacementStack(newName, `,
    "SubB": {
      "Type": "AWS::SNS::Subscription",
      "DependsOn": "LogGroup",
      "Properties": {
        "TopicArn": "arn:aws:sns:us-east-1:000000000000:nonexistent-topic-xyzzy",
        "Protocol": "sqs",
        "Endpoint": "arn:aws:sqs:us-east-1:000000000000:nonexistent-queue"
      }
    }`)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: the replacement is gone and the original — what the stack rolled
	// back to — is still standing
	if groups := describeLogGroups(t, srv, newName); len(groups) != 0 {
		t.Fatalf("replacement survived the update rollback: %+v", groups)
	}
	if groups := describeLogGroups(t, srv, oldName); len(groups) != 1 {
		t.Fatalf("DescribeLogGroups returned %d groups for the original, want it standing: %+v", len(groups), groups)
	}
}

package cloudformation_test

// replacement_rollback_test.go — replacement ordering and what a failed update
// leaves behind.
//
// CloudFormation replaces a resource by creating the new one first and
// deleting the old one only after the whole update succeeds. The ordering is
// what makes rollback possible: if anything later in the update fails, the
// original is still there to roll back to. Deleting first would mean a failed
// replacement destroys the resource outright, with nothing to restore — the
// update would take the data with it.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// A stack whose second resource fails to create, forcing a rollback after the
// first resource has already been replaced. AWS::Lambda::Function with no Code
// and no Runtime is rejected by CreateFunction, which is what fails the update.
//
// DependsOn is what makes this a test of replacement at all: without it the
// failing resource may be provisioned first, the update dies before the queue
// is ever touched, and the assertions below hold no matter which order
// replacement uses.
const replaceThenFailTemplate = `{
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "%s-v2"}
    },
    "Doomed": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": "Queue",
      "Properties": {
        "FunctionName": "%s-doomed",
        "Role": "arn:aws:iam::000000000000:role/lambda-role"
      }
    }
  }
}`

const replaceInitialTemplate = `{
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "%s-v1"}
    }
  }
}`

func TestUpdateStack_failedReplacementKeepsTheOriginalResource(t *testing.T) {
	// Given: a stack with a named queue
	srv := helpers.NewTestServer(t)
	stackName := "replace-rollback"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(replaceInitialTemplate, stackName)},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	originalQueue := stackName + "-v1"
	assertQueueExists(t, srv, originalQueue, true)

	// When: an update renames the queue (forcing replacement) and also adds a
	// resource that cannot be created, so the update fails after the
	// replacement has happened
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(replaceThenFailTemplate, stackName, stackName)},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: the original queue survives. Deleting it before creating the
	// replacement would have destroyed it with nothing to roll back to.
	assertQueueExists(t, srv, originalQueue, true)

	// And: the replacement created during the failed update is gone
	assertQueueExists(t, srv, stackName+"-v2", false)
}

func TestUpdateStack_successfulReplacementDeletesTheOriginal(t *testing.T) {
	// Given: a stack with a named queue
	srv := helpers.NewTestServer(t)
	stackName := "replace-success"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(replaceInitialTemplate, stackName)},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	assertQueueExists(t, srv, stackName+"-v1", true)

	// When: the queue is renamed, forcing a replacement that succeeds
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "` + stackName + `-v2"}
    }
  }
}`},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the new queue exists and the superseded one has been cleaned up —
	// deferring the delete must not mean forgetting it
	assertQueueExists(t, srv, stackName+"-v2", true)
	assertQueueExists(t, srv, stackName+"-v1", false)
}

// assertQueueExists checks the queue's presence through the SQS API, i.e. the
// real service state rather than CloudFormation's bookkeeping.
func assertQueueExists(t *testing.T, srv *helpers.TestServer, queueName string, want bool) {
	t.Helper()
	resp := sqsJSONCall(t, srv, "ListQueues", map[string]any{})
	defer resp.Body.Close()
	var out struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, resp, &out)
	found := false
	for _, u := range out.QueueUrls {
		if strings.HasSuffix(u, "/"+queueName) {
			found = true
			break
		}
	}
	if found != want {
		verb := "not to exist"
		if want {
			verb = "to exist"
		}
		t.Errorf("expected queue %q %s; queues=%v", queueName, verb, out.QueueUrls)
	}
}

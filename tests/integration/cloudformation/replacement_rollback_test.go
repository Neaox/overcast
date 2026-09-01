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
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
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

// TestUpdateStack_reportsTheCleanupPhase pins the status sequence AWS exposes.
// An update does not go straight from UPDATE_IN_PROGRESS to UPDATE_COMPLETE:
// removing what the update superseded is a separate, observable phase, which
// is why a stack blocked by a resource that will not delete is visibly stuck
// in UPDATE_COMPLETE_CLEANUP_IN_PROGRESS rather than looking mid-update.
func TestUpdateStack_reportsTheCleanupPhase(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "cleanup-phase"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(replaceInitialTemplate, stackName)},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: an update replaces the queue
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

	// Then: the cleanup phase was reported on the way there
	eventsResp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
	defer eventsResp.Body.Close()
	statuses := describeStackEventStatuses(t, eventsResp)
	if !slices.Contains(statuses, "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS") {
		t.Errorf("stack events never reported UPDATE_COMPLETE_CLEANUP_IN_PROGRESS; got %v", statuses)
	}
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

// TestUpdateStack_rollbackKeepsTheStacksResourceList covers what the stack
// still knows about itself after a failed update. Rollback restores the
// pre-update resource list, so a resource that was successfully updated before
// the failure must still be listed — pointing at the physical resource that
// actually exists. Losing it would leave the stack believing it owns nothing
// while the resource is still there, and the next update would try to create
// it again on top of itself.
func TestUpdateStack_rollbackKeepsTheStacksResourceList(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "rollback-resource-list"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(replaceInitialTemplate, stackName)},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(replaceThenFailTemplate, stackName, stackName)},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	resources := describeStackResourceIDs(t, srv, stackName)
	physID, listed := resources["Queue"]
	if !listed {
		t.Fatalf("stack lost the Queue resource after rollback; resources=%v", resources)
	}
	// And it points at the original, which is the one that still exists.
	if !strings.HasSuffix(physID, stackName+"-v1") {
		t.Errorf("Queue physical ID = %q, want the original %q", physID, stackName+"-v1")
	}
}

// describeStackResourceIDs maps logical ID → physical ID for a stack.
func describeStackResourceIDs(t *testing.T, srv *helpers.TestServer, stackName string) map[string]string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackResources", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	body := readBody(t, resp)
	var result struct {
		Members []struct {
			LogicalID  string `xml:"LogicalResourceId"`
			PhysicalID string `xml:"PhysicalResourceId"`
		} `xml:"DescribeStackResourcesResult>StackResources>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStackResources: %v\nbody: %s", err, body)
	}
	out := map[string]string{}
	for _, m := range result.Members {
		out[m.LogicalID] = m.PhysicalID
	}
	return out
}

// UpdateReplacePolicy=Retain keeps the original when a replacement supersedes
// it. That is about the *original*: the replacement the failed update created
// is still the update's own doing, so rollback removes it either way. Retaining
// the original and leaking the replacement would leave two live resources and
// no way to tell which the stack owns.
const retainOnReplaceTemplate = `{
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "UpdateReplacePolicy": "Retain",
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

const retainInitialTemplate = `{
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "UpdateReplacePolicy": "Retain",
      "Properties": {"QueueName": "%s-v1"}
    }
  }
}`

func TestUpdateStack_retainedOriginalSurvivesAndReplacementIsRemoved(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "retain-replace"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(retainInitialTemplate, stackName)},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: a replacement is forced and the update then fails
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(retainOnReplaceTemplate, stackName, stackName)},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: the retained original is still there, and so is the stack's claim on it
	assertQueueExists(t, srv, stackName+"-v1", true)
	// And: the replacement the failed update created was removed
	assertQueueExists(t, srv, stackName+"-v2", false)
}

func TestUpdateStack_retainedOriginalOutlivesASuccessfulReplacement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "retain-success"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(retainInitialTemplate, stackName)},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the replacement succeeds
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "UpdateReplacePolicy": "Retain",
      "Properties": {"QueueName": "` + stackName + `-v2"}
    }
  }
}`},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: cleanup left the retained original alone — that is what Retain means
	assertQueueExists(t, srv, stackName+"-v1", true)
	assertQueueExists(t, srv, stackName+"-v2", true)
}

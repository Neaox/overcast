package cloudformation_test

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateStack_fastStack(t *testing.T) {
	// Given: a running CloudFormation service
	srv := helpers.NewTestServer(t)
	stackName := "fast-create-stack"

	// When: a small stack is created
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{minimalTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the immediate describe sees the terminal status before an SDK waiter sleeps
	if got := describeStackStatus(t, srv, stackName); got != "CREATE_COMPLETE" {
		t.Fatalf("expected immediate CREATE_COMPLETE, got %q", got)
	}
}

func TestUpdateStack_fastStack(t *testing.T) {
	// Given: an existing small stack
	srv := helpers.NewTestServer(t)
	stackName := "fast-update-stack"
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{minimalTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the stack is updated
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{minimalTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)

	// Then: the immediate describe sees the terminal update status
	if got := describeStackStatus(t, srv, stackName); got != "UPDATE_COMPLETE" {
		t.Fatalf("expected immediate UPDATE_COMPLETE, got %q", got)
	}
}

func TestDeleteStack_fastStack(t *testing.T) {
	// Given: an existing small stack
	srv := helpers.NewTestServer(t)
	stackName := "fast-delete-stack"
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{minimalTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the stack is deleted
	deleteResp := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{stackName},
	})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)

	// Then: the immediate describe sees the terminal delete status
	if got := describeStackStatus(t, srv, stackName); got != "DELETE_COMPLETE" {
		t.Fatalf("expected immediate DELETE_COMPLETE, got %q", got)
	}
}

func TestExecuteChangeSet_fastStack(t *testing.T) {
	// Given: a CREATE change set for a small stack
	srv := helpers.NewTestServer(t)
	changeSet := createChangeSet(t, srv, "fast-changeset-stack", "fast-create", "CREATE")

	// When: the change set is executed
	executeResp := cfnQuery(t, srv, "ExecuteChangeSet", url.Values{
		"StackName":     []string{changeSet.StackName},
		"ChangeSetName": []string{changeSet.Name},
	})
	defer executeResp.Body.Close()
	helpers.AssertStatus(t, executeResp, http.StatusOK)

	// Then: the immediate stack and change set describes see terminal statuses
	if got := describeStackStatus(t, srv, changeSet.StackName); got != "CREATE_COMPLETE" {
		t.Fatalf("expected immediate CREATE_COMPLETE after ExecuteChangeSet, got %q", got)
	}
	if got := describeChangeSetExecutionStatus(t, srv, changeSet.StackName, changeSet.Name); got != "EXECUTE_COMPLETE" {
		t.Fatalf("expected immediate EXECUTE_COMPLETE, got %q", got)
	}
}

func TestDescribeStackEvents_fastStack(t *testing.T) {
	// Given: a stack that completes through the synchronous fast path
	srv := helpers.NewTestServer(t)
	stackName := "fast-events-stack"
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{minimalTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: events are described after create returns
	eventsResp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{stackName},
	})
	defer eventsResp.Body.Close()
	helpers.AssertStatus(t, eventsResp, http.StatusOK)
	statuses := describeStackEventStatuses(t, eventsResp)

	// Then: the full stack status history is still present
	if !slices.Contains(statuses, "CREATE_IN_PROGRESS") {
		t.Fatalf("expected CREATE_IN_PROGRESS event, got %v", statuses)
	}
	if !slices.Contains(statuses, "CREATE_COMPLETE") {
		t.Fatalf("expected CREATE_COMPLETE event, got %v", statuses)
	}
}

func describeStackStatus(t *testing.T, srv *helpers.TestServer, stackName string) string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": []string{stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var result struct {
		Status string `xml:"DescribeStacksResult>Stacks>member>StackStatus"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStacksResponse: %v\nbody: %s", err, body)
	}
	return result.Status
}

func describeChangeSetExecutionStatus(t *testing.T, srv *helpers.TestServer, stackName, changeSetName string) string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeChangeSet", url.Values{
		"StackName":     []string{stackName},
		"ChangeSetName": []string{changeSetName},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var result struct {
		ExecutionStatus string `xml:"DescribeChangeSetResult>ExecutionStatus"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeChangeSetResponse: %v\nbody: %s", err, body)
	}
	return result.ExecutionStatus
}

func describeStackEventStatuses(t *testing.T, resp *http.Response) []string {
	t.Helper()
	body := readBody(t, resp)
	var result struct {
		Statuses []string `xml:"DescribeStackEventsResult>StackEvents>member>ResourceStatus"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStackEventsResponse: %v\nbody: %s", err, body)
	}
	return result.Statuses
}

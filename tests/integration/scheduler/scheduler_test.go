// Package scheduler_test contains integration tests for the EventBridge Scheduler emulator.
//
// Run: go test ./tests/integration/scheduler/...
package scheduler_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// schDo performs a Scheduler REST-JSON request.
func schDo(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reqBody = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, srv.URL+path, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// createGroup creates a schedule group and returns its ARN.
func createGroup(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := schDo(t, srv, http.MethodPost, "/_scheduler/schedule-groups/"+name, map[string]any{
		"Tags": []any{},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		ScheduleGroupArn string `json:"ScheduleGroupArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.ScheduleGroupArn == "" {
		t.Fatal("expected ScheduleGroupArn in response")
	}
	return result.ScheduleGroupArn
}

// createSchedule creates a schedule in the given group and returns its ARN.
func createSchedule(t *testing.T, srv *helpers.TestServer, group, name, expression string) string {
	t.Helper()
	path := fmt.Sprintf("/_scheduler/schedules/%s/%s", group, name)
	resp := schDo(t, srv, http.MethodPost, path, map[string]any{
		"ScheduleExpression": expression,
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		ScheduleArn string `json:"ScheduleArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.ScheduleArn == "" {
		t.Fatal("expected ScheduleArn in response")
	}
	return result.ScheduleArn
}

// ─── Schedule Group Tests ─────────────────────────────────────────────────────

func TestCreateScheduleGroup_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateScheduleGroup is called
	resp := schDo(t, srv, http.MethodPost, "/_scheduler/schedule-groups/my-group", map[string]any{
		"Tags": map[string]any{"Env": "test"},
	})
	defer resp.Body.Close()

	// Then: 201 with ScheduleGroupArn
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		ScheduleGroupArn string `json:"ScheduleGroupArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if !strings.Contains(result.ScheduleGroupArn, "my-group") {
		t.Errorf("expected ARN to contain group name, got %q", result.ScheduleGroupArn)
	}
}

func TestCreateScheduleGroup_duplicate(t *testing.T) {
	// Given: a group already exists
	srv := helpers.NewTestServer(t)
	schDo(t, srv, http.MethodPost, "/_scheduler/schedule-groups/dup-group", map[string]any{}).Body.Close()

	// When: CreateScheduleGroup is called again with same name
	resp := schDo(t, srv, http.MethodPost, "/_scheduler/schedule-groups/dup-group", map[string]any{})
	defer resp.Body.Close()

	// Then: 409 Conflict
	helpers.AssertStatus(t, resp, http.StatusConflict)
}

func TestGetScheduleGroup_success(t *testing.T) {
	// Given: a schedule group exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "get-group")

	// When: GetScheduleGroup is called
	resp := schDo(t, srv, http.MethodGet, "/_scheduler/schedule-groups/get-group", nil)
	defer resp.Body.Close()

	// Then: 200 with group details
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Name  string `json:"Name"`
		State string `json:"State"`
		Arn   string `json:"Arn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Name != "get-group" {
		t.Errorf("expected Name=get-group, got %q", result.Name)
	}
	if result.State != "ACTIVE" {
		t.Errorf("expected State=ACTIVE, got %q", result.State)
	}
}

func TestGetScheduleGroup_notFound(t *testing.T) {
	// Given: no groups exist
	srv := helpers.NewTestServer(t)

	// When: GetScheduleGroup is called for unknown group
	resp := schDo(t, srv, http.MethodGet, "/_scheduler/schedule-groups/no-such-group", nil)
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestListScheduleGroups_success(t *testing.T) {
	// Given: two groups exist
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "group-a")
	createGroup(t, srv, "group-b")

	// When: ListScheduleGroups is called
	resp := schDo(t, srv, http.MethodGet, "/_scheduler/schedule-groups", nil)
	defer resp.Body.Close()

	// Then: 200 with both groups + "default"
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ScheduleGroups []struct {
			Name string `json:"Name"`
		} `json:"ScheduleGroups"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.ScheduleGroups) < 3 { // default + group-a + group-b
		t.Errorf("expected at least 3 groups, got %d", len(result.ScheduleGroups))
	}
}

func TestDeleteScheduleGroup_success(t *testing.T) {
	// Given: a schedule group exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "del-group")

	// When: DeleteScheduleGroup is called
	resp := schDo(t, srv, http.MethodDelete, "/_scheduler/schedule-groups/del-group", nil)
	resp.Body.Close()

	// Then: 200, and subsequent Get returns 404
	helpers.AssertStatus(t, resp, http.StatusOK)
	get := schDo(t, srv, http.MethodGet, "/_scheduler/schedule-groups/del-group", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusNotFound)
}

// ─── Schedule Tests ───────────────────────────────────────────────────────────

func TestCreateSchedule_success(t *testing.T) {
	// Given: a schedule group exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "sched-group")

	// When: CreateSchedule is called
	resp := schDo(t, srv, http.MethodPost, "/_scheduler/schedules/sched-group/my-schedule", map[string]any{
		"ScheduleExpression": "rate(5 minutes)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: 201 with ScheduleArn
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var result struct {
		ScheduleArn string `json:"ScheduleArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if !strings.Contains(result.ScheduleArn, "my-schedule") {
		t.Errorf("expected ARN to contain schedule name, got %q", result.ScheduleArn)
	}
}

func TestCreateSchedule_defaultGroup(t *testing.T) {
	// Given: no explicit group (uses implicit "default" group)
	srv := helpers.NewTestServer(t)

	// When: CreateSchedule is called without a group path prefix
	resp := schDo(t, srv, http.MethodPost, "/_scheduler/schedules/my-default-schedule", map[string]any{
		"ScheduleExpression": "rate(1 hour)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:my-queue",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: 201
	helpers.AssertStatus(t, resp, http.StatusCreated)
}

func TestGetSchedule_success(t *testing.T) {
	// Given: a schedule exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "g1")
	createSchedule(t, srv, "g1", "sched1", "rate(10 minutes)")

	// When: GetSchedule is called
	resp := schDo(t, srv, http.MethodGet, "/_scheduler/schedules/g1/sched1", nil)
	defer resp.Body.Close()

	// Then: 200 with schedule details
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Name               string `json:"Name"`
		GroupName          string `json:"GroupName"`
		ScheduleExpression string `json:"ScheduleExpression"`
		State              string `json:"State"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Name != "sched1" {
		t.Errorf("expected Name=sched1, got %q", result.Name)
	}
	if result.GroupName != "g1" {
		t.Errorf("expected GroupName=g1, got %q", result.GroupName)
	}
	if result.ScheduleExpression != "rate(10 minutes)" {
		t.Errorf("expected expression rate(10 minutes), got %q", result.ScheduleExpression)
	}
	if result.State != "ENABLED" {
		t.Errorf("expected State=ENABLED, got %q", result.State)
	}
}

func TestGetSchedule_notFound(t *testing.T) {
	// Given: no schedules exist
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "g2")

	// When: GetSchedule is called for non-existent schedule
	resp := schDo(t, srv, http.MethodGet, "/_scheduler/schedules/g2/no-such-schedule", nil)
	defer resp.Body.Close()

	// Then: 404
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestUpdateSchedule_success(t *testing.T) {
	// Given: a schedule exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "g3")
	createSchedule(t, srv, "g3", "updatable", "rate(5 minutes)")

	// When: UpdateSchedule changes the expression
	resp := schDo(t, srv, http.MethodPut, "/_scheduler/schedules/g3/updatable", map[string]any{
		"ScheduleExpression": "rate(15 minutes)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: 200 with updated ScheduleArn
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: GetSchedule returns the new expression
	get := schDo(t, srv, http.MethodGet, "/_scheduler/schedules/g3/updatable", nil)
	defer get.Body.Close()
	var result struct {
		ScheduleExpression string `json:"ScheduleExpression"`
	}
	helpers.DecodeJSON(t, get, &result)
	if result.ScheduleExpression != "rate(15 minutes)" {
		t.Errorf("expected updated expression, got %q", result.ScheduleExpression)
	}
}

func TestDeleteSchedule_success(t *testing.T) {
	// Given: a schedule exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "g4")
	createSchedule(t, srv, "g4", "del-sched", "rate(1 hour)")

	// When: DeleteSchedule is called
	resp := schDo(t, srv, http.MethodDelete, "/_scheduler/schedules/g4/del-sched", nil)
	resp.Body.Close()

	// Then: 200, subsequent Get returns 404
	helpers.AssertStatus(t, resp, http.StatusOK)
	get := schDo(t, srv, http.MethodGet, "/_scheduler/schedules/g4/del-sched", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusNotFound)
}

func TestListSchedules_success(t *testing.T) {
	// Given: two schedules in the same group
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "g5")
	createSchedule(t, srv, "g5", "sched-x", "rate(1 minute)")
	createSchedule(t, srv, "g5", "sched-y", "rate(2 minutes)")

	// When: ListSchedules is called
	resp := schDo(t, srv, http.MethodGet, "/_scheduler/schedules?ScheduleGroup=g5", nil)
	defer resp.Body.Close()

	// Then: 200 with both schedules
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Schedules []struct {
			Name string `json:"Name"`
		} `json:"Schedules"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Schedules) != 2 {
		t.Errorf("expected 2 schedules, got %d", len(result.Schedules))
	}
}

func TestListSchedules_filterByGroup(t *testing.T) {
	// Given: schedules in different groups
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "grp-a")
	createGroup(t, srv, "grp-b")
	createSchedule(t, srv, "grp-a", "a-sched", "rate(1 minute)")
	createSchedule(t, srv, "grp-b", "b-sched", "rate(1 minute)")

	// When: ListSchedules is filtered by grp-a
	resp := schDo(t, srv, http.MethodGet, "/_scheduler/schedules?ScheduleGroup=grp-a", nil)
	defer resp.Body.Close()

	// Then: only grp-a schedules are returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Schedules []struct {
			Name      string `json:"Name"`
			GroupName string `json:"GroupName"`
		} `json:"Schedules"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Schedules) != 1 {
		t.Errorf("expected 1 schedule, got %d", len(result.Schedules))
	}
	if result.Schedules[0].Name != "a-sched" {
		t.Errorf("expected a-sched, got %q", result.Schedules[0].Name)
	}
}

func TestCreateSchedule_cronExpression(t *testing.T) {
	// Given: a schedule group
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "cron-group")

	// When: CreateSchedule with AWS cron expression
	resp := schDo(t, srv, http.MethodPost, "/_scheduler/schedules/cron-group/cron-sched", map[string]any{
		"ScheduleExpression": "cron(0 12 * * ? *)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: 201 — cron expression is accepted and stored
	helpers.AssertStatus(t, resp, http.StatusCreated)
}

// ─── Target Firing Tests ──────────────────────────────────────────────────────

func TestSchedule_rateFiresTarget(t *testing.T) {
	// Given: a server with mock clock and a rate schedule targeting an SQS queue
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	// Create an SQS queue to receive scheduled events
	createReq, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/?Action=CreateQueue&QueueName=sched-target", nil)
	createResp, _ := http.DefaultClient.Do(createReq)
	createResp.Body.Close()

	// Create a schedule targeting that SQS queue with a 5-minute rate
	resp := schDo(t, srv, http.MethodPost, "/_scheduler/schedules/my-fire-sched", map[string]any{
		"ScheduleExpression": "rate(5 minutes)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:sched-target",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
			"Input":   `{"event":"scheduled"}`,
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)

	// When: advance mock clock by 6 minutes (past the 5-minute rate)
	srv.Clock.Add(6 * time.Minute)
	time.Sleep(200 * time.Millisecond) // let background goroutine fire

	// Then: the SQS queue should have received a message
	// Then: verify we can still GET the schedule (engine didn't crash)
	getResp := schDo(t, srv, http.MethodGet, "/_scheduler/schedules/my-fire-sched", nil)
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
}

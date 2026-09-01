package scheduler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// Input validation, group lifecycle and pagination — the parts of the service
// that only a real client exercises. Before #793 nothing reached these paths
// from an SDK at all, so they had never been driven by one.

func TestCreateSchedule_rejectsInvalidName(t *testing.T) {
	// Given: an emulator
	srv := helpers.NewTestServer(t)

	// When: the schedule name is outside AWS's [0-9a-zA-Z-_.] alphabet
	resp := schDo(t, srv, http.MethodPost, "/schedules/not%20valid", map[string]any{
		"ScheduleExpression": "rate(5 minutes)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:my-queue",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: ValidationException, not a schedule nobody can address
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestCreateSchedule_rejectsUnparseableExpression(t *testing.T) {
	// Given: an emulator
	srv := helpers.NewTestServer(t)

	// When: the expression is not one the engine can evaluate
	resp := schDo(t, srv, http.MethodPost, "/schedules/bad-expression", map[string]any{
		"ScheduleExpression": "every 5 minutes",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:my-queue",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: it is refused at create rather than accepted and logged as an
	// engine error on every tick forever
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")

	get := schDo(t, srv, http.MethodGet, "/schedules/bad-expression", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusNotFound)
}

func TestCreateSchedule_rejectsMissingFlexibleTimeWindow(t *testing.T) {
	// Given: an emulator
	srv := helpers.NewTestServer(t)

	// When: FlexibleTimeWindow, which AWS marks required, is omitted
	resp := schDo(t, srv, http.MethodPost, "/schedules/no-window", map[string]any{
		"ScheduleExpression": "rate(5 minutes)",
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:my-queue",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestCreateSchedule_rejectsUnknownState(t *testing.T) {
	// Given: an emulator
	srv := helpers.NewTestServer(t)

	// When: State is outside the modeled enum
	resp := schDo(t, srv, http.MethodPost, "/schedules/bad-state", map[string]any{
		"ScheduleExpression": "rate(5 minutes)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"State":              "PAUSED",
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:my-queue",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: ValidationException — an unknown state would be stored and would
	// silently stop the schedule ever firing
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestCreateSchedule_keepsStartAndEndDate(t *testing.T) {
	// Given: an emulator
	srv := helpers.NewTestServer(t)

	// When: the schedule is created over Scheduler's own REST route — the only
	// wire it answers on since #1226 retired the invented "Scheduler.<Op>"
	// X-Amz-Target dispatch CloudFormation's provisioner used to speak.
	resp := schDo(t, srv, http.MethodPost, "/schedules/dated", map[string]any{
		"ScheduleExpression": "rate(1 hour)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"StartDate":          "2030-01-01T00:00:00Z",
		"EndDate":            "2031-01-01T00:00:00Z",
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:my-queue",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: both dates survive.
	get := schDo(t, srv, http.MethodGet, "/schedules/dated", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var stored struct {
		StartDate string `json:"StartDate"`
		EndDate   string `json:"EndDate"`
	}
	helpers.DecodeJSON(t, get, &stored)
	if !strings.HasPrefix(stored.StartDate, "2030-01-01") {
		t.Errorf("expected StartDate to survive, got %q", stored.StartDate)
	}
	if !strings.HasPrefix(stored.EndDate, "2031-01-01") {
		t.Errorf("expected EndDate to survive, got %q", stored.EndDate)
	}
}

// TestXAmzTarget_noLongerDispatches pins #1226: Scheduler is restJson1 and the
// pinned models give it no X-Amz-Target prefix, so a request shaped like one
// must get the same honest "unknown operation" answer any nonsense target
// gets — never a 200, and never silently proxied to another service.
func TestXAmzTarget_noLongerDispatches(t *testing.T) {
	srv := helpers.NewTestServer(t)

	body, err := json.Marshal(map[string]any{
		"Name":               "ghost",
		"ScheduleExpression": "rate(1 hour)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:my-queue",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "Scheduler.CreateSchedule")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("X-Amz-Target: Scheduler.CreateSchedule answered 200 — the invented wire is still reachable")
	}
	helpers.AssertJSONError(t, resp, "UnknownOperationException")

	// And nothing was actually created on the strength of the invented call.
	get := schDo(t, srv, http.MethodGet, "/schedules/ghost", nil)
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusNotFound)
}

func TestDeleteScheduleGroup_deletesTheSchedulesInIt(t *testing.T) {
	// Given: a group with two schedules in it
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "doomed")
	createSchedule(t, srv, "doomed", "one", "rate(1 minute)")
	createSchedule(t, srv, "doomed", "two", "rate(1 minute)")

	// When: the group is deleted
	del := schDo(t, srv, http.MethodDelete, "/schedule-groups/doomed", nil)
	del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: its schedules go with it, as they do on AWS. Left behind they were
	// unreachable through the API and still fired on every engine tick.
	for _, name := range []string{"one", "two"} {
		get := schDo(t, srv, http.MethodGet, "/schedules/"+name+"?groupName=doomed", nil)
		helpers.AssertStatus(t, get, http.StatusNotFound)
		get.Body.Close()
	}

	list := schDo(t, srv, http.MethodGet, "/schedules", nil)
	defer list.Body.Close()
	var listed struct {
		Schedules []struct {
			Name string `json:"Name"`
		} `json:"Schedules"`
	}
	helpers.DecodeJSON(t, list, &listed)
	if len(listed.Schedules) != 0 {
		t.Errorf("expected no schedules left, got %+v", listed.Schedules)
	}
}

func TestListSchedules_paginates(t *testing.T) {
	// Given: five schedules in the default group
	srv := helpers.NewTestServer(t)
	for i := range 5 {
		createSchedule(t, srv, "default", fmt.Sprintf("page-%d", i), "rate(1 hour)")
	}

	// When: the client asks for two at a time
	seen := map[string]bool{}
	token := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		path := "/schedules?MaxResults=2"
		if token != "" {
			path += "&NextToken=" + token
		}
		resp := schDo(t, srv, http.MethodGet, path, nil)
		helpers.AssertStatus(t, resp, http.StatusOK)
		var page struct {
			Schedules []struct {
				Name string `json:"Name"`
			} `json:"Schedules"`
			NextToken string `json:"NextToken"`
		}
		helpers.DecodeJSON(t, resp, &page)
		resp.Body.Close()
		if len(page.Schedules) > 2 {
			t.Fatalf("MaxResults=2 returned %d schedules", len(page.Schedules))
		}
		for _, sc := range page.Schedules {
			seen[sc.Name] = true
		}
		token = page.NextToken
		if token == "" {
			break
		}
	}

	// Then: every schedule appears exactly once across the pages
	if len(seen) != 5 {
		t.Errorf("expected 5 distinct schedules across pages, got %d: %v", len(seen), seen)
	}
}

func TestListSchedules_rejectsUnusableNextToken(t *testing.T) {
	// Given: an emulator with a schedule
	srv := helpers.NewTestServer(t)
	createSchedule(t, srv, "default", "only", "rate(1 hour)")

	// When: the client sends a token that does not decode
	resp := schDo(t, srv, http.MethodGet, "/schedules?NextToken=not-a-token", nil)
	defer resp.Body.Close()

	// Then: ValidationException, rather than silently restarting at page one —
	// which an SDK paginator would read as a legitimate page and loop on
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestListSchedules_filtersByNamePrefixAndState(t *testing.T) {
	// Given: schedules with different names and states
	srv := helpers.NewTestServer(t)
	createSchedule(t, srv, "default", "keep-one", "rate(1 hour)")
	createSchedule(t, srv, "default", "keep-two", "rate(1 hour)")
	createSchedule(t, srv, "default", "drop-one", "rate(1 hour)")
	disable := schDo(t, srv, http.MethodPut, "/schedules/keep-two", map[string]any{
		"ScheduleExpression": "rate(1 hour)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"State":              "DISABLED",
		"Target": map[string]any{
			"Arn":     "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	disable.Body.Close()
	helpers.AssertStatus(t, disable, http.StatusOK)

	// When: the client filters on both NamePrefix and State
	resp := schDo(t, srv, http.MethodGet, "/schedules?NamePrefix=keep&State=ENABLED", nil)
	defer resp.Body.Close()

	// Then: only the enabled "keep" schedule comes back
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Schedules []struct {
			Name string `json:"Name"`
		} `json:"Schedules"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Schedules) != 1 || result.Schedules[0].Name != "keep-one" {
		t.Errorf("expected only keep-one, got %+v", result.Schedules)
	}
}

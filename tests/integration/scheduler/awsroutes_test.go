package scheduler_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// EventBridge Scheduler addresses a schedule by name alone. Its group is a body
// member on CreateSchedule and UpdateSchedule and a ?groupName query parameter
// on GetSchedule and DeleteSchedule — never a path segment — and its tag
// operations sit in the /tags/{ResourceArn} space shared with API Gateway, EKS
// and Pipes. Those three bindings are the ones an emulator is most likely to
// approximate rather than reproduce, so they are asserted here in their own
// right; the rest of the suite exercises the same paths as ordinary CRUD.
//
// Every one of them answered 501 for the emulator's first 33 releases, because
// the routes were mounted on an invented /_scheduler prefix and the whole suite
// drove that prefix too. See issue #793.

func TestCreateSchedule_groupNameIsABodyMember(t *testing.T) {
	// Given: a named schedule group
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "aws-grp")

	// When: CreateSchedule names the group in the body, at POST /schedules/{Name}
	resp := schDo(t, srv, http.MethodPost, "/schedules/grouped", map[string]any{
		"GroupName":          "aws-grp",
		"ScheduleExpression": "rate(5 minutes)",
		"FlexibleTimeWindow": map[string]any{"Mode": "OFF"},
		"Target": map[string]any{
			"Arn":     "arn:aws:sqs:us-east-1:000000000000:my-queue",
			"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role",
		},
	})
	defer resp.Body.Close()

	// Then: the schedule lands in that group, not in "default"
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ScheduleArn string `json:"ScheduleArn"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if !strings.HasSuffix(result.ScheduleArn, ":schedule/aws-grp/grouped") {
		t.Errorf("expected an aws-grp schedule ARN, got %q", result.ScheduleArn)
	}
}

func TestGetSchedule_groupNameIsAQueryParameter(t *testing.T) {
	// Given: the same schedule name in two different groups
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "read-a")
	createGroup(t, srv, "read-b")
	createSchedule(t, srv, "read-a", "shared-name", "rate(10 minutes)")
	createSchedule(t, srv, "read-b", "shared-name", "rate(20 minutes)")

	// When: GetSchedule selects one of them with ?groupName=
	resp := schDo(t, srv, http.MethodGet, "/schedules/shared-name?groupName=read-b", nil)
	defer resp.Body.Close()

	// Then: the query parameter, not the name, decides which one comes back
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		GroupName          string `json:"GroupName"`
		ScheduleExpression string `json:"ScheduleExpression"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.GroupName != "read-b" || result.ScheduleExpression != "rate(20 minutes)" {
		t.Errorf("expected read-b/rate(20 minutes), got %q/%q", result.GroupName, result.ScheduleExpression)
	}
}

func TestSchedulerTags_sharedTagsPathSpace(t *testing.T) {
	// Given: a schedule group, whose ARN is the tag resource
	srv := helpers.NewTestServer(t)
	arn := createGroup(t, srv, "tag-grp")
	escaped := url.PathEscape(arn)

	// When: TagResource is sent to POST /tags/{ResourceArn}
	tag := schDo(t, srv, http.MethodPost, "/tags/"+escaped, map[string]any{
		"Tags": map[string]string{"Env": "test"},
	})
	tag.Body.Close()
	helpers.AssertStatus(t, tag, http.StatusOK)

	// Then: ListTagsForResource returns them — the shared /tags dispatcher
	// routed on the ARN's own "scheduler" segment rather than falling through
	// to API Gateway's tag store.
	list := schDo(t, srv, http.MethodGet, "/tags/"+escaped, nil)
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var result struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, list, &result)
	if result.Tags["Env"] != "test" {
		t.Errorf("expected Env=test, got %+v", result.Tags)
	}

	// And: UntagResource removes them again.
	untag := schDo(t, srv, http.MethodDelete, "/tags/"+escaped+"?TagKeys=Env", nil)
	untag.Body.Close()
	helpers.AssertStatus(t, untag, http.StatusOK)

	after := schDo(t, srv, http.MethodGet, "/tags/"+escaped, nil)
	defer after.Body.Close()
	var remaining struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, after, &remaining)
	if len(remaining.Tags) != 0 {
		t.Errorf("expected no tags after UntagResource, got %+v", remaining.Tags)
	}
}

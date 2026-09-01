// Integration coverage for issue #537: CloudFormation's AWS::Scheduler::Schedule
// handler parsed Name, GroupName, ScheduleExpression, State, FlexibleTimeWindow
// and Target into CreateSchedule and dropped everything else — Description,
// ScheduleExpressionTimezone, StartDate, EndDate and KmsKeyArn — while the
// scheduler service itself already stores and returns every one of them
// (ScheduleExpressionTimezone and StartDate/EndDate are also honoured by the
// dispatcher; see docs/services/scheduler.md). Update always forced full
// replacement instead of calling UpdateSchedule, so an in-place property
// change tore the schedule down and rebuilt it.
//
// These tests provision through CloudFormation and read the result back
// through the scheduler service's own REST endpoints (GetSchedule,
// GetScheduleGroup, ListTagsForResource) — never through CloudFormation's own
// view of the resource, which would just reflect the template back.
package cloudformation_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// schedulerRESTCall issues a direct request against the scheduler service's
// own REST-JSON routes (not through CloudFormation), to read back what a
// CloudFormation-provisioned resource actually holds.
func schedulerRESTCall(t *testing.T, srv *helpers.TestServer, method, path string, body string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("schedulerRESTCall %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("schedulerRESTCall %s %s: %v", method, path, err)
	}
	return resp
}

const schedulerPropsTemplate = `{
	"Resources": {
		"Group": {
			"Type": "AWS::Scheduler::ScheduleGroup",
			"Properties": {
				"Name": "props-group",
				"Tags": [{"Key": "env", "Value": "test"}]
			}
		},
		"Schedule": {
			"Type": "AWS::Scheduler::Schedule",
			"DependsOn": ["Group"],
			"Properties": {
				"Name": "props-schedule",
				"GroupName": "props-group",
				"Description": "the original description",
				"ScheduleExpression": "rate(5 minutes)",
				"ScheduleExpressionTimezone": "Europe/London",
				"StartDate": "2030-01-01T00:00:00Z",
				"EndDate": "2031-01-01T00:00:00Z",
				"KmsKeyArn": "arn:aws:kms:us-east-1:000000000000:key/original-key",
				"State": "DISABLED",
				"FlexibleTimeWindow": {"Mode": "OFF"},
				"Target": {
					"Arn": "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
					"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role"
				}
			}
		}
	}
}`

// TestCFN_SchedulerSchedule_propertiesRoundTrip proves the five properties
// #537 named — Description, ScheduleExpressionTimezone, StartDate, EndDate
// and KmsKeyArn — reach CreateSchedule rather than being parsed into nothing.
func TestCFN_SchedulerSchedule_propertiesRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "scheduler-props-roundtrip"

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {schedulerPropsTemplate}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED"); status != "CREATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("create stack ended in %s: %s", status, readBody(t, events))
	}

	get := schedulerRESTCall(t, srv, http.MethodGet, "/schedules/props-schedule?groupName=props-group", "")
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var sc struct {
		Description                string `json:"Description"`
		ScheduleExpressionTimezone string `json:"ScheduleExpressionTimezone"`
		StartDate                  string `json:"StartDate"`
		EndDate                    string `json:"EndDate"`
		KmsKeyArn                  string `json:"KmsKeyArn"`
		CreationDate               string `json:"CreationDate"`
	}
	helpers.DecodeJSON(t, get, &sc)

	if sc.Description != "the original description" {
		t.Errorf("Description = %q, want it forwarded from the template", sc.Description)
	}
	if sc.ScheduleExpressionTimezone != "Europe/London" {
		t.Errorf("ScheduleExpressionTimezone = %q, want it forwarded from the template", sc.ScheduleExpressionTimezone)
	}
	if !strings.HasPrefix(sc.StartDate, "2030-01-01") {
		t.Errorf("StartDate = %q, want it forwarded from the template", sc.StartDate)
	}
	if !strings.HasPrefix(sc.EndDate, "2031-01-01") {
		t.Errorf("EndDate = %q, want it forwarded from the template", sc.EndDate)
	}
	if sc.KmsKeyArn != "arn:aws:kms:us-east-1:000000000000:key/original-key" {
		t.Errorf("KmsKeyArn = %q, want it forwarded from the template", sc.KmsKeyArn)
	}

	// And: the group's Tags survive too — #537 found no gap here, so this is a
	// sanity check rather than new coverage.
	groupGet := schedulerRESTCall(t, srv, http.MethodGet, "/schedule-groups/props-group", "")
	defer groupGet.Body.Close()
	helpers.AssertStatus(t, groupGet, http.StatusOK)
	var group struct {
		Arn string `json:"Arn"`
	}
	helpers.DecodeJSON(t, groupGet, &group)
	tagsGet := schedulerRESTCall(t, srv, http.MethodGet, "/tags/"+url.PathEscape(group.Arn), "")
	defer tagsGet.Body.Close()
	helpers.AssertStatus(t, tagsGet, http.StatusOK)
	var tags struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, tagsGet, &tags)
	found := false
	for _, tag := range tags.Tags {
		if tag.Key == "env" && tag.Value == "test" {
			found = true
		}
	}
	if !found {
		t.Errorf("ListTagsForResource = %+v, want the env=test tag from the template", tags.Tags)
	}
}

// TestCFN_SchedulerSchedule_updateAppliesInPlace proves a property-only
// change now calls UpdateSchedule instead of the previous unconditional
// errReplacementRequired, which tore the schedule down and rebuilt it (a new
// CreationDate) on every update.
func TestCFN_SchedulerSchedule_updateAppliesInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "scheduler-props-update"

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {schedulerPropsTemplate}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED"); status != "CREATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("create stack ended in %s: %s", status, readBody(t, events))
	}

	before := schedulerRESTCall(t, srv, http.MethodGet, "/schedules/props-schedule?groupName=props-group", "")
	defer before.Body.Close()
	var beforeResult struct {
		CreationDate string `json:"CreationDate"`
	}
	helpers.DecodeJSON(t, before, &beforeResult)
	if beforeResult.CreationDate == "" {
		t.Fatal("expected CreationDate after create")
	}

	updated := strings.NewReplacer(
		"the original description", "the updated description",
		"Europe/London", "America/New_York",
		"arn:aws:kms:us-east-1:000000000000:key/original-key", "arn:aws:kms:us-east-1:000000000000:key/updated-key",
	).Replace(schedulerPropsTemplate)

	resp = cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {updated}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE", "UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED", "UPDATE_ROLLBACK_FAILED"); status != "UPDATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("update stack ended in %s: %s", status, readBody(t, events))
	}

	after := schedulerRESTCall(t, srv, http.MethodGet, "/schedules/props-schedule?groupName=props-group", "")
	defer after.Body.Close()
	helpers.AssertStatus(t, after, http.StatusOK)
	var afterResult struct {
		Description                string `json:"Description"`
		ScheduleExpressionTimezone string `json:"ScheduleExpressionTimezone"`
		KmsKeyArn                  string `json:"KmsKeyArn"`
		CreationDate               string `json:"CreationDate"`
	}
	helpers.DecodeJSON(t, after, &afterResult)

	if afterResult.Description != "the updated description" {
		t.Errorf("Description = %q, want the updated value", afterResult.Description)
	}
	if afterResult.ScheduleExpressionTimezone != "America/New_York" {
		t.Errorf("ScheduleExpressionTimezone = %q, want the updated value", afterResult.ScheduleExpressionTimezone)
	}
	if afterResult.KmsKeyArn != "arn:aws:kms:us-east-1:000000000000:key/updated-key" {
		t.Errorf("KmsKeyArn = %q, want the updated value", afterResult.KmsKeyArn)
	}
	if afterResult.CreationDate != beforeResult.CreationDate {
		t.Errorf("CreationDate changed from %q to %q — the update replaced the schedule instead of applying in place",
			beforeResult.CreationDate, afterResult.CreationDate)
	}
}

// TestCFN_SchedulerSchedule_nameChangeReplaces proves Name still forces
// replacement — GroupName and Name are the only createOnly properties on
// AWS::Scheduler::Schedule, and the emulator's UpdateSchedule has nothing to
// locate the old record by once the name changes.
func TestCFN_SchedulerSchedule_nameChangeReplaces(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "scheduler-props-replace"

	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {schedulerPropsTemplate}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED"); status != "CREATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("create stack ended in %s: %s", status, readBody(t, events))
	}

	renamed := strings.Replace(schedulerPropsTemplate, `"Name": "props-schedule"`, `"Name": "props-schedule-renamed"`, 1)
	resp = cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {renamed}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE", "UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED", "UPDATE_ROLLBACK_FAILED"); status != "UPDATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("update stack ended in %s: %s", status, readBody(t, events))
	}

	oldGet := schedulerRESTCall(t, srv, http.MethodGet, "/schedules/props-schedule?groupName=props-group", "")
	defer oldGet.Body.Close()
	if oldGet.StatusCode != http.StatusNotFound {
		t.Errorf("old schedule status = %d, want 404 (replaced)", oldGet.StatusCode)
	}

	newGet := schedulerRESTCall(t, srv, http.MethodGet, "/schedules/props-schedule-renamed?groupName=props-group", "")
	defer newGet.Body.Close()
	helpers.AssertStatus(t, newGet, http.StatusOK)
}

// unnamedScheduleTemplate is the shape CDK's L2 Schedule emits: no Name, so
// CloudFormation mints one. Its %s is the description, so an update can change
// a property without touching anything else.
const unnamedScheduleTemplate = `{
	"Resources": {
		"Schedule": {
			"Type": "AWS::Scheduler::Schedule",
			"Properties": {
				"Description": "%s",
				"ScheduleExpression": "rate(5 minutes)",
				"FlexibleTimeWindow": {"Mode": "OFF"},
				"Target": {
					"Arn": "arn:aws:lambda:us-east-1:000000000000:function:my-fn",
					"RoleArn": "arn:aws:iam::000000000000:role/scheduler-role"
				}
			}
		}
	}
}`

// TestCFN_SchedulerSchedule_unnamedScheduleUpdatesInPlace covers the other
// half of the generated-name fix. Create now mints a name, but Update compared
// the template's Name against the previous template's, where an unnamed
// schedule's is the empty string on both sides — so it read as unchanged and
// re-issued UpdateSchedule with no name, back to the "/schedules/" the router
// answers 501 on. The comparison is against the physical ID now, which is the
// only place the generated name is recorded.
func TestCFN_SchedulerSchedule_unnamedScheduleUpdatesInPlace(t *testing.T) {
	// Given: a stack holding a schedule the template does not name
	srv := helpers.NewTestServer(t)
	stackName := "scheduler-unnamed-update"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(unnamedScheduleTemplate, "the original description")},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED"); status != "CREATE_COMPLETE" {
		t.Fatalf("create stack ended in %s; reasons:\n%s",
			status, strings.Join(describeStackEventReasons(t, srv, stackName), "\n"))
	}
	physicalID := describeStackResourceIDs(t, srv, stackName)["Schedule"]
	group, name, found := strings.Cut(physicalID, "/")
	if !found || name == "" {
		t.Fatalf("physical ID = %q, want %q and a generated name", physicalID, "default/…")
	}

	// When: a property changes and nothing else does
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(unnamedScheduleTemplate, "the updated description")},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE", "UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED", "UPDATE_ROLLBACK_FAILED"); status != "UPDATE_COMPLETE" {
		t.Fatalf("update stack ended in %s; reasons:\n%s",
			status, strings.Join(describeStackEventReasons(t, srv, stackName), "\n"))
	}

	// Then: the schedule kept its generated name and took the new property
	if got := describeStackResourceIDs(t, srv, stackName)["Schedule"]; got != physicalID {
		t.Errorf("physical ID = %q, want the original %q — the update replaced the schedule", got, physicalID)
	}
	get := schedulerRESTCall(t, srv, http.MethodGet,
		"/schedules/"+url.PathEscape(name)+"?"+url.Values{"groupName": {group}}.Encode(), "")
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	var sc struct {
		Description string `json:"Description"`
	}
	helpers.DecodeJSON(t, get, &sc)
	if sc.Description != "the updated description" {
		t.Errorf("Description = %q, want the updated value", sc.Description)
	}
}

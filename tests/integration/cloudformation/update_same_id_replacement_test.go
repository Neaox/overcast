package cloudformation_test

// update_same_id_replacement_test.go — a "replacement" that comes back with
// the same physical ID is not a replacement.
//
// Replacement creates the new resource before deleting the old one, and the
// deferred delete goes by physical ID. For resource types whose create is an
// upsert keyed by a caller-pinned name (CloudWatch PutMetricAlarm is the
// canonical case), the replacement create returns the *same* physical ID as
// the original. Deleting "the old resource" at cleanup — or "the replacement"
// during rollback — then destroys the one live resource while the stack
// reports success. Real CloudFormation cannot reach this state (it refuses to
// replace a custom-named resource without a name change), so the engine has
// to recognise it: same ID in, same ID out means the update was in place.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const pinnedAlarmTemplate = `{
  "Resources": {
    "Alarm": {
      "Type": "AWS::CloudWatch::Alarm",
      "Properties": {
        "AlarmName": "%s",
        "MetricName": "CPUUtilization",
        "Namespace": "AWS/EC2",
        "Statistic": "Average",
        "Period": 300,
        "EvaluationPeriods": 1,
        "Threshold": %s,
        "ComparisonOperator": "GreaterThanThreshold"
      }
    }
  }
}`

func TestUpdateStack_sameIDReplacement_resourceSurvivesCleanup(t *testing.T) {
	// Given: a stack with a pinned-name alarm
	srv := helpers.NewTestServer(t)
	stackName := "same-id-cleanup"
	alarmName := stackName + "-alarm"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(pinnedAlarmTemplate, alarmName, "80")},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: only the threshold changes
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(pinnedAlarmTemplate, alarmName, "90")},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the alarm still exists — the cleanup phase must not have deleted
	// it as "the superseded original"
	assertAlarmExists(t, srv, alarmName, true)

	// And: the changed property took effect
	resp, err := http.PostForm(srv.URL+"/", url.Values{
		"Action":              {"DescribeAlarms"},
		"Version":             {"2010-08-01"},
		"AlarmNames.member.1": {alarmName},
	})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	defer resp.Body.Close()
	if body := string(readBody(t, resp)); !strings.Contains(body, "<Threshold>90</Threshold>") && !strings.Contains(body, "<Threshold>90.0</Threshold>") {
		t.Errorf("alarm threshold was not updated to 90; body:\n%s", body)
	}
}

func TestUpdateStack_sameIDReplacement_resourceSurvivesRollback(t *testing.T) {
	// Given: a stack with a pinned-name alarm
	srv := helpers.NewTestServer(t)
	stackName := "same-id-rollback"
	alarmName := stackName + "-alarm"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(pinnedAlarmTemplate, alarmName, "80")},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: an update changes the alarm and then fails on a later resource
	// (Lambda with no Code/Runtime is rejected; DependsOn orders it after the
	// alarm so the alarm has already been touched when the update dies)
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "Alarm": {
      "Type": "AWS::CloudWatch::Alarm",
      "Properties": {
        "AlarmName": "` + alarmName + `",
        "MetricName": "CPUUtilization",
        "Namespace": "AWS/EC2",
        "Statistic": "Average",
        "Period": 300,
        "EvaluationPeriods": 1,
        "Threshold": 90,
        "ComparisonOperator": "GreaterThanThreshold"
      }
    },
    "Doomed": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": "Alarm",
      "Properties": {
        "FunctionName": "` + stackName + `-doomed",
        "Role": "arn:aws:iam::000000000000:role/lambda-role"
      }
    }
  }
}`},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: the alarm still exists — rollback deletes "the replacement", and
	// when the replacement's ID is the original's ID that delete would have
	// destroyed the resource the stack just rolled back to
	assertAlarmExists(t, srv, alarmName, true)
}

const recordSetTemplate = `{
  "Resources": {
    "Zone": {
      "Type": "AWS::Route53::HostedZone",
      "Properties": {"Name": "example.com."}
    },
    "Record": {
      "Type": "AWS::Route53::RecordSet",
      "Properties": {
        "HostedZoneId": {"Ref": "Zone"},
        "Name": "app.example.com.",
        "Type": "A",
        "TTL": "300",
        "ResourceRecords": ["%s"]
      }
    }
  }
}`

func TestUpdateStack_route53RecordValueChange_updatesInPlace(t *testing.T) {
	// Given: a stack with a zone and an A record
	srv := helpers.NewTestServer(t)
	stackName := "record-upd"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(recordSetTemplate, "192.0.2.1")},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	originalID := describeStackResourceIDs(t, srv, stackName)["Record"]

	// When: only the record's value changes
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(recordSetTemplate, "192.0.2.2")},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: same record (same physical ID), new value, and the record was not
	// deleted by the cleanup phase
	if got := describeStackResourceIDs(t, srv, stackName)["Record"]; got != originalID {
		t.Errorf("record was replaced: physical ID %q, want original %q", got, originalID)
	}
	zoneID := strings.SplitN(originalID, "/", 2)[0]
	recordResp := cfnRoute53Request(t, srv, http.MethodGet, "/2013-04-01/hostedzone/"+zoneID+"/rrset", "")
	defer recordResp.Body.Close()
	helpers.AssertStatus(t, recordResp, http.StatusOK)
	body := string(readBody(t, recordResp))
	if !strings.Contains(body, "app.example.com.") {
		t.Fatalf("record gone after update; rrset body:\n%s", body)
	}
	if !strings.Contains(body, "192.0.2.2") || strings.Contains(body, "192.0.2.1") {
		t.Errorf("record value not updated in place; rrset body:\n%s", body)
	}
}

func assertAlarmExists(t *testing.T, srv *helpers.TestServer, alarmName string, want bool) {
	t.Helper()
	resp, err := http.PostForm(srv.URL+"/", url.Values{
		"Action":              {"DescribeAlarms"},
		"Version":             {"2010-08-01"},
		"AlarmNames.member.1": {alarmName},
	})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	defer resp.Body.Close()
	body := string(readBody(t, resp))
	if got := strings.Contains(body, "<AlarmName>"+alarmName+"</AlarmName>"); got != want {
		t.Errorf("alarm %q exists = %v, want %v; body:\n%s", alarmName, got, want, body)
	}
}

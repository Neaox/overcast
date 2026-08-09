package cloudformation_test

// cloudwatch_alarm_test.go — AWS::CloudWatch::Alarm through the provisioner.
//
// `AlarmName` is optional on AWS::CloudWatch::Alarm: CloudFormation names the
// alarm when the template leaves it out, and CDK's `cloudwatch.Alarm` leaves it
// out unless the caller passes `alarmName`. So does every construct that builds
// an alarm for you — `queue.metricApproximateNumberOfMessagesVisible().createAlarm(...)`,
// `lambda.metricErrors().createAlarm(...)`. Passing the empty name straight to
// PutMetricAlarm turns "optional" into AWS's own required-field
// ValidationError, and every such stack dies at the first alarm.
//
// The other half is the properties the provisioner forwards. An optional
// property that is silently dropped produces an alarm that exists and is
// evaluated — against a configuration the template did not ask for.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// unnamedAlarmTemplate is the shape CDK emits for
// `queue.metricApproximateNumberOfMessagesVisible().createAlarm(...)` with an
// SNS action: no AlarmName, dimensions, and an action ARN.
const unnamedAlarmTemplate = `{
  "Resources": {
    "DlqAlarm": {
      "Type": "AWS::CloudWatch::Alarm",
      "Properties": {
        "AlarmActions": ["arn:aws:sns:ap-southeast-2:000000000000:task-service-dlq-errors"],
        "ComparisonOperator": "GreaterThanOrEqualToThreshold",
        "Dimensions": [{"Name": "QueueName", "Value": "task-service-dlq"}],
        "EvaluationPeriods": 2,
        "MetricName": "ApproximateNumberOfMessagesVisible",
        "Namespace": "AWS/SQS",
        "Period": 300,
        "Statistic": "Maximum",
        "Threshold": 1
      }
    }
  }
}`

func TestCreateStack_alarmWithoutAlarmName_cloudFormationNamesIt(t *testing.T) {
	// Given: a CDK-shaped alarm that leaves AlarmName to CloudFormation
	srv := helpers.NewTestServer(t)
	stackName := "alarm-unnamed"

	// When: the stack is created
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {unnamedAlarmTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	// Then: it completes — an omitted AlarmName is not a validation failure
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// And: the alarm carries a generated name derived from stack and logical ID,
	// which is what Ref on the resource hands to anything that consumes it
	physID := describeStackResourceIDs(t, srv, stackName)["DlqAlarm"]
	if physID == "" {
		t.Fatal("alarm has no physical ID")
	}
	if !strings.HasPrefix(physID, stackName+"-DlqAlarm-") {
		t.Errorf("generated alarm name = %q, want the CloudFormation <stack>-<logical>-<suffix> shape", physID)
	}
	assertAlarmExists(t, srv, physID, true)
}

func TestUpdateStack_alarmWithoutAlarmName_keepsItsGeneratedName(t *testing.T) {
	// Given: a stack whose alarm CloudFormation named
	srv := helpers.NewTestServer(t)
	stackName := "alarm-unnamed-upd"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {unnamedAlarmTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	originalID := describeStackResourceIDs(t, srv, stackName)["DlqAlarm"]

	// When: an unrelated property changes
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {strings.Replace(unnamedAlarmTemplate, `"Threshold": 1`, `"Threshold": 5`, 1)},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the generated name is kept rather than re-minted, and one alarm —
	// not two — is left behind
	if got := describeStackResourceIDs(t, srv, stackName)["DlqAlarm"]; got != originalID {
		t.Errorf("alarm name = %q after update, want the original %q", got, originalID)
	}
	assertAlarmExists(t, srv, originalID, true)
	if names := listAlarmNames(t, srv); len(names) != 1 {
		t.Errorf("stack left %d alarms behind, want 1: %v", len(names), names)
	}
}

// optionalPropsAlarmTemplate exercises the AWS::CloudWatch::Alarm properties
// that are optional in the template but change how the alarm is evaluated.
const optionalPropsAlarmTemplate = `{
  "Resources": {
    "Alarm": {
      "Type": "AWS::CloudWatch::Alarm",
      "Properties": {
        "AlarmName": "%s",
        "AlarmDescription": "errors on the task service",
        "MetricName": "Errors",
        "Namespace": "AWS/Lambda",
        "Statistic": "Sum",
        "Period": 60,
        "EvaluationPeriods": 5,
        "DatapointsToAlarm": 3,
        "TreatMissingData": "notBreaching",
        "Threshold": 1,
        "ComparisonOperator": "GreaterThanOrEqualToThreshold",
        "Unit": "Count",
        "ActionsEnabled": false
      }
    }
  }
}`

func TestCreateStack_alarmOptionalProperties_reachTheAlarm(t *testing.T) {
	// Given: a template setting the optional properties CDK's Alarm construct
	// emits by default (datapointsToAlarm, treatMissingData) alongside the rest
	srv := helpers.NewTestServer(t)
	stackName := "alarm-optional"
	alarmName := stackName + "-alarm"

	// When: the stack is created
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(optionalPropsAlarmTemplate, alarmName)},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: every one of them is on the alarm. A dropped DatapointsToAlarm or
	// TreatMissingData does not fail the stack — it produces an alarm that is
	// evaluated under rules the template did not ask for, which is worse.
	body := describeAlarmsBody(t, srv, alarmName)
	for _, want := range []string{
		"<DatapointsToAlarm>3</DatapointsToAlarm>",
		"<TreatMissingData>notBreaching</TreatMissingData>",
		"<AlarmDescription>errors on the task service</AlarmDescription>",
		"<Unit>Count</Unit>",
		"<ActionsEnabled>false</ActionsEnabled>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("alarm is missing %s; DescribeAlarms body:\n%s", want, body)
		}
	}
}

// metricMathAlarmTemplate is what CDK emits for `new MathExpression(...).createAlarm(...)`.
const metricMathAlarmTemplate = `{
  "Resources": {
    "MathAlarm": {
      "Type": "AWS::CloudWatch::Alarm",
      "Properties": {
        "AlarmName": "math-alarm",
        "ComparisonOperator": "GreaterThanThreshold",
        "EvaluationPeriods": 1,
        "Threshold": 1,
        "Metrics": [
          {"Id": "e1", "Expression": "m1/m2*100", "ReturnData": true},
          {"Id": "m1", "MetricStat": {"Metric": {"Namespace": "AWS/Lambda", "MetricName": "Errors"}, "Period": 300, "Stat": "Sum"}, "ReturnData": false},
          {"Id": "m2", "MetricStat": {"Metric": {"Namespace": "AWS/Lambda", "MetricName": "Invocations"}, "Period": 300, "Stat": "Sum"}, "ReturnData": false}
        ]
      }
    }
  }
}`

func TestCreateStack_metricMathAlarm_failsSayingItIsNotEmulated(t *testing.T) {
	// Given: a metric-math alarm, which the evaluator refuses by design
	srv := helpers.NewTestServer(t)
	stackName := "alarm-math"

	// When: the stack is created
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {metricMathAlarmTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	// Then: the stack fails — and the reason names metric math, rather than
	// blaming a missing MetricName the template never had to supply
	status := waitForStackStatusIn(t, srv, stackName, "CREATE_FAILED", "ROLLBACK_COMPLETE", "CREATE_COMPLETE")
	if status == "CREATE_COMPLETE" {
		t.Fatal("metric-math alarm stack reached CREATE_COMPLETE; the alarm cannot be evaluated")
	}
	reasons := strings.Join(describeStackEventReasons(t, srv, stackName), "\n")
	if !strings.Contains(reasons, "Metric-math") {
		t.Errorf("failure reason does not name metric math:\n%s", reasons)
	}
}

// describeAlarmsBody returns the DescribeAlarms XML for one alarm.
func describeAlarmsBody(t *testing.T, srv *helpers.TestServer, alarmName string) string {
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
	return string(readBody(t, resp))
}

// listAlarmNames returns every alarm name the emulator holds.
func listAlarmNames(t *testing.T, srv *helpers.TestServer) []string {
	t.Helper()
	resp, err := http.PostForm(srv.URL+"/", url.Values{
		"Action":  {"DescribeAlarms"},
		"Version": {"2010-08-01"},
	})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	defer resp.Body.Close()
	body := string(readBody(t, resp))

	var names []string
	for rest := body; ; {
		start := strings.Index(rest, "<AlarmName>")
		if start < 0 {
			return names
		}
		rest = rest[start+len("<AlarmName>"):]
		end := strings.Index(rest, "</AlarmName>")
		if end < 0 {
			return names
		}
		names = append(names, rest[:end])
		rest = rest[end:]
	}
}

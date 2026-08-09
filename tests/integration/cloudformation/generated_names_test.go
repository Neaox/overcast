package cloudformation_test

// generated_names_test.go — resources whose name the template does not supply.
//
// Almost every AWS::*::* name property is "Required: No", and CloudFormation
// mints "{StackName}-{LogicalID}-{RANDOM}" when the template leaves it out.
// CDK relies on this everywhere: its L2 constructs deliberately do not name
// resources unless you pass an explicit name, because a physical name makes
// the resource un-replaceable. So the no-name path is the *common* path for a
// CDK stack, not an edge case — a handler that forwards the empty string, or
// drops the name parameter and lets the service decide, breaks a stack that
// real CloudFormation deploys.
//
// This table is the systematic version of the AWS::CloudWatch::Alarm bug: one
// minimal, name-less resource per type CDK emits without a name.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateStack_resourcesWithoutNames_areNamedByCloudFormation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		logicalID  string
		properties string
	}{
		{
			name:      "AWS::SQS::Queue",
			logicalID: "Queue",
			properties: `{
        "Type": "AWS::SQS::Queue",
        "Properties": {}
      }`,
		},
		{
			name:      "AWS::SNS::Topic",
			logicalID: "Topic",
			properties: `{
        "Type": "AWS::SNS::Topic",
        "Properties": {}
      }`,
		},
		{
			name:      "AWS::DynamoDB::Table",
			logicalID: "Table",
			properties: `{
        "Type": "AWS::DynamoDB::Table",
        "Properties": {
          "KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
          "AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
          "BillingMode": "PAY_PER_REQUEST"
        }
      }`,
		},
		{
			name:      "AWS::Logs::LogGroup",
			logicalID: "Logs",
			properties: `{
        "Type": "AWS::Logs::LogGroup",
        "Properties": {"RetentionInDays": 7}
      }`,
		},
		{
			name:      "AWS::IAM::Role",
			logicalID: "Role",
			properties: `{
        "Type": "AWS::IAM::Role",
        "Properties": {
          "AssumeRolePolicyDocument": {
            "Version": "2012-10-17",
            "Statement": [{"Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]
          }
        }
      }`,
		},
		{
			name:      "AWS::Events::Rule",
			logicalID: "Rule",
			properties: `{
        "Type": "AWS::Events::Rule",
        "Properties": {
          "EventPattern": {"source": ["com.example"]},
          "State": "ENABLED"
        }
      }`,
		},
		{
			name:      "AWS::StepFunctions::StateMachine",
			logicalID: "Machine",
			properties: `{
        "Type": "AWS::StepFunctions::StateMachine",
        "Properties": {
          "RoleArn": "arn:aws:iam::000000000000:role/sfn",
          "DefinitionString": "{\"StartAt\":\"Done\",\"States\":{\"Done\":{\"Type\":\"Succeed\"}}}"
        }
      }`,
		},
		{
			name:      "AWS::Scheduler::ScheduleGroup",
			logicalID: "Group",
			properties: `{
        "Type": "AWS::Scheduler::ScheduleGroup",
        "Properties": {}
      }`,
		},
		{
			name:      "AWS::SecretsManager::Secret",
			logicalID: "Secret",
			properties: `{
        "Type": "AWS::SecretsManager::Secret",
        "Properties": {"SecretString": "shh"}
      }`,
		},
		{
			name:      "AWS::Kinesis::Stream",
			logicalID: "Stream",
			properties: `{
        "Type": "AWS::Kinesis::Stream",
        "Properties": {"ShardCount": 1}
      }`,
		},
		{
			name:      "AWS::ApiGateway::RestApi",
			logicalID: "Api",
			properties: `{
        "Type": "AWS::ApiGateway::RestApi",
        "Properties": {}
      }`,
		},
		{
			name:      "AWS::ECS::Cluster",
			logicalID: "Cluster",
			properties: `{
        "Type": "AWS::ECS::Cluster",
        "Properties": {}
      }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			stackName := "noname-" + strings.ToLower(tc.logicalID)
			template := fmt.Sprintf(`{"Resources": {%q: %s}}`, tc.logicalID, tc.properties)

			create := cfnQuery(t, srv, "CreateStack", url.Values{
				"StackName":    {stackName},
				"TemplateBody": {template},
			})
			defer create.Body.Close()
			helpers.AssertStatus(t, create, http.StatusOK)

			status := waitForStackStatusIn(t, srv, stackName,
				"CREATE_COMPLETE", "CREATE_FAILED", "ROLLBACK_COMPLETE", "ROLLBACK_IN_PROGRESS")
			if status != "CREATE_COMPLETE" {
				t.Fatalf("stack status = %s, want CREATE_COMPLETE; reasons:\n%s",
					status, strings.Join(describeStackEventReasons(t, srv, stackName), "\n"))
			}

			// And: the resource has a physical name to be referred to by. An
			// empty ID means Ref on the resource resolves to nothing, and the
			// deferred delete at cleanup or rollback has nothing to delete.
			physID := describeStackResourceIDs(t, srv, stackName)[tc.logicalID]
			if physID == "" {
				t.Fatal("resource has no physical ID")
			}
		})
	}
}

// TestCreateStack_twoUnnamedResourcesOfOneType_doNotCollide pins the other
// half of CloudFormation's naming rule: the generated name carries the logical
// ID, so two unnamed resources of the same type in one stack get two names.
// A fallback built from the stack name alone gives them both the same name,
// and the second silently overwrites — or upserts onto — the first.
func TestCreateStack_twoUnnamedResourcesOfOneType_doNotCollide(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resource string
	}{
		{"AWS::SQS::Queue", `{"Type": "AWS::SQS::Queue", "Properties": {}}`},
		{"AWS::SNS::Topic", `{"Type": "AWS::SNS::Topic", "Properties": {}}`},
		{"AWS::Logs::LogGroup", `{"Type": "AWS::Logs::LogGroup", "Properties": {}}`},
		{"AWS::Events::Rule", `{"Type": "AWS::Events::Rule", "Properties": {"EventPattern": {"source": ["com.example"]}}}`},
		{"AWS::Scheduler::ScheduleGroup", `{"Type": "AWS::Scheduler::ScheduleGroup", "Properties": {}}`},
		{"AWS::CloudWatch::Alarm", `{"Type": "AWS::CloudWatch::Alarm", "Properties": {
			"MetricName": "Errors", "Namespace": "AWS/Lambda", "Statistic": "Sum",
			"Period": 60, "EvaluationPeriods": 1, "Threshold": 1,
			"ComparisonOperator": "GreaterThanThreshold"}}`},
		{"AWS::EC2::SecurityGroup", `{"Type": "AWS::EC2::SecurityGroup", "Properties": {
			"GroupDescription": "test"}}`},
		{"AWS::ECR::Repository", `{"Type": "AWS::ECR::Repository", "Properties": {}}`},
		{"AWS::IAM::User", `{"Type": "AWS::IAM::User", "Properties": {}}`},
		{"AWS::ElasticLoadBalancingV2::TargetGroup", `{"Type": "AWS::ElasticLoadBalancingV2::TargetGroup", "Properties": {
			"Port": 80, "Protocol": "HTTP"}}`},
		{"AWS::Cognito::UserPool", `{"Type": "AWS::Cognito::UserPool", "Properties": {}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			stackName := "twin-" + strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(tc.name, "AWS::"), "::", "-"))
			template := fmt.Sprintf(`{"Resources": {"First": %s, "Second": %s}}`, tc.resource, tc.resource)

			create := cfnQuery(t, srv, "CreateStack", url.Values{
				"StackName":    {stackName},
				"TemplateBody": {template},
			})
			defer create.Body.Close()
			helpers.AssertStatus(t, create, http.StatusOK)

			status := waitForStackStatusIn(t, srv, stackName,
				"CREATE_COMPLETE", "CREATE_FAILED", "ROLLBACK_COMPLETE", "ROLLBACK_IN_PROGRESS")
			if status != "CREATE_COMPLETE" {
				t.Fatalf("stack status = %s, want CREATE_COMPLETE; reasons:\n%s",
					status, strings.Join(describeStackEventReasons(t, srv, stackName), "\n"))
			}

			ids := describeStackResourceIDs(t, srv, stackName)
			if ids["First"] == "" || ids["Second"] == "" {
				t.Fatalf("missing physical IDs: %v", ids)
			}
			if ids["First"] == ids["Second"] {
				t.Errorf("both resources got the physical ID %q — they are the same resource", ids["First"])
			}
		})
	}
}

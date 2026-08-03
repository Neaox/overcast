package cloudformation_test

// ecs_service_update_test.go — updating an AWS::ECS::Service, and what happens
// when the new deployment cannot run.
//
// An update that swaps in a task definition whose tasks fail is the most common
// way a real deployment goes wrong. CloudFormation has to notice, fail the
// resource and unwind — leaving the stack in a state the next `cdk deploy` can
// proceed from, rather than wedged.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// serviceStackTemplate renders the WordPress-shaped stack at a given desired
// count, so an update can change something the ECS service actually acts on.
func serviceStackTemplate(desiredCount string) string {
	return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::ECS::Cluster",
      "Properties": { "ClusterName": "upd-cluster" }
    },
    "TaskDefinition": {
      "Type": "AWS::ECS::TaskDefinition",
      "Properties": {
        "Family": "upd-task",
        "Cpu": "256",
        "Memory": "512",
        "NetworkMode": "awsvpc",
        "RequiresCompatibilities": ["FARGATE"],
        "ContainerDefinitions": [{ "Name": "app", "Image": "nginx:latest" }]
      }
    },
    "Service": {
      "Type": "AWS::ECS::Service",
      "Properties": {
        "ServiceName": "upd-svc",
        "Cluster": { "Ref": "Cluster" },
        "TaskDefinition": { "Ref": "TaskDefinition" },
        "LaunchType": "FARGATE",
        "DesiredCount": ` + desiredCount + `,
        "NetworkConfiguration": {
          "AwsvpcConfiguration": { "Subnets": ["subnet-12345678"] }
        }
      }
    }
  }
}`
}

func TestUpdateStack_ECSServiceDesiredCountChange(t *testing.T) {
	// Given: a deployed stack with a service running one task.
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ecs-upd-stack"},
		"TemplateBody": []string{serviceStackTemplate("1")},
	})
	cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "ecs-upd-stack", "CREATE_COMPLETE")

	// When: the stack is updated to want two.
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{"ecs-upd-stack"},
		"TemplateBody": []string{serviceStackTemplate("2")},
	})
	ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, "ecs-upd-stack", "UPDATE_COMPLETE")

	// Then: the update waited for the new count rather than reporting success
	// with the service still catching up.
	resp := awsJSONCall(t, srv, "AmazonEC2ContainerServiceV20141113.", "DescribeServices",
		"application/x-amz-json-1.1", map[string]any{
			"cluster": "upd-cluster", "services": []string{"upd-svc"},
		})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var described struct {
		Services []struct {
			DesiredCount int `json:"desiredCount"`
			RunningCount int `json:"runningCount"`
		} `json:"services"`
	}
	helpers.DecodeJSON(t, resp, &described)
	if len(described.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(described.Services))
	}
	if described.Services[0].DesiredCount != 2 || described.Services[0].RunningCount != 2 {
		t.Errorf("expected 2/2 after the update, got %d/%d",
			described.Services[0].RunningCount, described.Services[0].DesiredCount)
	}
}

// TestUpdateStack_ECSServiceFailedUpdateLeavesStackRecoverable checks the shape
// that wedges a real stack: an update whose ECS service resource fails must
// unwind to a terminal state the next deploy can start from, and must not
// leave the stack sitting in UPDATE_IN_PROGRESS forever.
func TestUpdateStack_ECSServiceFailedUpdateLeavesStackRecoverable(t *testing.T) {
	// Given: a deployed stack.
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ecs-upd-fail-stack"},
		"TemplateBody": []string{serviceStackTemplate("1")},
	})
	cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "ecs-upd-fail-stack", "CREATE_COMPLETE")

	// When: the update points the service at a task definition that does not
	// exist, so UpdateService fails the way a rolled-back deployment does.
	broken := strings.Replace(serviceStackTemplate("1"),
		`"TaskDefinition": { "Ref": "TaskDefinition" },`,
		`"TaskDefinition": "no-such-family:99",`, 1)
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{"ecs-upd-fail-stack"},
		"TemplateBody": []string{broken},
	})
	ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)

	// Then: the stack reaches a terminal state that says the update failed,
	// rather than reporting UPDATE_COMPLETE around a service that never
	// changed, or hanging in UPDATE_IN_PROGRESS.
	status := waitForStackStatusIn(t, srv, "ecs-upd-fail-stack",
		"UPDATE_ROLLBACK_COMPLETE", "UPDATE_ROLLBACK_FAILED", "UPDATE_FAILED", "UPDATE_COMPLETE")
	if status == "UPDATE_COMPLETE" {
		t.Fatal("a failed service update reported UPDATE_COMPLETE")
	}

	// And the service is still the one that worked, so the next deploy has
	// something to proceed from.
	resp := awsJSONCall(t, srv, "AmazonEC2ContainerServiceV20141113.", "DescribeServices",
		"application/x-amz-json-1.1", map[string]any{
			"cluster": "upd-cluster", "services": []string{"upd-svc"},
		})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var described struct {
		Services []struct {
			Status         string `json:"status"`
			TaskDefinition string `json:"taskDefinition"`
		} `json:"services"`
	}
	helpers.DecodeJSON(t, resp, &described)
	if len(described.Services) != 1 || described.Services[0].Status != "ACTIVE" {
		t.Fatalf("expected the service to survive the failed update, got %#v", described.Services)
	}
	if !strings.Contains(described.Services[0].TaskDefinition, "upd-task") {
		t.Errorf("expected the service to still run the working task definition, got %q",
			described.Services[0].TaskDefinition)
	}
}

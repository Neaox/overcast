package ecs_test

// entrypoint_test.go — entryPoint and workingDirectory survive the trip from a
// task definition to the container. A definition that sets either and has it
// dropped runs something other than what it asked for, without saying so.

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestRegisterTaskDefinition_keepsEntryPointAndWorkingDirectory(t *testing.T) {
	// Given: a task definition with a custom entry point, the shape CDK emits
	// for `entryPoint: [...]` on a container definition.
	srv := helpers.NewTestServer(t)

	reg := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family":                  "entrypoint-task",
		"networkMode":             "awsvpc",
		"requiresCompatibilities": []string{"FARGATE"},
		"cpu":                     "256",
		"memory":                  "512",
		"containerDefinitions": []map[string]any{{
			"name":             "app",
			"image":            "alpine:latest",
			"entryPoint":       []string{"/bin/sh", "-c"},
			"command":          []string{"echo hello"},
			"workingDirectory": "/srv",
		}},
	})
	helpers.AssertStatus(t, reg, http.StatusOK)
	reg.Body.Close()

	// When: the definition is read back.
	desc := ecsCall(t, srv, "DescribeTaskDefinition", map[string]any{
		"taskDefinition": "entrypoint-task:1",
	})
	helpers.AssertStatus(t, desc, http.StatusOK)
	var out struct {
		TaskDefinition struct {
			ContainerDefinitions []struct {
				EntryPoint       []string `json:"entryPoint"`
				Command          []string `json:"command"`
				WorkingDirectory string   `json:"workingDirectory"`
			} `json:"containerDefinitions"`
		} `json:"taskDefinition"`
	}
	helpers.DecodeJSON(t, desc, &out)
	desc.Body.Close()

	// Then: both survived, alongside the command.
	if len(out.TaskDefinition.ContainerDefinitions) != 1 {
		t.Fatalf("expected 1 container definition, got %d", len(out.TaskDefinition.ContainerDefinitions))
	}
	cd := out.TaskDefinition.ContainerDefinitions[0]
	if len(cd.EntryPoint) != 2 || cd.EntryPoint[0] != "/bin/sh" || cd.EntryPoint[1] != "-c" {
		t.Errorf("entryPoint = %#v, want [/bin/sh -c]", cd.EntryPoint)
	}
	if len(cd.Command) != 1 || cd.Command[0] != "echo hello" {
		t.Errorf("command = %#v, want [echo hello]", cd.Command)
	}
	if cd.WorkingDirectory != "/srv" {
		t.Errorf("workingDirectory = %q, want /srv", cd.WorkingDirectory)
	}
}

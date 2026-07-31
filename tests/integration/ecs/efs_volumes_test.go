package ecs_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestRegisterTaskDefinition_efsVolumesRoundTrip verifies that EFS-backed
// volumes and container mount points survive the register/describe cycle.
func TestRegisterTaskDefinition_efsVolumesRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family": "efs-task",
		"volumes": []map[string]any{{
			"name": "shared",
			"efsVolumeConfiguration": map[string]any{
				"fileSystemId":  "fs-0123456789abcdef0",
				"rootDirectory": "/",
				"authorizationConfig": map[string]any{
					"accessPointId": "fsap-0123456789abcdef0",
				},
			},
		}},
		"containerDefinitions": []map[string]any{{
			"name":  "app",
			"image": "public.ecr.aws/docker/library/busybox:latest",
			"mountPoints": []map[string]any{
				{"sourceVolume": "shared", "containerPath": "/data", "readOnly": true},
			},
		}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	desc := ecsCall(t, srv, "DescribeTaskDefinition", map[string]any{"taskDefinition": "efs-task"})
	defer desc.Body.Close()
	helpers.AssertStatus(t, desc, http.StatusOK)
	var result struct {
		TaskDefinition struct {
			Volumes []struct {
				Name                   string `json:"name"`
				EFSVolumeConfiguration *struct {
					FileSystemId string `json:"fileSystemId"`
				} `json:"efsVolumeConfiguration"`
			} `json:"volumes"`
			ContainerDefinitions []struct {
				MountPoints []struct {
					SourceVolume  string `json:"sourceVolume"`
					ContainerPath string `json:"containerPath"`
					ReadOnly      bool   `json:"readOnly"`
				} `json:"mountPoints"`
			} `json:"containerDefinitions"`
		} `json:"taskDefinition"`
	}
	helpers.DecodeJSON(t, desc, &result)

	td := result.TaskDefinition
	if len(td.Volumes) != 1 || td.Volumes[0].EFSVolumeConfiguration == nil ||
		td.Volumes[0].EFSVolumeConfiguration.FileSystemId != "fs-0123456789abcdef0" {
		t.Fatalf("expected EFS volume echoed, got %#v", td.Volumes)
	}
	if len(td.ContainerDefinitions) != 1 || len(td.ContainerDefinitions[0].MountPoints) != 1 {
		t.Fatalf("expected mount point echoed, got %#v", td.ContainerDefinitions)
	}
	mp := td.ContainerDefinitions[0].MountPoints[0]
	if mp.SourceVolume != "shared" || mp.ContainerPath != "/data" || !mp.ReadOnly {
		t.Fatalf("unexpected mount point: %#v", mp)
	}
}

// TestRegisterTaskDefinition_undefinedVolumeRejected verifies AWS's
// ClientException for mount points referencing undeclared volumes.
func TestRegisterTaskDefinition_undefinedVolumeRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family": "bad-task",
		"containerDefinitions": []map[string]any{{
			"name":  "app",
			"image": "public.ecr.aws/docker/library/busybox:latest",
			"mountPoints": []map[string]any{
				{"sourceVolume": "ghost", "containerPath": "/data"},
			},
		}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var body map[string]any
	helpers.DecodeJSON(t, resp, &body)
	if body["__type"] != "ClientException" {
		t.Fatalf("expected ClientException, got %#v", body)
	}
}

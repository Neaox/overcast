package docker

import (
	"encoding/json"
	"testing"
	"time"
)

func TestContainersByResourceRetainsEveryInstanceCandidate(t *testing.T) {
	containers := []ContainerSummary{
		{ID: "ours", Labels: map[string]string{LabelService: "ecs", LabelResourceID: "cluster/task"}},
		{ID: "theirs", Labels: map[string]string{LabelService: "ecs", LabelResourceID: "cluster/task"}},
		{ID: "unlabelled"},
	}

	got := ContainersByResource(containers)
	if len(got) != 1 || len(got["cluster/task"]) != 2 {
		t.Fatalf("resource index = %#v, want both candidates under cluster/task", got)
	}
	if got["cluster/task"][0].ID != "ours" || got["cluster/task"][1].ID != "theirs" {
		t.Fatalf("resource candidates = %#v, want input order preserved", got["cluster/task"])
	}
}

func TestDaemonSnapshotsPartitionByService(t *testing.T) {
	containers := []ContainerSummary{
		{ID: "ecs-1", Labels: map[string]string{LabelService: "ecs"}},
		{ID: "rds-1", Labels: map[string]string{LabelService: "rds"}},
		{ID: "unknown"},
	}
	networks := []NetworkSummary{
		{ID: "ec2-1", Labels: map[string]string{LabelService: "ec2"}},
		{ID: "unknown"},
	}

	byService := ContainersByService(containers)
	if len(byService) != 2 || len(byService["ecs"]) != 1 || len(byService["rds"]) != 1 {
		t.Fatalf("container partition = %#v", byService)
	}
	byNetworkService := NetworksByService(networks)
	if len(byNetworkService) != 1 || len(byNetworkService["ec2"]) != 1 {
		t.Fatalf("network partition = %#v", byNetworkService)
	}
	filtered := ContainersByService(containers, "ecs")
	if len(filtered) != 1 || len(filtered["ecs"]) != 1 {
		t.Fatalf("filtered container partition = %#v, want only ecs", filtered)
	}
}

func TestContainerInspectExitTime(t *testing.T) {
	const finishedAt = "2026-08-14T01:02:03.456789123Z"
	var inspect ContainerInspect
	if err := json.Unmarshal([]byte(`{"State":{"FinishedAt":"`+finishedAt+`"}}`), &inspect); err != nil {
		t.Fatalf("unmarshal inspect: %v", err)
	}
	want, _ := time.Parse(time.RFC3339Nano, finishedAt)
	if got := inspect.ExitTime(); !got.Equal(want) {
		t.Fatalf("ExitTime = %s, want %s", got, want)
	}
}

package ecs

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/serviceutil"
)

type stubEFSResolver struct {
	volumes map[string]string
}

func (s stubEFSResolver) EFSVolumeForFileSystem(_ context.Context, fsID string) (string, bool) {
	v, ok := s.volumes[fsID]
	return v, ok
}

func testTaskDef() *TaskDefinition {
	return &TaskDefinition{
		Family: "efs-family",
		Volumes: []TaskVolume{
			{Name: "shared", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-known"}},
			{Name: "unknown-fs", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-unknown"}},
			{Name: "host-vol"},
		},
		ContainerDefinitions: []ContainerDefinition{{
			Name: "app",
			MountPoints: []MountPoint{
				{SourceVolume: "shared", ContainerPath: "/data", ReadOnly: false},
				{SourceVolume: "shared", ContainerPath: "/data-ro", ReadOnly: true},
				{SourceVolume: "unknown-fs", ContainerPath: "/missing"},
				{SourceVolume: "host-vol", ContainerPath: "/host"},
			},
		}},
	}
}

func TestEFSBindsForContainer(t *testing.T) {
	h := &Handler{log: serviceutil.NewServiceLogger(zap.NewNop(), "ecs")}
	td := testTaskDef()
	cd := &td.ContainerDefinitions[0]
	ctx := context.Background()

	// No resolver wired → no binds.
	if got := h.efsBindsForContainer(ctx, td, cd); got != nil {
		t.Fatalf("expected nil binds without resolver, got %v", got)
	}

	// Resolved EFS mounts bind (with :ro honored); unknown file systems and
	// non-EFS volumes are skipped.
	h.efsResolver = stubEFSResolver{volumes: map[string]string{"fs-known": "overcast-efs-fs-known"}}
	got := h.efsBindsForContainer(ctx, td, cd)
	want := []string{"overcast-efs-fs-known:/data", "overcast-efs-fs-known:/data-ro:ro"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected %v, got %v", want, got)
	}

	// Containers without mount points stay bind-free.
	if got := h.efsBindsForContainer(ctx, td, &ContainerDefinition{Name: "plain"}); got != nil {
		t.Fatalf("expected nil binds for container without mount points, got %v", got)
	}
}

func TestValidateTaskVolumes(t *testing.T) {
	td := testTaskDef()
	if aerr := validateTaskVolumes(td.Volumes, td.ContainerDefinitions); aerr != nil {
		t.Fatalf("valid definition rejected: %v", aerr)
	}

	bad := []ContainerDefinition{{
		Name:        "app",
		MountPoints: []MountPoint{{SourceVolume: "nope", ContainerPath: "/x"}},
	}}
	aerr := validateTaskVolumes(td.Volumes, bad)
	if aerr == nil || aerr.Code != "ClientException" {
		t.Fatalf("expected ClientException for undefined volume, got %v", aerr)
	}
}

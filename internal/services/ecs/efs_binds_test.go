package ecs

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/serviceutil"
)

type stubEFSResolver struct {
	volumes      map[string]string
	accessPoints map[string][2]string // id → {volume, subpath}
}

func (s stubEFSResolver) EFSVolumeForFileSystem(_ context.Context, fsID string) (string, bool) {
	v, ok := s.volumes[fsID]
	return v, ok
}

func (s stubEFSResolver) EFSVolumeForAccessPointID(_ context.Context, apID string) (string, string, bool) {
	m, ok := s.accessPoints[apID]
	return m[0], m[1], ok
}

func testTaskDef() *TaskDefinition {
	return &TaskDefinition{
		Family: "efs-family",
		Volumes: []TaskVolume{
			{Name: "shared", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-known"}},
			{Name: "scoped", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-known", RootDirectory: "/data/app"}},
			{Name: "via-ap", EFSVolumeConfiguration: &EFSVolumeConfiguration{
				FileSystemId:        "fs-known",
				AuthorizationConfig: &EFSAuthorizationConfig{AccessPointId: "fsap-1"},
			}},
			{Name: "unknown-fs", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-unknown"}},
			{Name: "host-vol"},
		},
		ContainerDefinitions: []ContainerDefinition{{
			Name: "app",
			MountPoints: []MountPoint{
				{SourceVolume: "shared", ContainerPath: "/data", ReadOnly: false},
				{SourceVolume: "shared", ContainerPath: "/data-ro", ReadOnly: true},
				{SourceVolume: "scoped", ContainerPath: "/scoped"},
				{SourceVolume: "via-ap", ContainerPath: "/ap"},
				{SourceVolume: "unknown-fs", ContainerPath: "/missing"},
				{SourceVolume: "host-vol", ContainerPath: "/host"},
			},
		}},
	}
}

func TestEFSMountsForContainer(t *testing.T) {
	h := &Handler{log: serviceutil.NewServiceLogger(zap.NewNop(), "ecs")}
	td := testTaskDef()
	cd := &td.ContainerDefinitions[0]
	ctx := context.Background()

	// No resolver wired → no mounts.
	if got := h.efsMountsForContainer(ctx, td, cd); got != nil {
		t.Fatalf("expected nil mounts without resolver, got %v", got)
	}

	// Resolved EFS mounts (ro honored, subpaths from rootDirectory and access
	// point); unknown file systems and non-EFS volumes are skipped.
	h.efsResolver = stubEFSResolver{
		volumes:      map[string]string{"fs-known": "overcast-efs-fs-known"},
		accessPoints: map[string][2]string{"fsap-1": {"overcast-efs-fs-known", "ap-root"}},
	}
	got := h.efsMountsForContainer(ctx, td, cd)
	if len(got) != 4 {
		t.Fatalf("expected 4 mounts, got %v", got)
	}
	if got[0].Source != "overcast-efs-fs-known" || got[0].Target != "/data" || got[0].ReadOnly || got[0].VolumeOptions != nil {
		t.Fatalf("unexpected plain mount: %+v", got[0])
	}
	if !got[1].ReadOnly || got[1].Target != "/data-ro" {
		t.Fatalf("expected read-only mount, got %+v", got[1])
	}
	if got[2].VolumeOptions == nil || got[2].VolumeOptions.Subpath != "data/app" {
		t.Fatalf("expected rootDirectory subpath 'data/app', got %+v", got[2].VolumeOptions)
	}
	if got[3].VolumeOptions == nil || got[3].VolumeOptions.Subpath != "ap-root" {
		t.Fatalf("expected access-point subpath 'ap-root', got %+v", got[3].VolumeOptions)
	}

	// Containers without mount points stay mount-free.
	if got := h.efsMountsForContainer(ctx, td, &ContainerDefinition{Name: "plain"}); got != nil {
		t.Fatalf("expected nil mounts for container without mount points, got %v", got)
	}
}

func TestValidateTaskVolumes_accessPointRootDirectoryConflict(t *testing.T) {
	volumes := []TaskVolume{{
		Name: "bad",
		EFSVolumeConfiguration: &EFSVolumeConfiguration{
			FileSystemId:        "fs-1",
			RootDirectory:       "/app",
			AuthorizationConfig: &EFSAuthorizationConfig{AccessPointId: "fsap-1"},
		},
	}}
	aerr := validateTaskVolumes(volumes, nil)
	if aerr == nil || aerr.Code != "ClientException" {
		t.Fatalf("expected ClientException for accessPointId + rootDirectory, got %v", aerr)
	}

	// rootDirectory "/" alongside an access point is allowed.
	volumes[0].EFSVolumeConfiguration.RootDirectory = "/"
	if aerr := validateTaskVolumes(volumes, nil); aerr != nil {
		t.Fatalf("rootDirectory '/' with access point should be valid, got %v", aerr)
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

package ecs

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/serviceutil"
)

// testTaskID is a task ID of realistic shape: taskVolumeName takes its first
// eight characters, so a short stand-in would not exercise the same names.
const testTaskID = "abcdef12-3456-7890-abcd-ef1234567890"

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

// testTaskDef covers every volume shape a task definition can declare, mounted
// in a deliberate order so the emitted mounts can be asserted positionally.
func testTaskDef() *TaskDefinition {
	return &TaskDefinition{
		Family: "volumes-family",
		Volumes: []TaskVolume{
			{Name: "shared", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-known"}},
			{Name: "scoped", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-known", RootDirectory: "/data/app"}},
			{Name: "via-ap", EFSVolumeConfiguration: &EFSVolumeConfiguration{
				FileSystemId:        "fs-known",
				AuthorizationConfig: &EFSAuthorizationConfig{AccessPointId: "fsap-1"},
			}},
			{Name: "unknown-fs", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-unknown"}},
			{Name: "scratch"},
			{Name: "host-empty", Host: &HostVolumeProperties{}},
			{Name: "src", Host: &HostVolumeProperties{SourcePath: "/host/src"}},
			{Name: "dv-task", DockerVolumeConfiguration: &DockerVolumeConfiguration{Scope: "task"}},
			{Name: "dv-shared", DockerVolumeConfiguration: &DockerVolumeConfiguration{Scope: "shared", Autoprovision: true}},
		},
		ContainerDefinitions: []ContainerDefinition{{
			Name: "app",
			MountPoints: []MountPoint{
				{SourceVolume: "shared", ContainerPath: "/data", ReadOnly: false},
				{SourceVolume: "shared", ContainerPath: "/data-ro", ReadOnly: true},
				{SourceVolume: "scoped", ContainerPath: "/scoped"},
				{SourceVolume: "via-ap", ContainerPath: "/ap"},
				{SourceVolume: "unknown-fs", ContainerPath: "/missing"},
				{SourceVolume: "scratch", ContainerPath: "/scratch"},
				{SourceVolume: "host-empty", ContainerPath: "/host-empty"},
				{SourceVolume: "src", ContainerPath: "/var/www/html"},
				{SourceVolume: "dv-task", ContainerPath: "/dv-task"},
				{SourceVolume: "dv-shared", ContainerPath: "/dv-shared"},
				{SourceVolume: "undeclared", ContainerPath: "/nope"},
			},
		}},
	}
}

func testHandler() *Handler {
	return &Handler{log: serviceutil.NewServiceLogger(zap.NewNop(), "ecs")}
}

func resolvedEFS() stubEFSResolver {
	return stubEFSResolver{
		volumes:      map[string]string{"fs-known": "overcast-efs-fs-known"},
		accessPoints: map[string][2]string{"fsap-1": {"overcast-efs-fs-known", "ap-root"}},
	}
}

func TestContainerMounts(t *testing.T) {
	h := testHandler()
	h.efsResolver = resolvedEFS()
	td := testTaskDef()
	cd := &td.ContainerDefinitions[0]

	got := h.containerMounts(context.Background(), td, cd, testTaskID, nil)

	// One per mount point, less the unresolvable EFS volume and the mount
	// point naming a volume the definition does not declare.
	want := []struct {
		typ, source, target string
		readOnly            bool
		subpath             string
	}{
		{typ: "volume", source: "overcast-efs-fs-known", target: "/data"},
		{typ: "volume", source: "overcast-efs-fs-known", target: "/data-ro", readOnly: true},
		{typ: "volume", source: "overcast-efs-fs-known", target: "/scoped", subpath: "data/app"},
		{typ: "volume", source: "overcast-efs-fs-known", target: "/ap", subpath: "ap-root"},
		{typ: "volume", source: "overcast-ecs-task-abcdef12-scratch", target: "/scratch"},
		{typ: "volume", source: "overcast-ecs-task-abcdef12-host-empty", target: "/host-empty"},
		{typ: "bind", source: "/host/src", target: "/var/www/html"},
		{typ: "volume", source: "overcast-ecs-task-abcdef12-dv-task", target: "/dv-task"},
		// Shared scope keeps the task definition's own name: that is the name
		// it has on a container instance, and the name a pre-created volume is
		// expected to be found under.
		{typ: "volume", source: "dv-shared", target: "/dv-shared"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d mounts, got %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Type != w.typ || g.Source != w.source || g.Target != w.target || g.ReadOnly != w.readOnly {
			t.Errorf("mount %d: got {%s %s %s ro=%v}, want {%s %s %s ro=%v}",
				i, g.Type, g.Source, g.Target, g.ReadOnly, w.typ, w.source, w.target, w.readOnly)
		}
		switch {
		case w.subpath == "" && g.VolumeOptions != nil:
			t.Errorf("mount %d: unexpected volume options %+v", i, g.VolumeOptions)
		case w.subpath != "" && (g.VolumeOptions == nil || g.VolumeOptions.Subpath != w.subpath):
			t.Errorf("mount %d: want subpath %q, got %+v", i, w.subpath, g.VolumeOptions)
		}
	}
}

// Host, scratch and Docker volumes have nothing to do with EFS, so they must
// still be emitted when no EFS resolver is wired — the case that used to
// short-circuit the whole function and drop every mount.
func TestContainerMounts_withoutEFSResolver(t *testing.T) {
	h := testHandler()
	td := testTaskDef()

	got := h.containerMounts(context.Background(), td, &td.ContainerDefinitions[0], testTaskID, nil)

	if len(got) != 5 {
		t.Fatalf("expected the 5 non-EFS mounts, got %d: %+v", len(got), got)
	}
	for _, m := range got {
		if m.Source == "overcast-efs-fs-known" {
			t.Fatalf("EFS mount emitted with no resolver wired: %+v", m)
		}
	}
	if got[2].Type != "bind" || got[2].Source != "/host/src" {
		t.Errorf("expected the host bind to survive, got %+v", got[2])
	}
}

func TestContainerMounts_noMountPoints(t *testing.T) {
	h := testHandler()
	h.efsResolver = resolvedEFS()
	td := testTaskDef()

	if got := h.containerMounts(context.Background(), td, &ContainerDefinition{Name: "plain"}, testTaskID, nil); got != nil {
		t.Fatalf("expected nil mounts for a container without mount points, got %v", got)
	}
}

func TestMountedVolumes_skipsUnmountedVolumes(t *testing.T) {
	td := &TaskDefinition{
		Volumes: []TaskVolume{{Name: "used"}, {Name: "declared-but-unused"}},
		ContainerDefinitions: []ContainerDefinition{{
			Name:        "app",
			MountPoints: []MountPoint{{SourceVolume: "used", ContainerPath: "/used"}},
		}},
	}
	got := mountedVolumes(td)
	if len(got) != 1 || got[0].Name != "used" {
		t.Fatalf("expected only the mounted volume, got %v", got)
	}
}

// Provisioning stops at the first failure, so the order it walks volumes in
// decides which failure a task reports. Declaration order keeps that stable.
func TestMountedVolumes_declarationOrder(t *testing.T) {
	td := &TaskDefinition{
		Volumes: []TaskVolume{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		ContainerDefinitions: []ContainerDefinition{{
			Name: "app",
			MountPoints: []MountPoint{
				{SourceVolume: "c", ContainerPath: "/c"},
				{SourceVolume: "a", ContainerPath: "/a"},
				{SourceVolume: "b", ContainerPath: "/b"},
			},
		}},
	}
	got := mountedVolumes(td)
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Name != want {
			t.Fatalf("position %d: got %q, want %q", i, got[i].Name, want)
		}
	}
}

func TestDockerVolumeScope_defaultsToTask(t *testing.T) {
	if s := dockerVolumeScope(nil); s != "task" {
		t.Errorf("nil config: got %q, want task", s)
	}
	if s := dockerVolumeScope(&DockerVolumeConfiguration{}); s != "task" {
		t.Errorf("unset scope: got %q, want task", s)
	}
	if s := dockerVolumeScope(&DockerVolumeConfiguration{Scope: "SHARED"}); s != "shared" {
		t.Errorf("scope is case-insensitive on AWS: got %q, want shared", s)
	}
}

// ── Registration validation ────────────────────────────────────────────────

// The Fargate rejection is the fidelity invariant of this feature: a task
// definition real AWS refuses must be refused here too, or a template that
// passes locally fails its first real deploy. The message is AWS's own.
func TestValidateTaskVolumes_fargateRejectsHostSourcePath(t *testing.T) {
	volumes := []TaskVolume{{Name: "src", Host: &HostVolumeProperties{SourcePath: "/host/src"}}}

	aerr := validateTaskVolumes(volumes, nil, true)
	if aerr == nil {
		t.Fatal("expected Fargate to reject host.sourcePath")
	}
	if aerr.Code != "ClientException" {
		t.Errorf("got code %q, want ClientException", aerr.Code)
	}
	if aerr.Message != "host.sourcePath should not be set for volumes in Fargate" {
		t.Errorf("message must match AWS verbatim, got %q", aerr.Message)
	}

	// The same volume is valid under the EC2 launch type, where AWS supports it.
	if aerr := validateTaskVolumes(volumes, nil, false); aerr != nil {
		t.Errorf("EC2 launch type should accept host.sourcePath, got %v", aerr)
	}
}

// An empty host block is a scratch volume, not a bind — Fargate allows it, and
// CDK emits it for a plain shared volume.
func TestValidateTaskVolumes_fargateAllowsEmptyHostBlock(t *testing.T) {
	volumes := []TaskVolume{{Name: "scratch", Host: &HostVolumeProperties{}}}
	if aerr := validateTaskVolumes(volumes, nil, true); aerr != nil {
		t.Fatalf("Fargate should accept an empty host block, got %v", aerr)
	}
}

func TestValidateTaskVolumes_dockerVolumeRules(t *testing.T) {
	tests := []struct {
		name    string
		volume  TaskVolume
		fargate bool
		wantErr bool
	}{
		{
			name:    "fargate rejects dockerVolumeConfiguration",
			volume:  TaskVolume{Name: "dv", DockerVolumeConfiguration: &DockerVolumeConfiguration{Scope: "task"}},
			fargate: true,
			wantErr: true,
		},
		{
			name:    "task scope with autoprovision",
			volume:  TaskVolume{Name: "dv", DockerVolumeConfiguration: &DockerVolumeConfiguration{Scope: "task", Autoprovision: true}},
			wantErr: true,
		},
		{
			name:    "unknown scope",
			volume:  TaskVolume{Name: "dv", DockerVolumeConfiguration: &DockerVolumeConfiguration{Scope: "cluster"}},
			wantErr: true,
		},
		{
			name:   "shared scope with autoprovision",
			volume: TaskVolume{Name: "dv", DockerVolumeConfiguration: &DockerVolumeConfiguration{Scope: "shared", Autoprovision: true}},
		},
		{
			name:   "task scope without autoprovision",
			volume: TaskVolume{Name: "dv", DockerVolumeConfiguration: &DockerVolumeConfiguration{Scope: "task"}},
		},
		{
			name:   "unset scope defaults to task",
			volume: TaskVolume{Name: "dv", DockerVolumeConfiguration: &DockerVolumeConfiguration{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			aerr := validateTaskVolumes([]TaskVolume{tc.volume}, nil, tc.fargate)
			if tc.wantErr && aerr == nil {
				t.Fatal("expected a ClientException, got none")
			}
			if tc.wantErr && aerr.Code != "ClientException" {
				t.Fatalf("got code %q, want ClientException", aerr.Code)
			}
			if !tc.wantErr && aerr != nil {
				t.Fatalf("expected the volume to be valid, got %v", aerr)
			}
		})
	}
}

func TestValidateTaskVolumes_oneConfigurationPerVolume(t *testing.T) {
	volumes := []TaskVolume{{
		Name:                   "both",
		Host:                   &HostVolumeProperties{SourcePath: "/host/src"},
		EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-1"},
	}}
	aerr := validateTaskVolumes(volumes, nil, false)
	if aerr == nil || aerr.Code != "ClientException" {
		t.Fatalf("expected ClientException for two configurations, got %v", aerr)
	}

	// An empty host block alongside another configuration is not a conflict:
	// it carries no configuration of its own.
	volumes[0].Host = &HostVolumeProperties{}
	if aerr := validateTaskVolumes(volumes, nil, false); aerr != nil {
		t.Fatalf("empty host block should not conflict, got %v", aerr)
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
	aerr := validateTaskVolumes(volumes, nil, false)
	if aerr == nil || aerr.Code != "ClientException" {
		t.Fatalf("expected ClientException for accessPointId + rootDirectory, got %v", aerr)
	}

	// rootDirectory "/" alongside an access point is allowed.
	volumes[0].EFSVolumeConfiguration.RootDirectory = "/"
	if aerr := validateTaskVolumes(volumes, nil, false); aerr != nil {
		t.Fatalf("rootDirectory '/' with access point should be valid, got %v", aerr)
	}
}

func TestValidateTaskVolumes_undefinedSourceVolume(t *testing.T) {
	td := testTaskDef()
	if aerr := validateTaskVolumes(td.Volumes, nil, false); aerr != nil {
		t.Fatalf("valid definition rejected: %v", aerr)
	}

	bad := []ContainerDefinition{{
		Name:        "app",
		MountPoints: []MountPoint{{SourceVolume: "nope", ContainerPath: "/x"}},
	}}
	aerr := validateTaskVolumes(td.Volumes, bad, false)
	if aerr == nil || aerr.Code != "ClientException" {
		t.Fatalf("expected ClientException for undefined volume, got %v", aerr)
	}
}

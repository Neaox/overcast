package ecs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func hotReloadHandler(enabled bool) *Handler {
	return &Handler{
		log: serviceutil.NewServiceLogger(zap.NewNop(), "ecs"),
		cfg: &config.Config{ECSHotReload: enabled},
	}
}

// hotReloadTaskDef is the shape the guide recommends: a source volume to
// redirect, plus overlay volumes that must be left as scratch.
func hotReloadTaskDef() *TaskDefinition {
	return &TaskDefinition{
		TaskDefinitionArn: "arn:aws:ecs:us-east-1:000000000000:task-definition/app:1",
		Volumes: []TaskVolume{
			{Name: "app-src"},
			{Name: "app-vendor"},
			{Name: "efs-vol", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-1"}},
		},
		ContainerDefinitions: []ContainerDefinition{{
			Name: "app",
			MountPoints: []MountPoint{
				{SourceVolume: "app-src", ContainerPath: "/var/www/html"},
				{SourceVolume: "app-vendor", ContainerPath: "/var/www/html/vendor"},
			},
		}},
	}
}

func TestHotReloadPaths_suffixedKey(t *testing.T) {
	h := hotReloadHandler(true)
	got := h.hotReloadPaths(hotReloadTaskDef(), map[string]string{
		"overcast:hot-reload-path/app-src": "/host/app",
	})
	if len(got) != 1 || got["app-src"] != "/host/app" {
		t.Fatalf("expected app-src redirected to /host/app, got %v", got)
	}
}

// The bare key is Lambda's spelling, and the reason the two services read as
// one feature. It applies only when there is exactly one candidate.
func TestHotReloadPaths_bareKey(t *testing.T) {
	h := hotReloadHandler(true)
	td := &TaskDefinition{
		Volumes: []TaskVolume{
			{Name: "src"},
			{Name: "efs-vol", EFSVolumeConfiguration: &EFSVolumeConfiguration{FileSystemId: "fs-1"}},
		},
		ContainerDefinitions: []ContainerDefinition{{
			Name:        "app",
			MountPoints: []MountPoint{{SourceVolume: "src", ContainerPath: "/src"}},
		}},
	}
	got := h.hotReloadPaths(td, map[string]string{"overcast:hot-reload-path": "/host/app"})
	if len(got) != 1 || got["src"] != "/host/app" {
		t.Fatalf("expected the sole redirectable volume to be used, got %v", got)
	}
}

// The recommended Laravel layout declares overlay volumes, so the bare key is
// ambiguous there by design and must redirect nothing rather than guess.
func TestHotReloadPaths_bareKeyAmbiguous(t *testing.T) {
	h := hotReloadHandler(true)
	got := h.hotReloadPaths(hotReloadTaskDef(), map[string]string{
		"overcast:hot-reload-path": "/host/app",
	})
	if got != nil {
		t.Fatalf("expected no redirect when the bare key is ambiguous, got %v", got)
	}
}

func TestHotReloadPaths_suffixedWinsOverBare(t *testing.T) {
	h := hotReloadHandler(true)
	got := h.hotReloadPaths(hotReloadTaskDef(), map[string]string{
		"overcast:hot-reload-path":            "/host/ignored",
		"overcast:hot-reload-path/app-vendor": "/host/vendor",
	})
	if len(got) != 1 || got["app-vendor"] != "/host/vendor" {
		t.Fatalf("expected only the suffixed key to apply, got %v", got)
	}
}

func TestHotReloadPaths_disabledIgnoresTag(t *testing.T) {
	h := hotReloadHandler(false)
	got := h.hotReloadPaths(hotReloadTaskDef(), map[string]string{
		"overcast:hot-reload-path/app-src": "/host/app",
	})
	if got != nil {
		t.Fatalf("expected no redirect with hot reload disabled, got %v", got)
	}
}

func TestHotReloadPaths_rejectsUnredirectableAndUnknown(t *testing.T) {
	h := hotReloadHandler(true)
	got := h.hotReloadPaths(hotReloadTaskDef(), map[string]string{
		// An EFS volume names its own storage; redirecting it would override
		// what the definition asked for rather than fill in what it left open.
		"overcast:hot-reload-path/efs-vol": "/host/app",
		"overcast:hot-reload-path/nope":    "/host/app",
	})
	if got != nil {
		t.Fatalf("expected no redirect, got %v", got)
	}
}

func TestHotReloadPaths_relativePathRejected(t *testing.T) {
	h := hotReloadHandler(true)
	got := h.hotReloadPaths(hotReloadTaskDef(), map[string]string{
		"overcast:hot-reload-path/app-src": "relative/path",
	})
	if got != nil {
		t.Fatalf("expected a relative path to be rejected, got %v", got)
	}
}

func TestHotReloadPaths_windowsPathNormalized(t *testing.T) {
	h := hotReloadHandler(true)
	got := h.hotReloadPaths(hotReloadTaskDef(), map[string]string{
		"overcast:hot-reload-path/app-src": `F:\dev\myapp`,
	})
	if got["app-src"] != "/f/dev/myapp" {
		t.Fatalf("expected the Windows path to be normalized, got %v", got)
	}
}

func TestHotReloadPaths_noTags(t *testing.T) {
	h := hotReloadHandler(true)
	if got := h.hotReloadPaths(hotReloadTaskDef(), nil); got != nil {
		t.Fatalf("expected no redirect without tags, got %v", got)
	}
	if got := h.hotReloadPaths(hotReloadTaskDef(), map[string]string{"env": "dev"}); got != nil {
		t.Fatalf("expected unrelated tags to be ignored, got %v", got)
	}
}

// The redirect replaces the scratch volume the definition declared, and leaves
// every other mount point in the task alone.
func TestContainerMounts_hotReloadRedirect(t *testing.T) {
	h := hotReloadHandler(true)
	td := hotReloadTaskDef()

	got := h.containerMounts(context.Background(), td, &td.ContainerDefinitions[0], testTaskID,
		map[string]string{"app-src": "/host/app"})

	if len(got) != 2 {
		t.Fatalf("expected 2 mounts, got %+v", got)
	}
	if got[0].Type != "bind" || got[0].Source != "/host/app" || got[0].Target != "/var/www/html" {
		t.Errorf("expected the source volume bound to the host path, got %+v", got[0])
	}
	// The vendor overlay keeps its scratch volume: that is what makes the
	// image's vendor tree shadow the bind-mounted host tree.
	if got[1].Type != "volume" || got[1].Source != "overcast-ecs-task-abcdef12-app-vendor" {
		t.Errorf("expected the overlay to stay a scratch volume, got %+v", got[1])
	}
}

func TestDecorateBindMountError(t *testing.T) {
	mounts := []docker.Mount{
		{Type: "bind", Source: "/host/app"},
		{Type: "volume", Source: "some-volume"},
	}

	// A mount failure gains the path and the Docker Desktop hint.
	err := decorateBindMountError(errors.New("invalid mount config for type bind"), mounts)
	if got := err.Error(); !strings.Contains(got, "/host/app") || !strings.Contains(got, "File Sharing") {
		t.Errorf("expected the path and the file-sharing hint, got %q", got)
	}

	// An unrelated failure is passed through untouched rather than dressed up
	// as a mount problem.
	orig := errors.New("no such image")
	if got := decorateBindMountError(orig, mounts); got != orig {
		t.Errorf("expected an unrelated error to pass through, got %q", got)
	}

	// Nothing to name when the task binds nothing.
	volumeOnly := []docker.Mount{{Type: "volume", Source: "some-volume"}}
	if got := decorateBindMountError(orig, volumeOnly); got != orig {
		t.Errorf("expected passthrough with no binds, got %q", got)
	}
}

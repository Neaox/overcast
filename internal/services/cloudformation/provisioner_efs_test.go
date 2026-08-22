package cloudformation

// provisioner_efs_test.go — when an AWS::EFS::* resource is done.
//
// CreateMountTarget answers with the mount target in "creating"; with the NFS
// data plane on (OVERCAST_EFS_NFS) an export container then has to come up
// behind it, and one that cannot settles in "error". Neither state stopped the
// stack: it reported CREATE_COMPLETE around a mount target nothing could mount,
// and a task with that file system attached started against it and failed.
//
// The same applies to the file system itself and to an access point, both of
// which report the same LifeCycleState vocabulary and both of which a mount can
// be waiting on.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// fakeEFS is an EFS endpoint that walks one resource of each kind through a
// scripted sequence of lifecycle states — one per describe, with the last
// repeating.
type fakeEFS struct {
	script statusScript
	// empty answers describe with no records, as a resource deleted from under
	// the stack does.
	empty bool
}

// ServeHTTP answers EFS's own REST bindings under /2015-02-01/ — the surface
// the provisioner dispatches to since #1226 retired the invented
// "EFS.<Op>" X-Amz-Target prefix.
func (f *fakeEFS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/2015-02-01/file-systems":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"FileSystemId": "fs-0001", "LifeCycleState": "creating",
			"FileSystemArn": "arn:aws:elasticfilesystem:us-east-1:000000000000:file-system/fs-0001",
		})
	case r.Method == http.MethodGet && r.URL.Path == "/2015-02-01/file-systems":
		f.writeList(w, "FileSystems", map[string]any{"FileSystemId": "fs-0001"})

	case r.Method == http.MethodPost && r.URL.Path == "/2015-02-01/mount-targets":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MountTargetId": "fsmt-0001", "IpAddress": "10.0.0.10", "LifeCycleState": "creating",
		})
	case r.Method == http.MethodGet && r.URL.Path == "/2015-02-01/mount-targets":
		f.writeList(w, "MountTargets", map[string]any{"MountTargetId": "fsmt-0001"})

	case r.Method == http.MethodPost && r.URL.Path == "/2015-02-01/access-points":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"AccessPointId": "fsap-0001", "LifeCycleState": "creating",
			"AccessPointArn": "arn:aws:elasticfilesystem:us-east-1:000000000000:access-point/fsap-0001",
		})
	case r.Method == http.MethodGet && r.URL.Path == "/2015-02-01/access-points":
		f.writeList(w, "AccessPoints", map[string]any{"AccessPointId": "fsap-0001"})

	default:
		http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
	}
}

// writeList answers one describe with a single record in the requested state,
// or with nothing at all when the resource is meant to have vanished.
func (f *fakeEFS) writeList(w http.ResponseWriter, field string, record map[string]any) {
	if f.empty {
		_ = json.NewEncoder(w).Encode(map[string]any{field: []any{}})
		return
	}
	record["LifeCycleState"] = f.script.next("available")
	_ = json.NewEncoder(w).Encode(map[string]any{field: []any{record}})
}

func efsMountTargetProps() map[string]any {
	return map[string]any{"FileSystemId": "fs-0001", "SubnetId": "subnet-0001"}
}

// A mount target is not done when CreateMountTarget answers — the export behind
// it is still coming up, and until it does nothing can mount the file system.
func TestEFSMountTargetCreate_waitsForItToBecomeAvailable(t *testing.T) {
	// Given: a mount target still creating for its first two checks
	f := &fakeEFS{script: statusScript{statuses: []string{"creating", "creating", "available"}}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "MountTarget",
		TemplateResource{Type: "AWS::EFS::MountTarget"}, efsMountTargetProps(), rCtx)

	// Then: it completed only once the mount target was available
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if id != "fsmt-0001" {
		t.Errorf("physical ID = %q, want %q", id, "fsmt-0001")
	}
	if got := f.script.count(); got != 3 {
		t.Errorf("DescribeMountTargets calls = %d, want 3 — the resource completed before the "+
			"mount target was available", got)
	}
}

// A mount target whose export cannot be brought up settles in "error", which
// EFS documents as a lifecycle state and Overcast writes for exactly this case.
// The resource has to fail on it, naming the state — not wait it out.
func TestEFSMountTargetCreate_failsOnTheErrorLifecycleState(t *testing.T) {
	// Given: a mount target whose export never comes up
	f := &fakeEFS{script: statusScript{statuses: []string{"creating", "error"}}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "MountTarget",
		TemplateResource{Type: "AWS::EFS::MountTarget"}, efsMountTargetProps(), rCtx)

	// Then: the resource fails on the state itself
	if err == nil {
		t.Fatal(`expected the resource to fail for a mount target in "error"`)
	}
	if !strings.Contains(err.Error(), "error") {
		t.Errorf("expected the mount target's own lifecycle state in the reason, got %v", err)
	}
	if strings.Contains(err.Error(), "within") {
		t.Errorf("a terminal lifecycle state was reported as a timeout: %v", err)
	}
	// The mount target exists — create named it before the export failed — and
	// rollback deletes it by that name.
	if id != "fsmt-0001" {
		t.Errorf("physical ID = %q, want %q — a mount target that failed still exists", id, "fsmt-0001")
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("DescribeMountTargets calls = %d, want 2 — the wait kept polling past a terminal state", got)
	}
}

// A mount target that never becomes available must not leave the stack complete
// around it: the resource fails, saying what it was waiting for.
func TestEFSMountTargetCreate_neverAvailableFailsTheResource(t *testing.T) {
	f := &fakeEFS{script: statusScript{statuses: []string{"creating"}}}
	p, rCtx := newTestProvisioner(t, f, newPollDrivenClock())

	_, err := p.provisionResource(context.Background(), "MountTarget",
		TemplateResource{Type: "AWS::EFS::MountTarget"}, efsMountTargetProps(), rCtx)
	if err == nil {
		t.Fatal("expected the resource to fail for a mount target that never became available")
	}
	for _, want := range []string{"fsmt-0001", "available", "creating"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure reason %q does not mention %q", err.Error(), want)
		}
	}
}

// A mount target deleted from under the stack is not going to become available.
func TestEFSMountTargetCreate_failsWhenItDisappears(t *testing.T) {
	f := &fakeEFS{empty: true}
	p, rCtx := newTestProvisioner(t, f)

	_, err := p.provisionResource(context.Background(), "MountTarget",
		TemplateResource{Type: "AWS::EFS::MountTarget"}, efsMountTargetProps(), rCtx)
	if err == nil {
		t.Fatal("expected the resource to fail for a mount target that no longer exists")
	}
	if !strings.Contains(err.Error(), "fsmt-0001") {
		t.Errorf("expected the failure to name the mount target, got %v", err)
	}
}

// A file system settles on the same rule: nothing can be mounted, and no mount
// target can be created, until the file system itself is available.
func TestEFSFileSystemCreate_waitsForItToBecomeAvailable(t *testing.T) {
	f := &fakeEFS{script: statusScript{statuses: []string{"creating", "available"}}}
	p, rCtx := newTestProvisioner(t, f)

	id, err := p.provisionResource(context.Background(), "FileSystem",
		TemplateResource{Type: "AWS::EFS::FileSystem"}, map[string]any{"Encrypted": true}, rCtx)
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if id != "fs-0001" {
		t.Errorf("physical ID = %q, want %q", id, "fs-0001")
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("DescribeFileSystems calls = %d, want 2 — the resource completed before the "+
			"file system was available", got)
	}
}

// An access point reports the same vocabulary, and a task that mounts through
// one cannot do so until it is available.
func TestEFSAccessPointCreate_waitsForItToBecomeAvailable(t *testing.T) {
	f := &fakeEFS{script: statusScript{statuses: []string{"creating", "available"}}}
	p, rCtx := newTestProvisioner(t, f)

	if _, err := p.provisionResource(context.Background(), "AccessPoint",
		TemplateResource{Type: "AWS::EFS::AccessPoint"}, map[string]any{"FileSystemId": "fs-0001"}, rCtx); err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if got := f.script.count(); got != 2 {
		t.Errorf("DescribeAccessPoints calls = %d, want 2 — the resource completed before the "+
			"access point was available", got)
	}
}

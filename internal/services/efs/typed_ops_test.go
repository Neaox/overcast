package efs

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/state"
)

func newTestService(t *testing.T) (*Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(), zap.NewNop(), mock,
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc, mock
}

// settle advances the mock clock so pending 0-delay lifecycle transitions fire.
func settle(mock *clock.Mock) { mock.Add(time.Millisecond) }

func mustCreateFS(t *testing.T, svc *Service, mock *clock.Mock, token string) *FileSystemDescription {
	t.Helper()
	fs, aerr := svc.createFileSystemTyped(context.Background(), &createFileSystemRequest{CreationToken: token})
	if aerr != nil {
		t.Fatalf("CreateFileSystem(%s): %v", token, aerr)
	}
	settle(mock)
	return fs
}

func expectAWSError(t *testing.T, aerr *protocol.AWSError, code string) {
	t.Helper()
	if aerr == nil {
		t.Fatalf("expected %s error, got success", code)
	}
	if aerr.Code != code {
		t.Fatalf("expected error code %s, got %s (%s)", code, aerr.Code, aerr.Message)
	}
}

func TestTypedOps_matchDispatchSurface(t *testing.T) {
	s := &Service{}
	ops := s.typedOps()
	expected := []string{
		"CreateFileSystem", "DescribeFileSystems", "DeleteFileSystem",
		"UpdateFileSystem", "UpdateFileSystemProtection",
		"CreateMountTarget", "DescribeMountTargets", "DeleteMountTarget",
		"DescribeMountTargetSecurityGroups", "ModifyMountTargetSecurityGroups",
		"CreateAccessPoint", "DescribeAccessPoints", "DeleteAccessPoint",
		"PutLifecycleConfiguration", "DescribeLifecycleConfiguration",
		"PutBackupPolicy", "DescribeBackupPolicy",
		"PutFileSystemPolicy", "DescribeFileSystemPolicy", "DeleteFileSystemPolicy",
		"TagResource", "UntagResource", "ListTagsForResource",
		"CreateTags", "DeleteTags", "DescribeTags",
		"DescribeAccountPreferences", "PutAccountPreferences",
	}
	if len(ops) != len(expected) {
		t.Fatalf("typed ops len = %d, expected %d", len(ops), len(expected))
	}
	for _, name := range expected {
		operation, ok := ops[name]
		if !ok {
			t.Fatalf("missing typed op %q", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed op %q has Name() %q", name, operation.Name())
		}
	}
	for name, operation := range ops {
		if _, ok := operation.(*op.Raw); ok {
			t.Fatalf("typed op %q is a Raw escape hatch — use NewTyped", name)
		}
	}
}

func TestCreateFileSystem_lifecycleAndIdempotency(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()

	// Given a create request; When it succeeds; Then the response is "creating".
	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "token-1"})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	if fs.LifeCycleState != stateCreating {
		t.Fatalf("expected creating, got %s", fs.LifeCycleState)
	}
	if fs.PerformanceMode != "generalPurpose" || fs.ThroughputMode != "bursting" {
		t.Fatalf("expected defaults generalPurpose/bursting, got %s/%s", fs.PerformanceMode, fs.ThroughputMode)
	}
	if fs.FileSystemArn == "" || fs.FileSystemId == "" {
		t.Fatal("expected FileSystemId and FileSystemArn to be set")
	}

	// When the transition fires; Then describe reports "available".
	settle(mock)
	resp, aerr := svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{FileSystemId: fs.FileSystemId})
	if aerr != nil {
		t.Fatalf("DescribeFileSystems: %v", aerr)
	}
	if got := resp.FileSystems[0].LifeCycleState; got != stateAvailable {
		t.Fatalf("expected available, got %s", got)
	}

	// When creating again with the same token; Then FileSystemAlreadyExists.
	_, aerr = svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "token-1"})
	expectAWSError(t, aerr, "FileSystemAlreadyExists")

	// And describe by creation token finds the same file system.
	byToken, aerr := svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{CreationToken: "token-1"})
	if aerr != nil {
		t.Fatalf("DescribeFileSystems by token: %v", aerr)
	}
	if byToken.FileSystems[0].FileSystemId != fs.FileSystemId {
		t.Fatal("describe by creation token returned a different file system")
	}
}

func TestCreateFileSystem_validation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tp := 100.0

	cases := []struct {
		name string
		req  createFileSystemRequest
		code string
	}{
		{"missing token", createFileSystemRequest{}, "BadRequest"},
		{"bad performance mode", createFileSystemRequest{CreationToken: "t", PerformanceMode: "turbo"}, "BadRequest"},
		{"bad throughput mode", createFileSystemRequest{CreationToken: "t", ThroughputMode: "warp"}, "BadRequest"},
		{"provisioned without throughput", createFileSystemRequest{CreationToken: "t", ThroughputMode: "provisioned"}, "BadRequest"},
		{"throughput with bursting", createFileSystemRequest{CreationToken: "t", ProvisionedThroughputInMibps: &tp}, "BadRequest"},
		{"maxIO with elastic", createFileSystemRequest{CreationToken: "t", PerformanceMode: "maxIO", ThroughputMode: "elastic"}, "BadRequest"},
		{"maxIO one zone", createFileSystemRequest{CreationToken: "t", PerformanceMode: "maxIO", AvailabilityZoneName: "us-east-1a"}, "BadRequest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, aerr := svc.createFileSystemTyped(ctx, &tc.req)
			expectAWSError(t, aerr, tc.code)
		})
	}
}

func TestUpdateFileSystem_throughput(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()
	fs := mustCreateFS(t, svc, mock, "upd")

	tp := 128.0
	updated, aerr := svc.updateFileSystemTyped(ctx, &updateFileSystemRequest{
		FileSystemId: fs.FileSystemId, ThroughputMode: "provisioned", ProvisionedThroughputInMibps: &tp,
	})
	if aerr != nil {
		t.Fatalf("UpdateFileSystem: %v", aerr)
	}
	if updated.ThroughputMode != "provisioned" || updated.ProvisionedThroughputInMibps == nil || *updated.ProvisionedThroughputInMibps != 128 {
		t.Fatalf("unexpected throughput state: %+v", updated)
	}

	// Switching back to bursting clears the provisioned figure.
	updated, aerr = svc.updateFileSystemTyped(ctx, &updateFileSystemRequest{FileSystemId: fs.FileSystemId, ThroughputMode: "bursting"})
	if aerr != nil {
		t.Fatalf("UpdateFileSystem to bursting: %v", aerr)
	}
	if updated.ProvisionedThroughputInMibps != nil {
		t.Fatal("expected ProvisionedThroughputInMibps cleared for bursting")
	}

	_, aerr = svc.updateFileSystemTyped(ctx, &updateFileSystemRequest{FileSystemId: "fs-00000000000000000"})
	expectAWSError(t, aerr, "FileSystemNotFound")
}

func TestMountTargets_lifecycleAndConflicts(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()

	// Given a file system still creating; Then CreateMountTarget is rejected.
	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "mt"})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	_, aerr = svc.createMountTargetTyped(ctx, &createMountTargetRequest{FileSystemId: fs.FileSystemId, SubnetId: "subnet-a"})
	expectAWSError(t, aerr, "IncorrectFileSystemLifeCycleState")
	settle(mock)

	// When the file system is available; Then the mount target is created.
	mt, aerr := svc.createMountTargetTyped(ctx, &createMountTargetRequest{
		FileSystemId: fs.FileSystemId, SubnetId: "subnet-a", SecurityGroups: []string{"sg-1"},
	})
	if aerr != nil {
		t.Fatalf("CreateMountTarget: %v", aerr)
	}
	if mt.LifeCycleState != stateCreating {
		t.Fatalf("expected creating, got %s", mt.LifeCycleState)
	}
	if mt.IpAddress == "" || mt.AvailabilityZoneName == "" || mt.NetworkInterfaceId == "" {
		t.Fatalf("expected synthesized network fields, got %+v", mt)
	}
	settle(mock)

	// Then the file system counts it and it becomes available.
	described, aerr := svc.describeMountTargetsTyped(ctx, &describeMountTargetsRequest{FileSystemId: fs.FileSystemId})
	if aerr != nil {
		t.Fatalf("DescribeMountTargets: %v", aerr)
	}
	if len(described.MountTargets) != 1 || described.MountTargets[0].LifeCycleState != stateAvailable {
		t.Fatalf("unexpected mount targets: %+v", described.MountTargets)
	}
	fsResp, _ := svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{FileSystemId: fs.FileSystemId})
	if fsResp.FileSystems[0].NumberOfMountTargets != 1 {
		t.Fatalf("expected NumberOfMountTargets=1, got %d", fsResp.FileSystems[0].NumberOfMountTargets)
	}

	// A second mount target in the same subnet conflicts.
	_, aerr = svc.createMountTargetTyped(ctx, &createMountTargetRequest{FileSystemId: fs.FileSystemId, SubnetId: "subnet-a"})
	expectAWSError(t, aerr, "MountTargetConflict")

	// Too many security groups are rejected.
	_, aerr = svc.createMountTargetTyped(ctx, &createMountTargetRequest{
		FileSystemId: fs.FileSystemId, SubnetId: "subnet-zzz",
		SecurityGroups: []string{"1", "2", "3", "4", "5", "6"},
	})
	expectAWSError(t, aerr, "SecurityGroupLimitExceeded")

	// Deleting the file system while the mount target exists conflicts.
	_, aerr = svc.deleteFileSystemTyped(ctx, &deleteFileSystemRequest{FileSystemId: fs.FileSystemId})
	expectAWSError(t, aerr, "FileSystemInUse")

	// Security groups round-trip through modify/describe.
	if _, aerr = svc.modifyMountTargetSecurityGroupsTyped(ctx, &mountTargetSecurityGroupsRequest{
		MountTargetId: mt.MountTargetId, SecurityGroups: []string{"sg-2", "sg-3"},
	}); aerr != nil {
		t.Fatalf("ModifyMountTargetSecurityGroups: %v", aerr)
	}
	groups, aerr := svc.describeMountTargetSecurityGroupsTyped(ctx, &mountTargetSecurityGroupsRequest{MountTargetId: mt.MountTargetId})
	if aerr != nil {
		t.Fatalf("DescribeMountTargetSecurityGroups: %v", aerr)
	}
	if len(groups.SecurityGroups) != 2 || groups.SecurityGroups[0] != "sg-2" {
		t.Fatalf("unexpected security groups: %v", groups.SecurityGroups)
	}

	// Delete the mount target, then the file system; both disappear.
	if _, aerr = svc.deleteMountTargetTyped(ctx, &deleteMountTargetRequest{MountTargetId: mt.MountTargetId}); aerr != nil {
		t.Fatalf("DeleteMountTarget: %v", aerr)
	}
	settle(mock)
	if _, aerr = svc.deleteFileSystemTyped(ctx, &deleteFileSystemRequest{FileSystemId: fs.FileSystemId}); aerr != nil {
		t.Fatalf("DeleteFileSystem: %v", aerr)
	}
	settle(mock)
	_, aerr = svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{FileSystemId: fs.FileSystemId})
	expectAWSError(t, aerr, "FileSystemNotFound")

	// Describe without any filter is rejected.
	_, aerr = svc.describeMountTargetsTyped(ctx, &describeMountTargetsRequest{})
	expectAWSError(t, aerr, "BadRequest")
}

func TestAccessPoints_lifecycle(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()
	fs := mustCreateFS(t, svc, mock, "ap")

	uid, gid := int64(1000), int64(1000)
	ap, aerr := svc.createAccessPointTyped(ctx, &createAccessPointRequest{
		ClientToken:  "ct-1",
		FileSystemId: fs.FileSystemId,
		PosixUser:    &PosixUser{Uid: &uid, Gid: &gid},
		Tags:         []Tag{{Key: "Name", Value: "my-ap"}},
	})
	if aerr != nil {
		t.Fatalf("CreateAccessPoint: %v", aerr)
	}
	if ap.LifeCycleState != stateCreating || ap.Name != "my-ap" {
		t.Fatalf("unexpected access point: %+v", ap)
	}
	if ap.RootDirectory == nil || ap.RootDirectory.Path != "/" {
		t.Fatalf("expected default root directory /, got %+v", ap.RootDirectory)
	}
	settle(mock)

	// Duplicate client token conflicts.
	_, aerr = svc.createAccessPointTyped(ctx, &createAccessPointRequest{ClientToken: "ct-1", FileSystemId: fs.FileSystemId})
	expectAWSError(t, aerr, "AccessPointAlreadyExists")

	// Filtering by both IDs is rejected.
	_, aerr = svc.describeAccessPointsTyped(ctx, &describeAccessPointsRequest{AccessPointId: ap.AccessPointId, FileSystemId: fs.FileSystemId})
	expectAWSError(t, aerr, "BadRequest")

	// Describe by file system sees it available.
	resp, aerr := svc.describeAccessPointsTyped(ctx, &describeAccessPointsRequest{FileSystemId: fs.FileSystemId})
	if aerr != nil {
		t.Fatalf("DescribeAccessPoints: %v", aerr)
	}
	if len(resp.AccessPoints) != 1 || resp.AccessPoints[0].LifeCycleState != stateAvailable {
		t.Fatalf("unexpected access points: %+v", resp.AccessPoints)
	}

	// DescribeMountTargets accepts an access-point filter.
	if _, aerr := svc.describeMountTargetsTyped(ctx, &describeMountTargetsRequest{AccessPointId: ap.AccessPointId}); aerr != nil {
		t.Fatalf("DescribeMountTargets by access point: %v", aerr)
	}

	// Deleting the file system cascades to its access points.
	if _, aerr = svc.deleteFileSystemTyped(ctx, &deleteFileSystemRequest{FileSystemId: fs.FileSystemId}); aerr != nil {
		t.Fatalf("DeleteFileSystem: %v", aerr)
	}
	settle(mock)
	_, aerr = svc.describeAccessPointsTyped(ctx, &describeAccessPointsRequest{AccessPointId: ap.AccessPointId})
	expectAWSError(t, aerr, "AccessPointNotFound")
}

func TestFileSystemPolicy_roundTrip(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()
	fs := mustCreateFS(t, svc, mock, "pol")

	_, aerr := svc.describeFileSystemPolicyTyped(ctx, &describeFileSystemPolicyRequest{FileSystemId: fs.FileSystemId})
	expectAWSError(t, aerr, "PolicyNotFound")

	_, aerr = svc.putFileSystemPolicyTyped(ctx, &putFileSystemPolicyRequest{FileSystemId: fs.FileSystemId, Policy: "{not json"})
	expectAWSError(t, aerr, "InvalidPolicyException")

	policy := `{"Version":"2012-10-17","Statement":[]}`
	put, aerr := svc.putFileSystemPolicyTyped(ctx, &putFileSystemPolicyRequest{FileSystemId: fs.FileSystemId, Policy: policy})
	if aerr != nil {
		t.Fatalf("PutFileSystemPolicy: %v", aerr)
	}
	if put.Policy != policy {
		t.Fatalf("policy not echoed: %s", put.Policy)
	}
	got, aerr := svc.describeFileSystemPolicyTyped(ctx, &describeFileSystemPolicyRequest{FileSystemId: fs.FileSystemId})
	if aerr != nil {
		t.Fatalf("DescribeFileSystemPolicy: %v", aerr)
	}
	if got.Policy != policy {
		t.Fatalf("expected stored policy, got %s", got.Policy)
	}
	if _, aerr = svc.deleteFileSystemPolicyTyped(ctx, &describeFileSystemPolicyRequest{FileSystemId: fs.FileSystemId}); aerr != nil {
		t.Fatalf("DeleteFileSystemPolicy: %v", aerr)
	}
	_, aerr = svc.describeFileSystemPolicyTyped(ctx, &describeFileSystemPolicyRequest{FileSystemId: fs.FileSystemId})
	expectAWSError(t, aerr, "PolicyNotFound")
}

func TestLifecycleConfiguration_validation(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()
	fs := mustCreateFS(t, svc, mock, "lc")

	// Empty by default.
	resp, aerr := svc.describeLifecycleConfigurationTyped(ctx, &describeLifecycleConfigurationRequest{FileSystemId: fs.FileSystemId})
	if aerr != nil {
		t.Fatalf("DescribeLifecycleConfiguration: %v", aerr)
	}
	if len(resp.LifecyclePolicies) != 0 {
		t.Fatalf("expected empty lifecycle policies, got %+v", resp.LifecyclePolicies)
	}

	_, aerr = svc.putLifecycleConfigurationTyped(ctx, &putLifecycleConfigurationRequest{
		FileSystemId:      fs.FileSystemId,
		LifecyclePolicies: []LifecyclePolicy{{TransitionToIA: "AFTER_5_MINUTES"}},
	})
	expectAWSError(t, aerr, "BadRequest")

	_, aerr = svc.putLifecycleConfigurationTyped(ctx, &putLifecycleConfigurationRequest{
		FileSystemId:      fs.FileSystemId,
		LifecyclePolicies: []LifecyclePolicy{{TransitionToIA: "AFTER_30_DAYS", TransitionToArchive: "AFTER_90_DAYS"}},
	})
	expectAWSError(t, aerr, "BadRequest")

	put, aerr := svc.putLifecycleConfigurationTyped(ctx, &putLifecycleConfigurationRequest{
		FileSystemId: fs.FileSystemId,
		LifecyclePolicies: []LifecyclePolicy{
			{TransitionToIA: "AFTER_30_DAYS"},
			{TransitionToPrimaryStorageClass: "AFTER_1_ACCESS"},
		},
	})
	if aerr != nil {
		t.Fatalf("PutLifecycleConfiguration: %v", aerr)
	}
	if len(put.LifecyclePolicies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(put.LifecyclePolicies))
	}
}

func TestBackupPolicy(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()

	backup := true
	fsOn, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "b-on", Backup: &backup})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	settle(mock)
	fsOff := mustCreateFS(t, svc, mock, "b-off")

	on, _ := svc.describeBackupPolicyTyped(ctx, &describeBackupPolicyRequest{FileSystemId: fsOn.FileSystemId})
	if on.BackupPolicy.Status != "ENABLED" {
		t.Fatalf("expected ENABLED from Backup flag, got %s", on.BackupPolicy.Status)
	}
	off, _ := svc.describeBackupPolicyTyped(ctx, &describeBackupPolicyRequest{FileSystemId: fsOff.FileSystemId})
	if off.BackupPolicy.Status != "DISABLED" {
		t.Fatalf("expected DISABLED default, got %s", off.BackupPolicy.Status)
	}

	_, aerr = svc.putBackupPolicyTyped(ctx, &putBackupPolicyRequest{FileSystemId: fsOff.FileSystemId, BackupPolicy: &BackupPolicy{Status: "SOMETIMES"}})
	expectAWSError(t, aerr, "BadRequest")

	set, aerr := svc.putBackupPolicyTyped(ctx, &putBackupPolicyRequest{FileSystemId: fsOff.FileSystemId, BackupPolicy: &BackupPolicy{Status: "ENABLED"}})
	if aerr != nil {
		t.Fatalf("PutBackupPolicy: %v", aerr)
	}
	if set.BackupPolicy.Status != "ENABLED" {
		t.Fatalf("expected ENABLED, got %s", set.BackupPolicy.Status)
	}
}

func TestTags_modernAndLegacy(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()
	fs := mustCreateFS(t, svc, mock, "tags")

	_, aerr := svc.tagResourceTyped(ctx, &tagResourceRequest{ResourceId: "fs-00000000000000000", Tags: []Tag{{Key: "a", Value: "b"}}})
	expectAWSError(t, aerr, "FileSystemNotFound")

	if _, aerr = svc.tagResourceTyped(ctx, &tagResourceRequest{
		ResourceId: fs.FileSystemId,
		Tags:       []Tag{{Key: "Name", Value: "shared-files"}, {Key: "env", Value: "dev"}},
	}); aerr != nil {
		t.Fatalf("TagResource: %v", aerr)
	}

	// The Name tag surfaces on the file system description; ARNs resolve too.
	described, _ := svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{FileSystemId: fs.FileSystemId})
	if described.FileSystems[0].Name != "shared-files" {
		t.Fatalf("expected Name from tag, got %q", described.FileSystems[0].Name)
	}
	listed, aerr := svc.listTagsForResourceTyped(ctx, &listTagsForResourceRequest{ResourceId: described.FileSystems[0].FileSystemArn})
	if aerr != nil {
		t.Fatalf("ListTagsForResource by ARN: %v", aerr)
	}
	if len(listed.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %+v", listed.Tags)
	}

	// Legacy API views the same tag set.
	legacy, aerr := svc.describeTagsTyped(ctx, &describeTagsRequest{FileSystemId: fs.FileSystemId})
	if aerr != nil {
		t.Fatalf("DescribeTags: %v", aerr)
	}
	if len(legacy.Tags) != 2 {
		t.Fatalf("expected 2 tags via legacy API, got %+v", legacy.Tags)
	}
	if _, aerr = svc.deleteTagsTyped(ctx, &deleteTagsRequest{FileSystemId: fs.FileSystemId, TagKeys: []string{"env"}}); aerr != nil {
		t.Fatalf("DeleteTags: %v", aerr)
	}
	if _, aerr = svc.untagResourceTyped(ctx, &untagResourceRequest{ResourceId: fs.FileSystemId, TagKeys: []string{"Name"}}); aerr != nil {
		t.Fatalf("UntagResource: %v", aerr)
	}
	remaining, _ := svc.listTagsForResourceTyped(ctx, &listTagsForResourceRequest{ResourceId: fs.FileSystemId})
	if len(remaining.Tags) != 0 {
		t.Fatalf("expected no tags left, got %+v", remaining.Tags)
	}
}

func TestDescribeFileSystems_pagination(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()
	for _, token := range []string{"p1", "p2", "p3"} {
		mustCreateFS(t, svc, mock, token)
	}

	first, aerr := svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{MaxItems: 2})
	if aerr != nil {
		t.Fatalf("DescribeFileSystems page 1: %v", aerr)
	}
	if len(first.FileSystems) != 2 || first.NextMarker == "" {
		t.Fatalf("expected 2 items + NextMarker, got %d items marker=%q", len(first.FileSystems), first.NextMarker)
	}
	second, aerr := svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{MaxItems: 2, Marker: first.NextMarker})
	if aerr != nil {
		t.Fatalf("DescribeFileSystems page 2: %v", aerr)
	}
	if len(second.FileSystems) != 1 || second.NextMarker != "" {
		t.Fatalf("expected final page with 1 item, got %d items marker=%q", len(second.FileSystems), second.NextMarker)
	}
	if second.Marker != first.NextMarker {
		t.Fatalf("expected Marker echoed, got %q", second.Marker)
	}

	_, aerr = svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{Marker: "garbage"})
	expectAWSError(t, aerr, "BadRequest")
}

func TestAccountPreferences(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	def, aerr := svc.describeAccountPreferencesTyped(ctx, &describeAccountPreferencesRequest{})
	if aerr != nil {
		t.Fatalf("DescribeAccountPreferences: %v", aerr)
	}
	if def.ResourceIdPreference.ResourceIdType != "LONG_ID" {
		t.Fatalf("expected LONG_ID default, got %s", def.ResourceIdPreference.ResourceIdType)
	}

	_, aerr = svc.putAccountPreferencesTyped(ctx, &putAccountPreferencesRequest{ResourceIdType: "MEDIUM_ID"})
	expectAWSError(t, aerr, "BadRequest")

	if _, aerr = svc.putAccountPreferencesTyped(ctx, &putAccountPreferencesRequest{ResourceIdType: "SHORT_ID"}); aerr != nil {
		t.Fatalf("PutAccountPreferences: %v", aerr)
	}
	got, _ := svc.describeAccountPreferencesTyped(ctx, &describeAccountPreferencesRequest{})
	if got.ResourceIdPreference.ResourceIdType != "SHORT_ID" {
		t.Fatalf("expected SHORT_ID, got %s", got.ResourceIdPreference.ResourceIdType)
	}
}

func TestUpdateFileSystemProtection(t *testing.T) {
	svc, mock := newTestService(t)
	ctx := context.Background()
	fs := mustCreateFS(t, svc, mock, "prot")

	_, aerr := svc.updateFileSystemProtectionTyped(ctx, &updateFileSystemProtectionRequest{FileSystemId: fs.FileSystemId, ReplicationOverwriteProtection: "MAYBE"})
	expectAWSError(t, aerr, "BadRequest")

	out, aerr := svc.updateFileSystemProtectionTyped(ctx, &updateFileSystemProtectionRequest{FileSystemId: fs.FileSystemId, ReplicationOverwriteProtection: "DISABLED"})
	if aerr != nil {
		t.Fatalf("UpdateFileSystemProtection: %v", aerr)
	}
	if out.ReplicationOverwriteProtection != "DISABLED" {
		t.Fatalf("expected DISABLED, got %s", out.ReplicationOverwriteProtection)
	}
	described, _ := svc.describeFileSystemsTyped(ctx, &describeFileSystemsRequest{FileSystemId: fs.FileSystemId})
	if described.FileSystems[0].FileSystemProtection.ReplicationOverwriteProtection != "DISABLED" {
		t.Fatal("protection change not reflected in describe")
	}
}

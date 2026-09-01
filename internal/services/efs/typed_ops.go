package efs

import (
	"github.com/overcast-sh/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		// File systems
		"CreateFileSystem": op.NewTyped[createFileSystemRequest, FileSystemDescription](
			"CreateFileSystem", s.createFileSystemTyped,
		),
		"DescribeFileSystems": op.NewTyped[describeFileSystemsRequest, describeFileSystemsResponse](
			"DescribeFileSystems", s.describeFileSystemsTyped,
		),
		"DeleteFileSystem": op.NewTypedAny[deleteFileSystemRequest](
			"DeleteFileSystem", s.deleteFileSystemTyped,
		),
		"UpdateFileSystem": op.NewTyped[updateFileSystemRequest, FileSystemDescription](
			"UpdateFileSystem", s.updateFileSystemTyped,
		),
		"UpdateFileSystemProtection": op.NewTyped[updateFileSystemProtectionRequest, FileSystemProtectionDescription](
			"UpdateFileSystemProtection", s.updateFileSystemProtectionTyped,
		),
		// Mount targets
		"CreateMountTarget": op.NewTyped[createMountTargetRequest, MountTargetDescription](
			"CreateMountTarget", s.createMountTargetTyped,
		),
		"DescribeMountTargets": op.NewTyped[describeMountTargetsRequest, describeMountTargetsResponse](
			"DescribeMountTargets", s.describeMountTargetsTyped,
		),
		"DeleteMountTarget": op.NewTypedAny[deleteMountTargetRequest](
			"DeleteMountTarget", s.deleteMountTargetTyped,
		),
		"DescribeMountTargetSecurityGroups": op.NewTyped[mountTargetSecurityGroupsRequest, describeMountTargetSecurityGroupsResponse](
			"DescribeMountTargetSecurityGroups", s.describeMountTargetSecurityGroupsTyped,
		),
		"ModifyMountTargetSecurityGroups": op.NewTypedAny[mountTargetSecurityGroupsRequest](
			"ModifyMountTargetSecurityGroups", s.modifyMountTargetSecurityGroupsTyped,
		),
		// Access points
		"CreateAccessPoint": op.NewTyped[createAccessPointRequest, AccessPointDescription](
			"CreateAccessPoint", s.createAccessPointTyped,
		),
		"DescribeAccessPoints": op.NewTyped[describeAccessPointsRequest, describeAccessPointsResponse](
			"DescribeAccessPoints", s.describeAccessPointsTyped,
		),
		"DeleteAccessPoint": op.NewTypedAny[deleteAccessPointRequest](
			"DeleteAccessPoint", s.deleteAccessPointTyped,
		),
		// Lifecycle configuration
		"PutLifecycleConfiguration": op.NewTyped[putLifecycleConfigurationRequest, lifecycleConfigurationResponse](
			"PutLifecycleConfiguration", s.putLifecycleConfigurationTyped,
		),
		"DescribeLifecycleConfiguration": op.NewTyped[describeLifecycleConfigurationRequest, lifecycleConfigurationResponse](
			"DescribeLifecycleConfiguration", s.describeLifecycleConfigurationTyped,
		),
		// Backup policy
		"PutBackupPolicy": op.NewTyped[putBackupPolicyRequest, backupPolicyResponse](
			"PutBackupPolicy", s.putBackupPolicyTyped,
		),
		"DescribeBackupPolicy": op.NewTyped[describeBackupPolicyRequest, backupPolicyResponse](
			"DescribeBackupPolicy", s.describeBackupPolicyTyped,
		),
		// File-system policy
		"PutFileSystemPolicy": op.NewTyped[putFileSystemPolicyRequest, fileSystemPolicyResponse](
			"PutFileSystemPolicy", s.putFileSystemPolicyTyped,
		),
		"DescribeFileSystemPolicy": op.NewTyped[describeFileSystemPolicyRequest, fileSystemPolicyResponse](
			"DescribeFileSystemPolicy", s.describeFileSystemPolicyTyped,
		),
		"DeleteFileSystemPolicy": op.NewTypedAny[describeFileSystemPolicyRequest](
			"DeleteFileSystemPolicy", s.deleteFileSystemPolicyTyped,
		),
		// Tags
		"TagResource": op.NewTypedAny[tagResourceRequest](
			"TagResource", s.tagResourceTyped,
		),
		"UntagResource": op.NewTypedAny[untagResourceRequest](
			"UntagResource", s.untagResourceTyped,
		),
		"ListTagsForResource": op.NewTyped[listTagsForResourceRequest, listTagsForResourceResponse](
			"ListTagsForResource", s.listTagsForResourceTyped,
		),
		"CreateTags": op.NewTypedAny[createTagsRequest](
			"CreateTags", s.createTagsTyped,
		),
		"DeleteTags": op.NewTypedAny[deleteTagsRequest](
			"DeleteTags", s.deleteTagsTyped,
		),
		"DescribeTags": op.NewTyped[describeTagsRequest, describeTagsResponse](
			"DescribeTags", s.describeTagsTyped,
		),
		// Account preferences
		"DescribeAccountPreferences": op.NewTyped[describeAccountPreferencesRequest, accountPreferencesResponse](
			"DescribeAccountPreferences", s.describeAccountPreferencesTyped,
		),
		"PutAccountPreferences": op.NewTyped[putAccountPreferencesRequest, accountPreferencesResponse](
			"PutAccountPreferences", s.putAccountPreferencesTyped,
		),
	}
}

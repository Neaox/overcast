//go:build dev

package efs

import "github.com/overcast-sh/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// File systems
		capabilities.Capability{Service: "efs", Operation: "CreateFileSystem", Category: "File systems", Status: capabilities.StatusSupported, Notes: "Creation-token idempotency, performance/throughput mode validation, One Zone metadata, optional Backup flag and inline tags; lifecycle creating → available"},
		capabilities.Capability{Service: "efs", Operation: "DescribeFileSystems", Category: "File systems", Status: capabilities.StatusSupported, Notes: "Filters by FileSystemId or CreationToken; Marker/MaxItems pagination"},
		capabilities.Capability{Service: "efs", Operation: "UpdateFileSystem", Category: "File systems", Status: capabilities.StatusSupported, Notes: "Updates ThroughputMode and ProvisionedThroughputInMibps with pairing validation"},
		capabilities.Capability{Service: "efs", Operation: "DeleteFileSystem", Category: "File systems", Status: capabilities.StatusSupported, Notes: "Rejects deletion while mount targets exist (FileSystemInUse); cascades access points, tags, and policy"},
		capabilities.Capability{Service: "efs", Operation: "UpdateFileSystemProtection", Category: "File systems", Status: capabilities.StatusSupported, Notes: "Stores ReplicationOverwriteProtection"},
		// Mount targets
		capabilities.Capability{Service: "efs", Operation: "CreateMountTarget", Category: "Mount targets", Status: capabilities.StatusSupported, Notes: "Serves a real NFSv4 export with OVERCAST_EFS_NFS=true, metadata only otherwise. One mount target per AZ/subnet enforced; AZ and IP are synthesized deterministically from the subnet ID"},
		capabilities.Capability{Service: "efs", Operation: "DescribeMountTargets", Category: "Mount targets", Status: capabilities.StatusSupported, Notes: "Lookup by FileSystemId, MountTargetId, or AccessPointId; Marker/MaxItems pagination"},
		capabilities.Capability{Service: "efs", Operation: "DeleteMountTarget", Category: "Mount targets", Status: capabilities.StatusSupported, Notes: "Lifecycle deleting → removed"},
		capabilities.Capability{Service: "efs", Operation: "DescribeMountTargetSecurityGroups", Category: "Mount targets", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "efs", Operation: "ModifyMountTargetSecurityGroups", Category: "Mount targets", Status: capabilities.StatusSupported, Notes: "Enforces the 5-security-group limit; groups are stored, not validated against EC2"},
		// Access points
		capabilities.Capability{Service: "efs", Operation: "CreateAccessPoint", Category: "Access points", Status: capabilities.StatusSupported, Notes: "Client-token idempotency conflict, PosixUser/RootDirectory stored, inline tags; lifecycle creating → available"},
		capabilities.Capability{Service: "efs", Operation: "DescribeAccessPoints", Category: "Access points", Status: capabilities.StatusSupported, Notes: "Filters by AccessPointId or FileSystemId (mutually exclusive); NextToken/MaxResults pagination"},
		capabilities.Capability{Service: "efs", Operation: "DeleteAccessPoint", Category: "Access points", Status: capabilities.StatusSupported},
		// Lifecycle configuration
		capabilities.Capability{Service: "efs", Operation: "PutLifecycleConfiguration", Category: "Lifecycle", Status: capabilities.StatusSupported, Notes: "Validates transition enums and the one-transition-per-policy rule; storage-class transitions are metadata only"},
		capabilities.Capability{Service: "efs", Operation: "DescribeLifecycleConfiguration", Category: "Lifecycle", Status: capabilities.StatusSupported},
		// Backup policy
		capabilities.Capability{Service: "efs", Operation: "PutBackupPolicy", Category: "Backup", Status: capabilities.StatusSupported, Notes: "Stores ENABLED/DISABLED directly (no ENABLING/DISABLING transitional states); no AWS Backup integration"},
		capabilities.Capability{Service: "efs", Operation: "DescribeBackupPolicy", Category: "Backup", Status: capabilities.StatusSupported},
		// File-system policy
		capabilities.Capability{Service: "efs", Operation: "PutFileSystemPolicy", Category: "Policy", Status: capabilities.StatusSupported, Notes: "Stores the policy document (JSON-validated); not enforced on requests"},
		capabilities.Capability{Service: "efs", Operation: "DescribeFileSystemPolicy", Category: "Policy", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "efs", Operation: "DeleteFileSystemPolicy", Category: "Policy", Status: capabilities.StatusSupported},
		// Tags
		capabilities.Capability{Service: "efs", Operation: "TagResource", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Accepts file-system and access-point IDs or ARNs"},
		capabilities.Capability{Service: "efs", Operation: "UntagResource", Category: "Tags", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "efs", Operation: "ListTagsForResource", Category: "Tags", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "efs", Operation: "CreateTags", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Legacy alias of TagResource (file systems only)"},
		capabilities.Capability{Service: "efs", Operation: "DeleteTags", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Legacy alias of UntagResource (file systems only)"},
		capabilities.Capability{Service: "efs", Operation: "DescribeTags", Category: "Tags", Status: capabilities.StatusSupported, Notes: "Legacy alias of ListTagsForResource (file systems only)"},
		// Account preferences
		capabilities.Capability{Service: "efs", Operation: "DescribeAccountPreferences", Category: "Account", Status: capabilities.StatusSupported, Notes: "Defaults to LONG_ID"},
		capabilities.Capability{Service: "efs", Operation: "PutAccountPreferences", Category: "Account", Status: capabilities.StatusSupported, Notes: "Stores the preference; generated IDs are always long-form"},
		// Replication — not emulated
		capabilities.Capability{Service: "efs", Operation: "CreateReplicationConfiguration", Category: "Replication", Status: capabilities.StatusUnsupported},
		capabilities.Capability{Service: "efs", Operation: "DeleteReplicationConfiguration", Category: "Replication", Status: capabilities.StatusUnsupported},
		capabilities.Capability{Service: "efs", Operation: "DescribeReplicationConfigurations", Category: "Replication", Status: capabilities.StatusUnsupported},
	)
}

//go:build dev

package rds

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// DB instances
		capabilities.Capability{Service: "rds", Operation: "CreateDBInstance", Category: "DB instances", Status: capabilities.StatusSupported, Notes: "Docker-backed when available; async creating→available; mysql/postgres/mariadb/aurora-mysql/aurora-postgresql; accepts `DBClusterIdentifier` for Aurora"},
		capabilities.Capability{Service: "rds", Operation: "DescribeDBInstances", Category: "DB instances", Status: capabilities.StatusSupported, Notes: "List all or filter by DBInstanceIdentifier"},
		capabilities.Capability{Service: "rds", Operation: "DeleteDBInstance", Category: "DB instances", Status: capabilities.StatusSupported, Notes: "Sets status to \"deleting\"; stops+removes Docker container"},
		capabilities.Capability{Service: "rds", Operation: "StopDBInstance", Category: "DB instances", Status: capabilities.StatusSupported, Notes: "Stops Docker container; available→stopping→stopped"},
		capabilities.Capability{Service: "rds", Operation: "StartDBInstance", Category: "DB instances", Status: capabilities.StatusSupported, Notes: "Starts Docker container; stopped→starting→available"},
		capabilities.Capability{Service: "rds", Operation: "ModifyDBInstance", Category: "DB instances", Status: capabilities.StatusSupported, Notes: "Metadata updates (class, storage, engine version, multi-AZ)"},
		capabilities.Capability{Service: "rds", Operation: "RebootDBInstance", Category: "DB instances", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "CreateDBSnapshot", Category: "DB instances", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "DeleteDBSnapshot", Category: "DB instances", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "DescribeDBSnapshots", Category: "DB instances", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "RestoreDBInstanceFromDBSnapshot", Category: "DB instances", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "DescribeDBLogFiles", Category: "DB instances", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "DownloadDBLogFilePortion", Category: "DB instances", Status: capabilities.StatusSupported},
		// Aurora clusters
		capabilities.Capability{Service: "rds", Operation: "CreateDBCluster", Category: "Aurora clusters", Status: capabilities.StatusSupported, Notes: "aurora-mysql and aurora-postgresql only; logical cluster, Docker started on first instance"},
		capabilities.Capability{Service: "rds", Operation: "DescribeDBClusters", Category: "Aurora clusters", Status: capabilities.StatusSupported, Notes: "List all or filter by DBClusterIdentifier; returns cluster members"},
		capabilities.Capability{Service: "rds", Operation: "DeleteDBCluster", Category: "Aurora clusters", Status: capabilities.StatusSupported, Notes: "Sets status to \"deleting\"; async removal"},
		capabilities.Capability{Service: "rds", Operation: "ModifyDBCluster", Category: "Aurora clusters", Status: capabilities.StatusSupported, Notes: "Engine version update"},
		capabilities.Capability{Service: "rds", Operation: "StartDBCluster", Category: "Aurora clusters", Status: capabilities.StatusSupported, Notes: "stopped→starting→available"},
		capabilities.Capability{Service: "rds", Operation: "StopDBCluster", Category: "Aurora clusters", Status: capabilities.StatusSupported, Notes: "available→stopping→stopped"},
		capabilities.Capability{Service: "rds", Operation: "CreateDBClusterSnapshot", Category: "Aurora clusters", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "DeleteDBClusterSnapshot", Category: "Aurora clusters", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "DescribeDBClusterSnapshots", Category: "Aurora clusters", Status: capabilities.StatusSupported},
		// Engine metadata
		capabilities.Capability{Service: "rds", Operation: "DescribeDBEngineVersions", Category: "Engine metadata", Status: capabilities.StatusSupported, Notes: "mysql (8.0, 5.7), postgres (16.1, 15.5, 14.11), mariadb (11.4, 10.11), aurora-mysql (3.04, 2.11), aurora-postgresql (15.4, 14.11)"},
		capabilities.Capability{Service: "rds", Operation: "DescribeOrderableDBInstanceOptions", Category: "Engine metadata", Status: capabilities.StatusSupported, Notes: "Static list of engine + instance class combos for mysql/postgres/mariadb"},
		// Subnet groups
		capabilities.Capability{Service: "rds", Operation: "CreateDBSubnetGroup", Category: "Subnet groups", Status: capabilities.StatusSupported, Notes: "Metadata-only; stores subnet IDs and VPC ID"},
		capabilities.Capability{Service: "rds", Operation: "DescribeDBSubnetGroups", Category: "Subnet groups", Status: capabilities.StatusSupported, Notes: "List all or filter by name"},
		capabilities.Capability{Service: "rds", Operation: "DeleteDBSubnetGroup", Category: "Subnet groups", Status: capabilities.StatusSupported},
		// Parameter groups
		capabilities.Capability{Service: "rds", Operation: "CreateDBParameterGroup", Category: "Parameter groups", Status: capabilities.StatusSupported, Notes: "Validates family against known engines; stores in state"},
		capabilities.Capability{Service: "rds", Operation: "DescribeDBParameterGroups", Category: "Parameter groups", Status: capabilities.StatusSupported, Notes: "List all or filter by name"},
		capabilities.Capability{Service: "rds", Operation: "DeleteDBParameterGroup", Category: "Parameter groups", Status: capabilities.StatusSupported},
		// General
		capabilities.Capability{Service: "rds", Operation: "AddTagsToResource", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "ListTagsForResource", Category: "General", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "rds", Operation: "RemoveTagsFromResource", Category: "General", Status: capabilities.StatusSupported},
	)
}

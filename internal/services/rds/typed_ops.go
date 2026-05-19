package rds

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		// Instance operations
		"CreateDBInstance":         op.NewTyped("CreateDBInstance", h.createDBInstanceTyped),
		"DescribeDBInstances":      op.NewTyped("DescribeDBInstances", h.describeDBInstancesTyped),
		"DeleteDBInstance":         op.NewTyped("DeleteDBInstance", h.deleteDBInstanceTyped),
		"DescribeDBEngineVersions": op.NewTyped("DescribeDBEngineVersions", h.describeDBEngineVersionsTyped),
		"StopDBInstance":           op.NewTyped("StopDBInstance", h.stopDBInstanceTyped),
		"StartDBInstance":          op.NewTyped("StartDBInstance", h.startDBInstanceTyped),
		"ModifyDBInstance":         op.NewTyped("ModifyDBInstance", h.modifyDBInstanceTyped),
		// Subnet group operations
		"CreateDBSubnetGroup":    op.NewTyped("CreateDBSubnetGroup", h.createDBSubnetGroupTyped),
		"DeleteDBSubnetGroup":    op.NewTyped("DeleteDBSubnetGroup", h.deleteDBSubnetGroupTyped),
		"DescribeDBSubnetGroups": op.NewTyped("DescribeDBSubnetGroups", h.describeDBSubnetGroupsTyped),
		// Parameter group operations
		"CreateDBParameterGroup":             op.NewTyped("CreateDBParameterGroup", h.createDBParameterGroupTyped),
		"DeleteDBParameterGroup":             op.NewTyped("DeleteDBParameterGroup", h.deleteDBParameterGroupTyped),
		"DescribeDBParameterGroups":          op.NewTyped("DescribeDBParameterGroups", h.describeDBParameterGroupsTyped),
		"DescribeOrderableDBInstanceOptions": op.NewTyped("DescribeOrderableDBInstanceOptions", h.describeOrderableDBInstanceOptionsTyped),
		// Aurora cluster operations
		"CreateDBCluster":    op.NewTyped("CreateDBCluster", h.createDBClusterTyped),
		"DeleteDBCluster":    op.NewTyped("DeleteDBCluster", h.deleteDBClusterTyped),
		"DescribeDBClusters": op.NewTyped("DescribeDBClusters", h.describeDBClustersTyped),
		"ModifyDBCluster":    op.NewTyped("ModifyDBCluster", h.modifyDBClusterTyped),
		"StartDBCluster":     op.NewTyped("StartDBCluster", h.startDBClusterTyped),
		"StopDBCluster":      op.NewTyped("StopDBCluster", h.stopDBClusterTyped),
		// Stub operations (not yet implemented)
		"CreateDBSnapshot":                op.NewRaw("CreateDBSnapshot", h.CreateDBSnapshot),
		"DeleteDBSnapshot":                op.NewRaw("DeleteDBSnapshot", h.DeleteDBSnapshot),
		"DescribeDBSnapshots":             op.NewRaw("DescribeDBSnapshots", h.DescribeDBSnapshots),
		"RestoreDBInstanceFromDBSnapshot": op.NewRaw("RestoreDBInstanceFromDBSnapshot", h.RestoreDBInstanceFromDBSnapshot),
		"CreateDBClusterSnapshot":         op.NewRaw("CreateDBClusterSnapshot", h.CreateDBClusterSnapshot),
		"DeleteDBClusterSnapshot":         op.NewRaw("DeleteDBClusterSnapshot", h.DeleteDBClusterSnapshot),
		"DescribeDBClusterSnapshots":      op.NewRaw("DescribeDBClusterSnapshots", h.DescribeDBClusterSnapshots),
		"RebootDBInstance":                op.NewRaw("RebootDBInstance", h.RebootDBInstance),
		"DescribeDBLogFiles":              op.NewRaw("DescribeDBLogFiles", h.DescribeDBLogFiles),
		"DownloadDBLogFilePortion":        op.NewRaw("DownloadDBLogFilePortion", h.DownloadDBLogFilePortion),
		"AddTagsToResource":               op.NewRaw("AddTagsToResource", h.AddTagsToResource),
		"RemoveTagsFromResource":          op.NewRaw("RemoveTagsFromResource", h.RemoveTagsFromResource),
		"ListTagsForResource":             op.NewRaw("ListTagsForResource", h.ListTagsForResource),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}

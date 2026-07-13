package elasticache

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateCacheCluster":           op.NewTyped[ecCreateCacheClusterReq, ecCreateCacheClusterResp]("CreateCacheCluster", h.createCacheClusterTyped),
		"DescribeCacheClusters":        op.NewTyped[ecDescribeCacheClustersReq, ecDescribeCacheClustersResp]("DescribeCacheClusters", h.describeCacheClustersTyped),
		"DeleteCacheCluster":           op.NewTyped[ecDeleteCacheClusterReq, ecDeleteCacheClusterResp]("DeleteCacheCluster", h.deleteCacheClusterTyped),
		"CreateServerlessCache":        op.NewRaw("CreateServerlessCache", h.CreateServerlessCache),
		"DescribeServerlessCaches":     op.NewRaw("DescribeServerlessCaches", h.DescribeServerlessCaches),
		"DeleteServerlessCache":        op.NewRaw("DeleteServerlessCache", h.DeleteServerlessCache),
		"ModifyServerlessCache":        op.NewRaw("ModifyServerlessCache", h.ModifyServerlessCache),
		"CreateReplicationGroup":       op.NewTyped[ecCreateReplicationGroupReq, ecCreateReplicationGroupResp]("CreateReplicationGroup", h.createReplicationGroupTyped),
		"DescribeReplicationGroups":    op.NewTyped[ecDescribeReplicationGroupsReq, ecDescribeReplicationGroupsResp]("DescribeReplicationGroups", h.describeReplicationGroupsTyped),
		"DeleteReplicationGroup":       op.NewTyped[ecDeleteReplicationGroupReq, ecDeleteReplicationGroupResp]("DeleteReplicationGroup", h.deleteReplicationGroupTyped),
		"CreateCacheSubnetGroup":       op.NewTyped[ecCreateCacheSubnetGroupReq, ecCreateCacheSubnetGroupResp]("CreateCacheSubnetGroup", h.createCacheSubnetGroupTyped),
		"DescribeCacheSubnetGroups":    op.NewTyped[ecDescribeCacheSubnetGroupsReq, ecDescribeCacheSubnetGroupsResp]("DescribeCacheSubnetGroups", h.describeCacheSubnetGroupsTyped),
		"DeleteCacheSubnetGroup":       op.NewTyped[ecDeleteCacheSubnetGroupReq, ecDeleteCacheSubnetGroupResp]("DeleteCacheSubnetGroup", h.deleteCacheSubnetGroupTyped),
		"CreateCacheParameterGroup":    op.NewTyped[ecCreateCacheParameterGroupReq, ecCreateCacheParameterGroupResp]("CreateCacheParameterGroup", h.createCacheParameterGroupTyped),
		"DescribeCacheParameterGroups": op.NewTyped[ecDescribeCacheParameterGroupsReq, ecDescribeCacheParameterGroupsResp]("DescribeCacheParameterGroups", h.describeCacheParameterGroupsTyped),
		"DeleteCacheParameterGroup":    op.NewTyped[ecDeleteCacheParameterGroupReq, ecDeleteCacheParameterGroupResp]("DeleteCacheParameterGroup", h.deleteCacheParameterGroupTyped),
		"DescribeCacheParameters":      op.NewTyped[ecDescribeCacheParametersReq, ecDescribeCacheParametersResp]("DescribeCacheParameters", h.describeCacheParametersTyped),
		"ModifyCacheCluster":           op.NewTyped[ecModifyCacheClusterReq, ecModifyCacheClusterResp]("ModifyCacheCluster", h.modifyCacheClusterTyped),
		"ModifyReplicationGroup":       op.NewTyped[ecModifyReplicationGroupReq, ecModifyReplicationGroupResp]("ModifyReplicationGroup", h.modifyReplicationGroupTyped),
		// Tag operations use non-standard list format (Tag.N.Key) — keep legacy handlers.
		"AddTagsToResource":      op.NewRaw("AddTagsToResource", h.AddTagsToResource),
		"ListTagsForResource":    op.NewRaw("ListTagsForResource", h.ListTagsForResource),
		"RemoveTagsFromResource": op.NewRaw("RemoveTagsFromResource", h.RemoveTagsFromResource),
		// Stubs
		"RebootCacheCluster":          op.NewRaw("RebootCacheCluster", h.RebootCacheCluster),
		"DescribeCacheEngineVersions": op.NewRaw("DescribeCacheEngineVersions", h.DescribeCacheEngineVersions),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, o := range ops {
		out = append(out, o)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}

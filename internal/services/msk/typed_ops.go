package msk

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateCluster": op.NewTyped[createClusterRequest, createClusterResponse](
			"CreateCluster", s.createClusterTyped,
		),
		"DescribeCluster": op.NewTyped[describeClusterRequest, describeClusterResponse](
			"DescribeCluster", s.describeClusterTyped,
		),
		"ListClusters": op.NewTyped[listClustersRequest, listClustersResponse](
			"ListClusters", s.listClustersTyped,
		),
		"DeleteCluster": op.NewTyped[deleteClusterRequest, deleteClusterResponse](
			"DeleteCluster", s.deleteClusterTyped,
		),
		"GetBootstrapBrokers": op.NewTyped[getBootstrapBrokersRequest, getBootstrapBrokersResponse](
			"GetBootstrapBrokers", s.getBootstrapBrokersTyped,
		),
		"CreateConfiguration": op.NewTyped[createConfigurationRequest, createConfigurationResponse](
			"CreateConfiguration", s.createConfigurationTyped,
		),
		"DescribeConfiguration": op.NewTyped[describeConfigurationRequest, describeConfigurationResponse](
			"DescribeConfiguration", s.describeConfigurationTyped,
		),
		"ListConfigurations": op.NewTyped[listConfigurationsRequest, listConfigurationsResponse](
			"ListConfigurations", s.listConfigurationsTyped,
		),
		"DeleteConfiguration": op.NewTyped[deleteConfigurationRequest, struct{}](
			"DeleteConfiguration", s.deleteConfigurationTyped,
		),
		"ListKafkaVersions": op.NewTyped[listKafkaVersionsRequest, listKafkaVersionsResponse](
			"ListKafkaVersions", s.listKafkaVersionsTyped,
		),
		"TagResource": op.NewTyped[tagResourceRequest, struct{}](
			"TagResource", s.tagResourceTyped,
		),
		"UntagResource": op.NewTyped[untagResourceRequest, struct{}](
			"UntagResource", s.untagResourceTyped,
		),
		"ListTagsForResource": op.NewTyped[listTagsForResourceRequest, listTagsForResourceResponse](
			"ListTagsForResource", s.listTagsForResourceTyped,
		),
		"CreateClusterV2": op.NewTyped[createClusterV2Request, createClusterV2Response](
			"CreateClusterV2", s.createClusterV2Typed,
		),
		"DescribeClusterV2": op.NewTyped[describeClusterV2Request, describeClusterV2Response](
			"DescribeClusterV2", s.describeClusterV2Typed,
		),
		"UpdateClusterConfiguration": op.NewTyped[updateClusterConfigurationRequest, updateClusterConfigurationResponse](
			"UpdateClusterConfiguration", s.updateClusterConfigurationTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}

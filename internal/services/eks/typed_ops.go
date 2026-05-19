package eks

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
		"UpdateClusterVersion": op.NewTyped[updateClusterVersionRequest, updateClusterVersionResponse](
			"UpdateClusterVersion", s.updateClusterVersionTyped,
		),
		"DescribeClusterVersions": op.NewTyped[describeClusterVersionsRequest, describeClusterVersionsResponse](
			"DescribeClusterVersions", s.describeClusterVersionsTyped,
		),
		"ListUpdates": op.NewTyped[listUpdatesRequest, listUpdatesResponse](
			"ListUpdates", s.listUpdatesTyped,
		),
		"DescribeUpdate": op.NewTyped[describeUpdateRequest, describeUpdateResponse](
			"DescribeUpdate", s.describeUpdateTyped,
		),
		"ListInsights": op.NewTyped[listInsightsRequest, listInsightsResponse](
			"ListInsights", s.listInsightsTyped,
		),
		"DescribeInsight": op.NewTyped[describeInsightRequest, describeInsightResponse](
			"DescribeInsight", s.describeInsightTyped,
		),
		"UpdateClusterConfig": op.NewTyped[updateClusterConfigRequest, updateClusterConfigResponse](
			"UpdateClusterConfig", s.updateClusterConfigTyped,
		),
		"UpdateKubeconfig": op.NewTyped[updateKubeconfigRequest, updateKubeconfigResponse](
			"UpdateKubeconfig", s.updateKubeconfigTyped,
		),
		"CreateNodegroup": op.NewTyped[createNodegroupRequest, createNodegroupResponse](
			"CreateNodegroup", s.createNodegroupTyped,
		),
		"ListNodegroups": op.NewTyped[listNodegroupsRequest, listNodegroupsResponse](
			"ListNodegroups", s.listNodegroupsTyped,
		),
		"DescribeNodegroup": op.NewTyped[describeNodegroupRequest, describeNodegroupResponse](
			"DescribeNodegroup", s.describeNodegroupTyped,
		),
		"DeleteNodegroup": op.NewTyped[deleteNodegroupRequest, deleteNodegroupResponse](
			"DeleteNodegroup", s.deleteNodegroupTyped,
		),
		"UpdateNodegroupVersion": op.NewTyped[updateNodegroupVersionRequest, updateNodegroupVersionResponse](
			"UpdateNodegroupVersion", s.updateNodegroupVersionTyped,
		),
		"UpdateNodegroupConfig": op.NewTyped[updateNodegroupConfigRequest, updateNodegroupConfigResponse](
			"UpdateNodegroupConfig", s.updateNodegroupConfigTyped,
		),
		"ListFargateProfiles": op.NewTyped[listFargateProfilesRequest, listFargateProfilesResponse](
			"ListFargateProfiles", s.listFargateProfilesTyped,
		),
		"DescribeFargateProfile": op.NewTyped[describeFargateProfileRequest, describeFargateProfileResponse](
			"DescribeFargateProfile", s.describeFargateProfileTyped,
		),
		"CreateFargateProfile": op.NewTyped[createFargateProfileRequest, createFargateProfileResponse](
			"CreateFargateProfile", s.createFargateProfileTyped,
		),
		"DeleteFargateProfile": op.NewTyped[deleteFargateProfileRequest, deleteFargateProfileResponse](
			"DeleteFargateProfile", s.deleteFargateProfileTyped,
		),
		"CreateAddon": op.NewTyped[createAddonRequest, createAddonResponse](
			"CreateAddon", s.createAddonTyped,
		),
		"ListAddons": op.NewTyped[listAddonsRequest, listAddonsResponse](
			"ListAddons", s.listAddonsTyped,
		),
		"DescribeAddon": op.NewTyped[describeAddonRequest, describeAddonResponse](
			"DescribeAddon", s.describeAddonTyped,
		),
		"DeleteAddon": op.NewTyped[deleteAddonRequest, deleteAddonResponse](
			"DeleteAddon", s.deleteAddonTyped,
		),
		"UpdateAddon": op.NewTyped[updateAddonRequest, updateAddonResponse](
			"UpdateAddon", s.updateAddonTyped,
		),
		"DescribeAddonVersions": op.NewTyped[describeAddonVersionsRequest, describeAddonVersionsResponse](
			"DescribeAddonVersions", s.describeAddonVersionsTyped,
		),
		"DescribeAddonConfiguration": op.NewTyped[describeAddonConfigurationRequest, describeAddonConfigurationResponse](
			"DescribeAddonConfiguration", s.describeAddonConfigurationTyped,
		),
		"ListAccessEntries": op.NewTyped[listAccessEntriesRequest, listAccessEntriesResponse](
			"ListAccessEntries", s.listAccessEntriesTyped,
		),
		"CreateAccessEntry": op.NewTyped[createAccessEntryRequest, createAccessEntryResponse](
			"CreateAccessEntry", s.createAccessEntryTyped,
		),
		"DescribeAccessEntry": op.NewTyped[describeAccessEntryRequest, describeAccessEntryResponse](
			"DescribeAccessEntry", s.describeAccessEntryTyped,
		),
		"UpdateAccessEntry": op.NewTyped[updateAccessEntryRequest, updateAccessEntryResponse](
			"UpdateAccessEntry", s.updateAccessEntryTyped,
		),
		"DeleteAccessEntry": op.NewTypedAny[deleteAccessEntryRequest](
			"DeleteAccessEntry", s.deleteAccessEntryTyped,
		),
		"AssociateAccessPolicy": op.NewTyped[associateAccessPolicyRequest, associateAccessPolicyResponse](
			"AssociateAccessPolicy", s.associateAccessPolicyTyped,
		),
		"ListAssociatedAccessPolicies": op.NewTyped[listAssociatedAccessPoliciesRequest, listAssociatedAccessPoliciesResponse](
			"ListAssociatedAccessPolicies", s.listAssociatedAccessPoliciesTyped,
		),
		"DisassociateAccessPolicy": op.NewTypedAny[disassociateAccessPolicyRequest](
			"DisassociateAccessPolicy", s.disassociateAccessPolicyTyped,
		),
		"ListAccessPolicies": op.NewTyped[listAccessPoliciesRequest, listAccessPoliciesResponse](
			"ListAccessPolicies", s.listAccessPoliciesTyped,
		),
		"DescribeAccessPolicy": op.NewTyped[describeAccessPolicyRequest, describeAccessPolicyResponse](
			"DescribeAccessPolicy", s.describeAccessPolicyTyped,
		),
		"ListIdentityProviderConfigs": op.NewTyped[listIdentityProviderConfigsRequest, listIdentityProviderConfigsResponse](
			"ListIdentityProviderConfigs", s.listIdentityProviderConfigsTyped,
		),
		"DescribeIdentityProviderConfig": op.NewTyped[describeIdentityProviderConfigRequest, describeIdentityProviderConfigResponse](
			"DescribeIdentityProviderConfig", s.describeIdentityProviderConfigTyped,
		),
		"UpdateIdentityProviderConfig": op.NewTyped[updateIdentityProviderConfigRequest, updateIdentityProviderConfigResponse](
			"UpdateIdentityProviderConfig", s.updateIdentityProviderConfigTyped,
		),
		"AssociateIdentityProviderConfig": op.NewTyped[associateIdentityProviderConfigRequest, associateIdentityProviderConfigResponse](
			"AssociateIdentityProviderConfig", s.associateIdentityProviderConfigTyped,
		),
		"DisassociateIdentityProviderConfig": op.NewTyped[disassociateIdentityProviderConfigRequest, disassociateIdentityProviderConfigResponse](
			"DisassociateIdentityProviderConfig", s.disassociateIdentityProviderConfigTyped,
		),
		"ListPodIdentityAssociations": op.NewTyped[listPodIdentityAssociationsRequest, listPodIdentityAssociationsResponse](
			"ListPodIdentityAssociations", s.listPodIdentityAssociationsTyped,
		),
		"CreatePodIdentityAssociation": op.NewTyped[createPodIdentityAssociationRequest, createPodIdentityAssociationResponse](
			"CreatePodIdentityAssociation", s.createPodIdentityAssociationTyped,
		),
		"DescribePodIdentityAssociation": op.NewTyped[describePodIdentityAssociationRequest, describePodIdentityAssociationResponse](
			"DescribePodIdentityAssociation", s.describePodIdentityAssociationTyped,
		),
		"UpdatePodIdentityAssociation": op.NewTyped[updatePodIdentityAssociationRequest, updatePodIdentityAssociationResponse](
			"UpdatePodIdentityAssociation", s.updatePodIdentityAssociationTyped,
		),
		"DeletePodIdentityAssociation": op.NewTyped[deletePodIdentityAssociationRequest, deletePodIdentityAssociationResponse](
			"DeletePodIdentityAssociation", s.deletePodIdentityAssociationTyped,
		),
		"TagResource": op.NewTypedAny[eksTagResourceRequest](
			"TagResource", s.eksTagResourceTyped,
		),
		"UntagResource": op.NewTypedAny[eksUntagResourceRequest](
			"UntagResource", s.eksUntagResourceTyped,
		),
		"ListTagsForResource": op.NewTyped[eksListTagsForResourceRequest, eksListTagsForResourceResponse](
			"ListTagsForResource", s.eksListTagsForResourceTyped,
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

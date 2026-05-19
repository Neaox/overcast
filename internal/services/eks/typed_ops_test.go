package eks

import (
	"testing"

	"github.com/Neaox/overcast/internal/protocol/op"
)

func TestTypedOps_matchDispatchSurface(t *testing.T) {
	s := &Service{}
	ops := s.typedOps()
	expected := []string{
		"CreateCluster",
		"DescribeCluster",
		"ListClusters",
		"DeleteCluster",
		"UpdateClusterVersion",
		"DescribeClusterVersions",
		"ListUpdates",
		"DescribeUpdate",
		"ListInsights",
		"DescribeInsight",
		"UpdateClusterConfig",
		"UpdateKubeconfig",
		"CreateNodegroup",
		"ListNodegroups",
		"DescribeNodegroup",
		"DeleteNodegroup",
		"UpdateNodegroupVersion",
		"UpdateNodegroupConfig",
		"ListFargateProfiles",
		"DescribeFargateProfile",
		"CreateFargateProfile",
		"DeleteFargateProfile",
		"CreateAddon",
		"ListAddons",
		"DescribeAddon",
		"DeleteAddon",
		"UpdateAddon",
		"DescribeAddonVersions",
		"DescribeAddonConfiguration",
		"ListAccessEntries",
		"CreateAccessEntry",
		"DescribeAccessEntry",
		"UpdateAccessEntry",
		"DeleteAccessEntry",
		"AssociateAccessPolicy",
		"ListAssociatedAccessPolicies",
		"DisassociateAccessPolicy",
		"ListAccessPolicies",
		"DescribeAccessPolicy",
		"ListIdentityProviderConfigs",
		"DescribeIdentityProviderConfig",
		"UpdateIdentityProviderConfig",
		"AssociateIdentityProviderConfig",
		"DisassociateIdentityProviderConfig",
		"ListPodIdentityAssociations",
		"CreatePodIdentityAssociation",
		"DescribePodIdentityAssociation",
		"UpdatePodIdentityAssociation",
		"DeletePodIdentityAssociation",
		"TagResource",
		"UntagResource",
		"ListTagsForResource",
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
			t.Fatalf("%s registered as raw operation", name)
		}
	}
}

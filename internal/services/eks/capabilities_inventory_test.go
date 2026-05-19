//go:build dev

package eks

import (
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/capabilities"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// TestCapabilities_MatchRouteInventory asserts that the capability
// declarations in capabilities_dev.go exactly cover the EKS REST routes
// registered by Service.RegisterRoutes. This prevents the generated docs and
// support inventory from drifting when a route is added or removed.
func TestCapabilities_MatchRouteInventory(t *testing.T) {
	t.Helper()

	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeMock},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	routeSet := make(map[string]struct{})
	err := chi.Walk(r, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		op, ok := eksOperationForRoute(method, route)
		if !ok {
			return fmt.Errorf("unmapped EKS route inventory entry: %s %s", method, route)
		}
		routeSet[op] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("walk EKS routes: %v", err)
	}

	caps := capabilities.Default.ForService("eks")
	capsSet := make(map[string]struct{}, len(caps))
	for _, c := range caps {
		capsSet[c.Operation] = struct{}{}
	}

	var inRoutesNotCaps []string
	for op := range routeSet {
		if _, ok := capsSet[op]; !ok {
			inRoutesNotCaps = append(inRoutesNotCaps, op)
		}
	}
	sort.Strings(inRoutesNotCaps)

	var inCapsNotRoutes []string
	for op := range capsSet {
		if _, ok := routeSet[op]; !ok {
			inCapsNotRoutes = append(inCapsNotRoutes, op)
		}
	}
	sort.Strings(inCapsNotRoutes)

	if len(inRoutesNotCaps) > 0 {
		t.Errorf("operations in EKS routes but not in capabilities_dev.go:\n  %v\nAdd a Capability entry for each.", inRoutesNotCaps)
	}
	if len(inCapsNotRoutes) > 0 {
		t.Errorf("operations in capabilities_dev.go but not in EKS routes:\n  %v\nRemove the stale Capability entry or add the missing route.", inCapsNotRoutes)
	}
}

func eksOperationForRoute(method, route string) (string, bool) {
	switch {
	case method == "POST" && route == "/clusters":
		return "CreateCluster", true
	case method == "GET" && route == "/clusters":
		return "ListClusters", true
	case method == "GET" && route == "/clusters/{name}":
		return "DescribeCluster", true
	case method == "POST" && route == "/clusters/{name}/access-entries":
		return "CreateAccessEntry", true
	case method == "GET" && route == "/clusters/{name}/access-entries":
		return "ListAccessEntries", true
	case method == "GET" && route == "/clusters/{name}/access-entries/{principalArn}":
		return "DescribeAccessEntry", true
	case method == "POST" && route == "/clusters/{name}/access-entries/{principalArn}":
		return "UpdateAccessEntry", true
	case method == "DELETE" && route == "/clusters/{name}/access-entries/{principalArn}":
		return "DeleteAccessEntry", true
	case method == "POST" && route == "/clusters/{name}/access-entries/{principalArn}/access-policies":
		return "AssociateAccessPolicy", true
	case method == "GET" && route == "/clusters/{name}/access-entries/{principalArn}/access-policies":
		return "ListAssociatedAccessPolicies", true
	case method == "DELETE" && route == "/clusters/{name}/access-entries/{principalArn}/access-policies/{policyArn}":
		return "DisassociateAccessPolicy", true
	case method == "GET" && route == "/access-policies":
		return "ListAccessPolicies", true
	case method == "GET" && route == "/access-policies/{name}":
		return "DescribeAccessPolicy", true
	case method == "GET" && route == "/cluster-versions":
		return "DescribeClusterVersions", true
	case method == "GET" && route == "/clusters/{name}/identity-provider-configs":
		return "ListIdentityProviderConfigs", true
	case method == "GET" && route == "/clusters/{name}/identity-provider-configs/{configType}/{configName}":
		return "DescribeIdentityProviderConfig", true
	case method == "POST" && route == "/clusters/{name}/identity-provider-configs/{configType}/{configName}/update":
		return "UpdateIdentityProviderConfig", true
	case method == "POST" && route == "/clusters/{name}/identity-provider-configs/associate":
		return "AssociateIdentityProviderConfig", true
	case method == "POST" && route == "/clusters/{name}/identity-provider-configs/disassociate":
		return "DisassociateIdentityProviderConfig", true
	case method == "POST" && route == "/clusters/{name}/pod-identity-associations":
		return "CreatePodIdentityAssociation", true
	case method == "GET" && route == "/clusters/{name}/pod-identity-associations":
		return "ListPodIdentityAssociations", true
	case method == "GET" && route == "/clusters/{name}/pod-identity-associations/{associationId}":
		return "DescribePodIdentityAssociation", true
	case method == "POST" && route == "/clusters/{name}/pod-identity-associations/{associationId}":
		return "UpdatePodIdentityAssociation", true
	case method == "DELETE" && route == "/clusters/{name}/pod-identity-associations/{associationId}":
		return "DeletePodIdentityAssociation", true
	case method == "GET" && route == "/clusters/{name}/updates":
		return "ListUpdates", true
	case method == "POST" && route == "/clusters/{name}/updates":
		return "UpdateClusterVersion", true
	case method == "GET" && route == "/clusters/{name}/insights":
		return "ListInsights", true
	case method == "GET" && route == "/clusters/{name}/insights/{insightId}":
		return "DescribeInsight", true
	case method == "POST" && route == "/clusters/{name}/update-config":
		return "UpdateClusterConfig", true
	case method == "GET" && route == "/clusters/{name}/updates/{updateId}":
		return "DescribeUpdate", true
	case method == "POST" && route == "/clusters/{name}/kubeconfig":
		return "UpdateKubeconfig", true
	case method == "DELETE" && route == "/clusters/{name}":
		return "DeleteCluster", true
	case method == "POST" && route == "/clusters/{name}/node-groups":
		return "CreateNodegroup", true
	case method == "POST" && route == "/clusters/{name}/node-groups/{nodegroupName}/updates":
		return "UpdateNodegroupVersion", true
	case method == "POST" && route == "/clusters/{name}/node-groups/{nodegroupName}/update-config":
		return "UpdateNodegroupConfig", true
	case method == "GET" && route == "/clusters/{name}/node-groups":
		return "ListNodegroups", true
	case method == "GET" && route == "/clusters/{name}/node-groups/{nodegroupName}":
		return "DescribeNodegroup", true
	case method == "DELETE" && route == "/clusters/{name}/node-groups/{nodegroupName}":
		return "DeleteNodegroup", true
	case method == "GET" && route == "/clusters/{name}/fargate-profiles":
		return "ListFargateProfiles", true
	case method == "GET" && route == "/clusters/{name}/fargate-profiles/{fargateProfileName}":
		return "DescribeFargateProfile", true
	case method == "POST" && route == "/clusters/{name}/fargate-profiles":
		return "CreateFargateProfile", true
	case method == "DELETE" && route == "/clusters/{name}/fargate-profiles/{fargateProfileName}":
		return "DeleteFargateProfile", true
	case method == "GET" && route == "/tags/{resourceArn}":
		return "ListTagsForResource", true
	case method == "POST" && route == "/tags/{resourceArn}":
		return "TagResource", true
	case method == "DELETE" && route == "/tags/{resourceArn}":
		return "UntagResource", true
	case method == "POST" && route == "/clusters/{name}/addons":
		return "CreateAddon", true
	case method == "GET" && route == "/clusters/{name}/addons":
		return "ListAddons", true
	case method == "GET" && route == "/clusters/{name}/addons/{addonName}":
		return "DescribeAddon", true
	case method == "POST" && route == "/clusters/{name}/addons/{addonName}/updates":
		return "UpdateAddon", true
	case method == "DELETE" && route == "/clusters/{name}/addons/{addonName}":
		return "DeleteAddon", true
	case method == "GET" && route == "/addons/{addonName}/versions":
		return "DescribeAddonVersions", true
	case method == "GET" && route == "/addons/{addonName}/configuration":
		return "DescribeAddonConfiguration", true
	default:
		return "", false
	}
}

//go:build !nosqlite

package state

import "testing"

func TestLambdaFunctionPoliciesNamespace_isRegisteredHot(t *testing.T) {
	const namespace = "lambda:function-policies"

	// Given: Lambda resource policies are control-plane state that must be
	// restored into HybridStore's in-memory tier at startup.
	tier, registered := namespaceTiers[namespace]

	// Then: the namespace is explicitly registered as hot. Checking map
	// membership matters because TierFor intentionally defaults unknown
	// namespaces to TierHot even though unknown namespaces are not seeded.
	if !registered {
		t.Fatalf("namespace %q is not registered", namespace)
	}
	if tier != TierHot {
		t.Fatalf("namespace %q tier = %v, want TierHot", namespace, tier)
	}
	if _, seeded := hybridNamespaceSet(hybridHotNamespaces())[namespace]; !seeded {
		t.Fatalf("namespace %q is not included in HybridStore startup restore", namespace)
	}
}

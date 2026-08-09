//go:build !nosqlite

package state

import "testing"

func TestLambdaFunctionPoliciesNamespace_isRegisteredHot(t *testing.T) {
	const namespace = "lambda:function-policies"

	// Given: Lambda resource policies are control-plane state that must be
	// restored into HybridStore's in-memory tier at startup.
	tier, registered := namespaceTiers[namespace]

	// Then: the namespace is explicitly registered as hot. Checking map
	// membership matters because only a registered TierHot namespace reaches
	// the hybrid seed list — an unregistered one is served lazily from SQLite
	// however it was meant to be classified.
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

func TestLambdaFunctionCodeNamespace_isRegisteredCached(t *testing.T) {
	const namespace = "lambda:function-code"

	// Given: deployment packages are data-plane bulk — the largest values
	// Lambda stores, by orders of magnitude, and read only on a cold start.
	tier, registered := namespaceTiers[namespace]

	// Then: the namespace carries an explicit classification. Membership is the
	// point rather than the tier alone: an unregistered namespace is left out
	// of the hybrid seed list silently, so which tier it ends up in would be
	// settled by omission rather than by a decision anyone reviewed.
	if !registered {
		t.Fatalf("namespace %q is not registered", namespace)
	}
	if tier != TierCached {
		t.Fatalf("namespace %q tier = %v, want TierCached", namespace, tier)
	}
	if got := TierFor(namespace); got != TierCached {
		t.Fatalf("TierFor(%q) = %v, want TierCached", namespace, got)
	}
	if _, seeded := hybridNamespaceSet(hybridHotNamespaces())[namespace]; seeded {
		t.Fatalf("namespace %q is seeded into memory at startup; deployment packages must stay SQLite-backed", namespace)
	}
}

// TestTierFor_unregisteredNamespaceIsCached pins TierFor's unknown-namespace
// answer to what HybridStore actually does with such a namespace. The seed list
// is built from this table (hybridHotNamespaces), so a namespace missing from
// it is never seeded and every read goes to SQLite through the pending overlay
// — TierCached behaviour in everything but name. TierFor previously answered
// TierHot for those, which is the reading that made "lambda:function-code is
// untiered" look like "every deployment package is resident in memory".
func TestTierFor_unregisteredNamespaceIsCached(t *testing.T) {
	// Given: a namespace nobody has classified.
	const namespace = "unregistered:namespace"
	if _, registered := namespaceTiers[namespace]; registered {
		t.Fatalf("fixture namespace %q is registered; pick one that is not", namespace)
	}

	// When/Then: TierFor reports the tier it will actually be served at.
	if got := TierFor(namespace); got != TierCached {
		t.Fatalf("TierFor(%q) = %v, want TierCached", namespace, got)
	}
	if !shouldReadHybridNamespaceFromSQLite(namespace) {
		t.Fatalf("namespace %q is not read from SQLite, so TierCached is the wrong answer for it", namespace)
	}
}

func TestLambdaEventSourceMappingTagsNamespace_isRegisteredHot(t *testing.T) {
	const namespace = "lambda:esm-tags"

	// Given: event source mapping tags are control-plane state alongside the
	// mapping itself, which is registered hot.
	tier, registered := namespaceTiers[namespace]

	// Then: the namespace is registered, so HybridStore restores it at startup.
	// An unregistered namespace is never seeded — its tags stay readable
	// through the lazy SQLite path, but every read of them is a round trip
	// rather than the memory hit a control-plane namespace is meant to get.
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

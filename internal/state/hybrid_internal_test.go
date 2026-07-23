//go:build !nosqlite

package state

import (
	"fmt"
	"testing"
)

func TestHybridSQLiteTransient_includesInterrupted(t *testing.T) {
	// Given: the SQLite error surfaced by background scheduler scans under load.
	err := fmt.Errorf("sqlite scan [scheduler:schedules/*]: interrupted (9)")

	// When/Then: interrupted reads are classified with other transient SQLite
	// contention errors so hybrid retries them instead of surfacing InternalError.
	if !isSQLiteTransient(err) {
		t.Fatal("interrupted SQLite error should be retryable")
	}
}

func TestHybridHotNamespaces_followNamespaceTiers(t *testing.T) {
	seeded := hybridNamespaceSet(hybridHotNamespaces())

	for namespace, tier := range namespaceTiers {
		_, ok := seeded[namespace]
		if tier == TierHot && !ok {
			t.Fatalf("TierHot namespace %q missing from hybrid seed list", namespace)
		}
		if tier == TierCached && ok {
			t.Fatalf("TierCached namespace %q should stay lazy", namespace)
		}
	}
}

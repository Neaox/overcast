//go:build slim

package docssearch

import "testing"

func TestSearch_slimBuild(t *testing.T) {
	// Given: slim builds exclude the generated docs search index.

	// When: we search.
	results := Search("cdk", 5)

	// Then: the fallback index returns no results without failing.
	if len(results) != 0 {
		t.Fatalf("expected no slim search results, got %d", len(results))
	}
}

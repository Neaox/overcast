//go:build !slim

package docssearch

import "testing"

func TestSearch_cdkLocalVpc(t *testing.T) {
	// Given: the generated docs index is available.

	// When: we search for the local VPC CDK workflow.
	results := Search("cdk local vpc provider", 5)

	// Then: the focused local VPC guide is ranked first.
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Href != "cdk/local-vpc.md" {
		t.Fatalf("expected local VPC guide first, got %q", results[0].Href)
	}
}

func TestSearch_ignoresStopwords(t *testing.T) {
	// Given: a query containing only stopwords.

	// When: we search.
	results := Search("the and of", 5)

	// Then: no documents are returned.
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

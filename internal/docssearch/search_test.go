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

// TestSearch_servicePageOutranksTheOperationManifest guards a corpus-wide
// consequence of weighting by field. docs/operation-manifest.md is a generated
// listing of every modelled AWS operation, so it mentions every service name;
// summed occurrence counts let that listing beat the page that actually
// documents the service. Its mirror is in web/api/src/routes/docs.test.ts.
func TestSearch_servicePageOutranksTheOperationManifest(t *testing.T) {
	// Given: queries naming a service that the manifest also lists.
	for query, want := range map[string]string{
		"msk cluster":         "services/msk.md",
		"opensearch domain":   "services/opensearch.md",
		"autoscaling group":   "services/autoscaling.md",
		"log group retention": "services/cloudwatch-logs.md",
	} {
		// When: each is searched for.
		results := Search(query, 5)

		// Then: the service's own page ranks above the generated listing.
		if len(results) == 0 {
			t.Errorf("%q: expected search results", query)
			continue
		}
		if results[0].Href != want {
			t.Errorf("%q: ranked %q first, want %q", query, results[0].Href, want)
		}
	}
}

func TestSearch_camelCaseOperationWords(t *testing.T) {
	// Given: the generated docs index includes operation names.

	// When: we search for words from a CamelCase operation.
	results := Search("live tail", 5)

	// Then: the CloudWatch Logs StartLiveTail documentation is discoverable.
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Href != "services/cloudwatch-logs.md" {
		t.Fatalf("expected CloudWatch Logs first, got %q", results[0].Href)
	}
}

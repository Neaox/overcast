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
// documents the service. (This used to have a TypeScript mirror in the vite
// dev server's Hono BFF; that port was retired in #1104, so this is the only
// copy of the guard.)
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

// TestSearch_camelCaseOperationWords checks that an operation is findable by
// the English words inside its CamelCase name, which is what tokenize's
// identifier splitting exists for.
//
// What is asserted is that the operations page is reachable by those words at
// all, not that it ranks first: whether the landing page also matches is a
// property of that page's prose, which is rewritten from time to time, and
// promoteServiceLandingPages puts the landing above its own sub-page whenever
// both are in the results. Pinning first place would make this test a fixture
// check on one paragraph of CloudWatch Logs documentation rather than a check
// on tokenize.
func TestSearch_camelCaseOperationWords(t *testing.T) {
	// Given: the generated docs index includes operation names.

	// When: we search for words from a CamelCase operation.
	results := Search("live tail", 5)

	// Then: the CloudWatch Logs StartLiveTail documentation is discoverable.
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	found := false
	for _, r := range results {
		if r.Href == "services/cloudwatch-logs/operations.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected the CloudWatch Logs operations page in the results, got %v", hrefs(results))
	}
}

func hrefs(results []Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Href
	}
	return out
}

// TestSearch_serviceLandingPageOutranksItsOwnOperationsTable is the guard
// above, for the generated listing that now sits directly beneath each service
// page rather than one shared manifest elsewhere in the tree.
func TestSearch_serviceLandingPageOutranksItsOwnOperationsTable(t *testing.T) {
	// Given: a query both the landing page and its operations table match.
	// When: it is searched for.
	results := Search("log group retention", 5)

	// Then: the page that explains the behaviour comes first, and the table is
	// still in the results behind it.
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Href != "services/cloudwatch-logs.md" {
		t.Fatalf("ranked %q first, want services/cloudwatch-logs.md", results[0].Href)
	}
	found := false
	for _, r := range results {
		if r.Href == "services/cloudwatch-logs/operations.md" {
			found = true
		}
	}
	if !found {
		t.Error("the operations table should still be in the results, just not first")
	}
}

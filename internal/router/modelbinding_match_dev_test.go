//go:build dev

package router

import "testing"

// These tests fix the rules TestModeledBindings_areServedWhereAWSBindsThem
// relies on. Each case is a shape that appears in this tree, named by the
// service that produces it, because every one of them was found by the gate
// reporting a result that was wrong in an interesting way.
func TestPatternCovers(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		uri     string
		want    coverage
	}{{
		name:    "identical literals",
		pattern: "/v2/email/identities",
		uri:     "/v2/email/identities",
		want:    coverageExact,
	}, {
		name:    "label names differ but bind the same position",
		pattern: "/schedules/{scheduleName}",
		uri:     "/schedules/{Name}",
		want:    coverageExact,
	}, {
		name:    "literal where the model binds a parameter serves one value of it",
		pattern: "/schedules/default",
		uri:     "/schedules/{Name}",
		want:    coverageNone,
	}, {
		name:    "label where the model has a literal claims more than the model binds",
		pattern: "/applications/{id}",
		uri:     "/applications/list",
		want:    coverageBroad,
	}, {
		name:    "extra path segment",
		pattern: "/v2/clusters/{arn}/nodes",
		uri:     "/v2/clusters/{arn}",
		want:    coverageNone,
	}, {
		name:    "wildcard standing in for the model's last segment",
		pattern: "/tags/*",
		uri:     "/tags/{resourceArn}",
		want:    coverageExact,
	}, {
		name:    "wildcard standing in for a greedy model label",
		pattern: "/{bucket}/*",
		uri:     "/{Bucket}/{Key+}",
		want:    coverageExact,
	}, {
		name:    "wildcard over further modeled structure leaves routing to the handler",
		pattern: "/v1/clusters/*",
		uri:     "/v1/clusters/{ClusterArn}/bootstrap-brokers",
		want:    coverageBroad,
	}, {
		name:    "greedy model label needs a wildcard, not a single label",
		pattern: "/tags/{resourceArn}",
		uri:     "/tags/{ResourceArn+}",
		want:    coverageNone,
	}, {
		// Without this rule SQS's queue-URL route matches the first segment of
		// every modeled URI in the corpus, and the gate reports the entire
		// emulator as served.
		name:    "constrained label rejects a literal outside its expression",
		pattern: "/{accountID:[0-9]+}/{queueName}",
		uri:     "/2021-01-01/tags",
		want:    coverageNone,
	}, {
		name:    "constrained label accepts a literal inside its expression",
		pattern: "/{accountID:[0-9]+}/{queueName}",
		uri:     "/12345/{name}",
		want:    coverageBroad,
	}, {
		name:    "constrained label cannot cover a parameter it may not admit",
		pattern: "/{accountID:[0-9]+}/{queueName}",
		uri:     "/{AccountId}/{QueueName}",
		want:    coverageNone,
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given: a registered chi pattern and a modeled URI template.

			// When: the gate grades the pattern against the template.
			got := patternCovers(patternSegments(test.pattern), uriSegments(test.uri))

			// Then: the grade matches what the router would really do.
			if got != test.want {
				t.Errorf("patternCovers(%q, %q) = %v, want %v", test.pattern, test.uri, got, test.want)
			}
		})
	}
}

func TestURISegments_dropsThePinnedQueryString(t *testing.T) {
	// Given: one of the 139 modeled URIs that pin a query parameter. chi routes
	// on the path alone, so the handler branches on the parameter and the gate
	// can only prove the path.

	// When: the template is split for matching.
	got := uriSegments("/2020-05-31/distribution?WithTags")

	// Then: the query is not treated as part of the path.
	want := []string{"", "2020-05-31", "distribution"}
	if len(got) != len(want) {
		t.Fatalf("uriSegments() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("uriSegments() = %q, want %q", got, want)
		}
	}
}

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegionFromHost(t *testing.T) {
	cases := []struct {
		host, want string
	}{
		{"d038ecd84a.execute-api.ap-southeast-2.amazonaws.com", "ap-southeast-2"},
		{"d038ecd84a.execute-api.us-east-1.localhost.localstack.cloud:4566", "us-east-1"},
		{"d038ecd84a.execute-api.eu-west-1.localhost:4566", "eu-west-1"},
		{"localhost:4566", ""},
		{"", ""},
		{"foo.bar", ""},
		{"d038ecd84a.unknown-svc.ap-southeast-2.amazonaws.com", ""},

		// A hostname is case-insensitive, so neither the service label nor the
		// region segment may be matched or returned case-sensitively. This one
		// bites harder than a routing miss: the region returned here becomes
		// the store's key prefix (serviceutil.RegionKey), so an upper-case
		// region silently partitions state — resources created under
		// "US-East-1" are invisible to every request that says "us-east-1".
		{"d038ecd84a.EXECUTE-API.ap-southeast-2.amazonaws.com", "ap-southeast-2"},
		{"d038ecd84a.execute-api.AP-SOUTHEAST-2.amazonaws.com", "ap-southeast-2"},
		{"D038ECD84A.Execute-Api.EU-West-1.localhost:4566", "eu-west-1"},
		{"MyBucket.S3.US-East-1.amazonaws.com", "us-east-1"},
	}
	for _, tc := range cases {
		if got := regionFromHost(tc.host); got != tc.want {
			t.Errorf("regionFromHost(%q) = %q; want %q", tc.host, got, tc.want)
		}
	}
}

// TestRegionFromCredential_isCanonicalisedToLowercase covers the other way a
// region reaches the store's key prefix. AWS region identifiers are canonically
// lowercase and no SDK emits anything else, but the region parsed here is not a
// hint — serviceutil.RegionKey makes it the state partition — so an unfolded one
// hides every resource from every other request.
//
// The codebase already settled this convention on the IAM side:
// requestedRegionFromSigV4 (iam_enforce.go) lowercases the same scope segment.
// Leaving this one verbatim meant a single request could evaluate its IAM
// conditions against "us-east-1" while writing state under "US-East-1".
//
// Folding here cannot affect authentication: signature verification builds its
// own scope string straight from the Authorization header (parseSigV4), so a
// caller that signed an upper-case scope still verifies against exactly what it
// sent — it just stops partitioning state into a region nothing else can name.
func TestRegionFromCredential_isCanonicalisedToLowercase(t *testing.T) {
	cases := []struct {
		name, auth, want string
	}{
		{
			name: "lowercase scope is unchanged",
			auth: "AWS4-HMAC-SHA256 Credential=AKIA/20260729/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc",
			want: "us-east-1",
		},
		{
			name: "uppercase region is folded",
			auth: "AWS4-HMAC-SHA256 Credential=AKIA/20260729/US-EAST-1/s3/aws4_request, SignedHeaders=host, Signature=abc",
			want: "us-east-1",
		},
		{
			name: "mixed case region is folded",
			auth: "AWS4-HMAC-SHA256 Credential=AKIA/20260729/Ap-SouthEast-2/sqs/aws4_request, SignedHeaders=host, Signature=abc",
			want: "ap-southeast-2",
		},
		{
			name: "no authorization header",
			auth: "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, "http://localhost:4566/", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			if got := regionFromCredential(r); got != tc.want {
				t.Errorf("regionFromCredential() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRegion_middlewareAgreesWithIAMOnScopeCase is the cross-check that matters
// more than either function alone: the region the store partitions by and the
// region IAM evaluates against must be the same string for one request.
func TestRegion_middlewareAgreesWithIAMOnScopeCase(t *testing.T) {
	for _, scope := range []string{"us-east-1", "US-EAST-1", "Eu-West-1"} {
		t.Run(scope, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, "http://localhost:4566/", nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Authorization",
				"AWS4-HMAC-SHA256 Credential=AKIA/20260729/"+scope+"/s3/aws4_request, SignedHeaders=host, Signature=abc")

			store, iam := regionFromCredential(r), requestedRegionFromSigV4(r)

			if store != iam {
				t.Errorf("scope %q: store partitions by %q but IAM evaluates %q", scope, store, iam)
			}
		})
	}
}

// TestRegion_contextRegionIsAlwaysCanonical states the invariant the store
// depends on, at the level that actually guarantees it. The individual
// extractors each fold, but what downstream code keys state on is whatever the
// Region middleware stamped — so assert it there, across every source in the
// documented resolution order, rather than trusting three separate functions to
// have remembered.
func TestRegion_contextRegionIsAlwaysCanonical(t *testing.T) {
	cases := []struct {
		name, header, auth, host, want string
	}{
		{name: "internal override header", header: "US-East-1", host: "localhost:4566", want: "us-east-1"},
		{name: "sigv4 credential scope", auth: "AWS4-HMAC-SHA256 Credential=AKIA/20260729/EU-WEST-1/s3/aws4_request, SignedHeaders=host, Signature=abc", host: "localhost:4566", want: "eu-west-1"},
		{name: "host subdomain", host: "abc123.execute-api.AP-SOUTHEAST-2.amazonaws.com", want: "ap-southeast-2"},
		{name: "already canonical", host: "abc123.execute-api.us-east-1.amazonaws.com", want: "us-east-1"},
		{name: "no region anywhere", host: "localhost:4566", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			h := Region(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = RegionFromContext(r.Context(), "")
			}))

			r, err := http.NewRequest(http.MethodGet, "http://localhost:4566/", nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Host = tc.host
			if tc.header != "" {
				r.Header.Set("X-Overcast-Region", tc.header)
			}
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)

			if got != tc.want {
				t.Errorf("region in context = %q, want %q", got, tc.want)
			}
		})
	}
}

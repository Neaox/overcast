package middleware

import (
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
)

// hostbase_matrix_test.go — every addressing form against every recognised
// base, as one grid.
//
// The point is that a gap shows up as a wrong cell rather than as a silent
// "this host doesn't work". Two real gaps were found exactly this way:
// localhost.floci.io was missing from the S3 bare-form bases while
// internal/containerendpoint already advertised it, and
// appsync-realtime-api — the hostname AWS actually serves subscriptions on,
// and the one Amplify derives by substituting into the GraphQL URL — was
// unregistered, so the bare form claimed it as an S3 bucket.
//
// See docs/plans/host-routing-precedence.md.

type claimShape string

const (
	shapeS3        claimShape = "s3"
	shapeHostRoute claimShape = "svc"
	shapeNone      claimShape = "none"
)

func shapeOf(c HostClaim) claimShape {
	switch c.Kind {
	case HostClaimS3:
		return shapeS3
	case HostClaimHostRoute:
		return shapeHostRoute
	case HostClaimNone:
		return shapeNone
	}
	return shapeNone
}

func TestHostClassifier_everyFormOnEveryBase(t *testing.T) {
	// Given: the bases a client can actually reach Overcast on, plus
	// amazonaws.com (reachable only with a DNS override, but the shape real
	// SDKs emit).
	bases := []string{
		"localhost",
		"localhost.overcast.sh",
		"localhost.localstack.cloud",
		"localhost.floci.io",
		"amazonaws.com",
	}

	// Each form, with the claim expected on a wildcard-DNS base and on
	// amazonaws.com. They differ only for the bare S3 form, which has no
	// real-AWS equivalent: "bucket.amazonaws.com" is not an S3 address.
	forms := []struct {
		name        string
		prefix      string
		wantLocal   claimShape
		wantAmazon  claimShape
		wantService string // host-route label, when wantLocal is shapeHostRoute
	}{
		{name: "S3 bare", prefix: "mybucket.", wantLocal: shapeS3, wantAmazon: shapeNone},
		{name: "S3 labelled", prefix: "mybucket.s3.us-east-1.", wantLocal: shapeS3, wantAmazon: shapeS3},
		{name: "S3 labelled no region", prefix: "mybucket.s3.", wantLocal: shapeS3, wantAmazon: shapeS3},
		{name: "API Gateway", prefix: "abc123.execute-api.us-east-1.", wantLocal: shapeHostRoute, wantAmazon: shapeHostRoute, wantService: LabelExecuteAPI},
		{name: "Lambda function URL", prefix: "urlid.lambda-url.us-east-1.", wantLocal: shapeHostRoute, wantAmazon: shapeHostRoute, wantService: LabelLambdaURL},
		{name: "AppSync GraphQL", prefix: "myapi.appsync-api.us-east-1.", wantLocal: shapeHostRoute, wantAmazon: shapeHostRoute, wantService: LabelAppSyncAPI},
		{name: "AppSync realtime", prefix: "myapi.appsync-realtime-api.us-east-1.", wantLocal: shapeHostRoute, wantAmazon: shapeHostRoute, wantService: LabelAppSyncRealtimeAPI},
	}

	c := NewHostClassifier("")
	for _, f := range forms {
		for _, base := range bases {
			t.Run(f.name+" on "+base, func(t *testing.T) {
				want := f.wantLocal
				if base == "amazonaws.com" {
					want = f.wantAmazon
				}

				// When: the host is classified
				claim := c.Classify(f.prefix + base + ":4566")

				// Then: it lands on the expected owner
				if got := shapeOf(claim); got != want {
					t.Fatalf("Classify(%q) = %s, want %s", f.prefix+base, got, want)
				}
				if want == shapeHostRoute && claim.Route.Label != f.wantService {
					t.Errorf("Classify(%q) label = %q, want %q", f.prefix+base, claim.Route.Label, f.wantService)
				}
			})
		}
	}
}

// TestVirtualHostBases_coverEveryWildcardDomain is the DRY guard. The S3
// bare-form base list and internal/containerendpoint's split-horizon list were
// maintained separately and drifted: containerendpoint knew
// localhost.floci.io, the S3 matcher did not, so a bucket was unreachable on a
// domain Overcast advertises. Both now derive from config.WildcardDNSDomains.
func TestVirtualHostBases_coverEveryWildcardDomain(t *testing.T) {
	// Given: the canonical wildcard-DNS domains
	for _, domain := range config.WildcardDNSDomains {
		t.Run(domain, func(t *testing.T) {
			// When: a bucket is addressed bare on that domain
			claim := NewHostClassifier("").Classify("mybucket." + domain + ":4566")

			// Then: it resolves to the bucket. A domain Overcast tells users
			// resolves to it must work for every addressing form.
			if claim.Kind != HostClaimS3 || claim.Bucket != "mybucket" {
				t.Errorf("Classify(mybucket.%s) = %s, want s3:mybucket — %s is in "+
					"config.WildcardDNSDomains but not recognised for virtual-hosted addressing",
					domain, shapeOf(claim), domain)
			}
		})
	}
}

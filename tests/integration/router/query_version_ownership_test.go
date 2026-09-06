// Tests for #1884: an AWS Query request must be answered by the service its
// API version names, not by whichever registered service happens to spell the
// same Action.
//
// queryOwner resolves a Query request in two passes: API version first, then
// action name. The second pass matches on the name alone, and AWS reuses names
// freely across services — Elastic Load Balancing Classic (2012-06-01) and
// ELBv2 (2015-12-01) share the entire vocabulary a load balancer needs. Only
// v2 is implemented, so every Classic call fell into the action pass and was
// answered by ELBv2's handler: `DescribeTags` came back 200 with an ELBv2
// document under the 2015-12-01 XML namespace, and `CreateLoadBalancer` came
// back 400 complaining about `Name`, which is ELBv2's member — Classic spells
// it `LoadBalancerName`.
//
// Both answers are worse than no answer. A Classic client is entitled to the
// 501 an unimplemented service owes it, which is what the generated registry
// writes once the action pass stops claiming the request.
//
// Run: go test ./tests/integration/router/...
package router_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// elbSigV4 is the Authorization header both ELB SDKs send. The signing name is
// shared by Classic and v2, which is why it cannot be the discriminator.
const elbSigV4 = "AWS4-HMAC-SHA256 Credential=test/20260906/us-east-1/elasticloadbalancing" +
	"/aws4_request, SignedHeaders=host, Signature=test"

// postQuery sends one AWS Query request and returns its status and body.
func postQuery(t *testing.T, baseURL, version, action string) (int, string) {
	t.Helper()
	body := url.Values{"Action": {action}, "Version": {version}}.Encode()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", elbSigV4)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", version, action, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", action, err)
	}
	return resp.StatusCode, string(payload)
}

// TestELBClassicIsNotAnsweredByELBv2 is the regression this file exists for.
func TestELBClassicIsNotAnsweredByELBv2(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Given: the Classic operations whose names ELBv2 also declares, plus two
	// that only Classic declares. The "was" column is the answer on the commit
	// this fixes.
	for _, tc := range []struct {
		action string
		was    string
	}{
		{"DescribeTags", "200 from ELBv2's handler"},
		{"DescribeLoadBalancers", "200 from ELBv2's handler"},
		{"CreateLoadBalancer", "400 ValidationError: Name is required (ELBv2's member)"},
		{"DescribeLoadBalancerAttributes", "400 ValidationError: LoadBalancerArn is required (Classic takes a name)"},
		{"DescribeAccountLimits", "501"},
		{"DescribeLoadBalancerPolicyTypes", "501"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			// When: a Classic client calls it at Classic's API version.
			status, body := postQuery(t, srv.URL, "2012-06-01", tc.action)

			// Then: Overcast says it does not emulate ELB Classic, in Classic's
			// own Query XML envelope.
			if status != http.StatusNotImplemented {
				t.Fatalf("ELB Classic %s = %d, want 501 (was %s)\n%s", tc.action, status, tc.was, body)
			}
			if !strings.Contains(body, "NotImplemented") {
				t.Errorf("ELB Classic %s body is not a NotImplemented envelope:\n%s", tc.action, body)
			}
			// A 200 shaped by ELBv2 is the specific failure; a namespace from
			// the wrong API version is how it announced itself.
			if strings.Contains(body, "2015-12-01") {
				t.Errorf("ELB Classic %s answered in ELBv2's XML namespace:\n%s", tc.action, body)
			}
		})
	}
}

// TestELBv2StillAnswersItsOwnQueryVersion is the control. The fix withdraws a
// claim the action pass should never have made; it must not withdraw the one
// ELBv2 is entitled to, and ELBv2 declares the same action names.
func TestELBv2StillAnswersItsOwnQueryVersion(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for _, action := range []string{"DescribeTags", "DescribeLoadBalancers", "DescribeTargetGroups"} {
		t.Run(action, func(t *testing.T) {
			status, body := postQuery(t, srv.URL, "2015-12-01", action)
			if status != http.StatusOK {
				t.Fatalf("ELBv2 %s = %d, want 200\n%s", action, status, body)
			}
			if !strings.Contains(body, "2015-12-01") {
				t.Errorf("ELBv2 %s did not answer in its own XML namespace:\n%s", action, body)
			}
		})
	}

	// A request with no Version at all still reaches ELBv2 by action name.
	// That is the fallback the version guard deliberately leaves in place: with
	// nothing to attribute, there is nothing to withdraw.
	t.Run("no version falls back to the action name", func(t *testing.T) {
		body := url.Values{"Action": {"DescribeLoadBalancers"}}.Encode()
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(body))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", elbSigV4)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("versionless DescribeLoadBalancers: %v", err)
		}
		defer resp.Body.Close()
		payload, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("versionless DescribeLoadBalancers = %d, want 200\n%s", resp.StatusCode, payload)
		}
	})
}

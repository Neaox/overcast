package elbv2

// describe_identifiers_test.go — the not-found codes now come from one place
// per resource (identifierScope), so the operations that resolve a single ARN
// answer the same thing a Describe* does.
//
// The wire-level cases for the Describe* lists themselves are in
// tests/integration/elbv2/describe_identifiers_test.go; these cover what only
// the package can see: which code each single-ARN operation reaches for.
//
// Shares attrHandler, elbv2Call and xmlValue with loadbalancer_attributes_test.go.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// DeleteListener answered LoadBalancerNotFound for a listener that was not
// there — errNotFound took the resource name as prose and hardcoded the code,
// so the only listener error ELBv2 could produce named the wrong resource. A
// caller catching ListenerNotFound around a delete never saw it.
func TestDeleteListener_unknownARNIsListenerNotFound(t *testing.T) {
	h := attrHandler(t)
	missing := "arn:aws:elasticloadbalancing:us-east-1:000000000000:listener/app/nope/0123456789abcdef/fedcba9876543210"

	rec := elbv2Call(t, h.DeleteListener, url.Values{"ListenerArn": {missing}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DeleteListener on an unknown ARN = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if got := xmlValue(t, rec.Body.String(), "Code"); got != "ListenerNotFound" {
		t.Errorf("error code = %q, want ListenerNotFound", got)
	}
}

// AddTags, RemoveTags and DescribeTags take load balancer and target group ARNs
// in one ResourceArns, and AWS answers each with its own resource's code.
func TestTagOperations_unknownARNNamesItsOwnResource(t *testing.T) {
	h := attrHandler(t)
	cases := []struct {
		arn  string
		want string
	}{
		{"arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/nope/0123456789abcdef", "TargetGroupNotFound"},
		{"arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/nope/0123456789abcdef", "LoadBalancerNotFound"},
	}

	for _, tc := range cases {
		rec := elbv2Call(t, h.DescribeTags, url.Values{"ResourceArns.member.1": {tc.arn}})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("DescribeTags on %s = %d, want 400: %s", tc.arn, rec.Code, rec.Body.String())
		}
		if got := xmlValue(t, rec.Body.String(), "Code"); got != tc.want {
			t.Errorf("DescribeTags on %s: error code = %q, want %q", tc.arn, got, tc.want)
		}
	}
}

// A single-ARN operation applies the shape check the Describe* lists do, so a
// value that is not an ARN for the resource fails as a bad request rather than
// reading as "it has been deleted".
func TestSingleARNOperations_malformedARNIsValidationError(t *testing.T) {
	h := attrHandler(t)
	cases := []struct {
		name  string
		fn    http.HandlerFunc
		param string
	}{
		{"DeleteLoadBalancer", h.DeleteLoadBalancer, "LoadBalancerArn"},
		{"DeleteTargetGroup", h.DeleteTargetGroup, "TargetGroupArn"},
		{"DeleteListener", h.DeleteListener, "ListenerArn"},
	}

	for _, tc := range cases {
		rec := elbv2Call(t, tc.fn, url.Values{tc.param: {"web"}})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s on a bare name = %d, want 400: %s", tc.name, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if got := xmlValue(t, body, "Code"); got != "ValidationError" {
			t.Errorf("%s: error code = %q, want ValidationError", tc.name, got)
		}
		if !strings.Contains(body, "is not a valid") {
			t.Errorf("%s message does not say what was wrong: %s", tc.name, body)
		}
	}
}

// The physical ID CloudFormation falls back to when a create response carries
// no ARN — "…:loadbalancer/<name>", with no type segment or id — still has to
// resolve as a load balancer ARN, or a teardown of that stack reads as a
// validation failure instead of a resource that is already gone.
func TestScopeShape_acceptsTheCloudFormationFallbackARN(t *testing.T) {
	for _, tc := range []struct {
		scope identifierScope
		arn   string
	}{
		{loadBalancerScope, "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/web"},
		{targetGroupScope, "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/web"},
	} {
		if !tc.scope.shape.MatchString(tc.arn) {
			t.Errorf("%s scope rejects %q", tc.scope.segment, tc.arn)
		}
	}
}

package elbv2

// loadbalancer_attributes_test.go — ModifyLoadBalancerAttributes and its
// Describe counterpart.
//
// These answered 501 until now, and the cost was not the attribute. A CDK
// load balancer sets deletion_protection.enabled by default, so every CDK
// stack carrying one issued this call on every update; CloudFormation's
// provisioner treats a failed in-place update as "this resource needs
// replacing" and tore the load balancer down mid-deploy, taking the listener
// and the target group the ECS service was registered with:
//
//	cfn: in-place update failed, falling back to replace
//	  type=AWS::ElasticLoadBalancingV2::LoadBalancer
//	  error=ModifyLoadBalancerAttributes: HTTP 501: … NotImplemented

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
	"go.uber.org/zap"
)

func attrHandler(t *testing.T) *Handler {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	return New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New()).handler
}

func elbv2Call(t *testing.T, fn http.HandlerFunc, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

// createNLB stands up the shape CDK's NetworkLoadBalancedFargateService emits.
func createNLB(t *testing.T, h *Handler, name string) string {
	t.Helper()
	rec := elbv2Call(t, h.CreateLoadBalancer, url.Values{
		"Name": {name}, "Type": {"network"}, "Scheme": {"internal"},
		"Subnets.member.1": {"subnet-0123456789abcdef0"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateLoadBalancer: %d %s", rec.Code, rec.Body.String())
	}
	arn := xmlValue(t, rec.Body.String(), "LoadBalancerArn")
	return arn
}

// xmlValue is the ec2 package's helper of the same name — the first value of a
// tag in a Query-protocol response body.
func xmlValue(t *testing.T, body, tag string) string {
	t.Helper()
	m := regexp.MustCompile("<" + tag + ">([^<]+)</" + tag + ">").FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no <%s> in response: %s", tag, body)
	}
	return m[1]
}

func TestModifyLoadBalancerAttributes_roundTripsThroughDescribe(t *testing.T) {
	h := attrHandler(t)
	arn := createNLB(t, h, "nlb-l-ase2-ms-task-service")

	// The call CDK makes: the attribute set on the load balancer by default,
	// with the member indices CloudFormation's provisioner emits.
	rec := elbv2Call(t, h.ModifyLoadBalancerAttributes, url.Values{
		"LoadBalancerArn":           {arn},
		"Attributes.member.1.Key":   {"deletion_protection.enabled"},
		"Attributes.member.1.Value": {"false"},
		"Attributes.member.2.Key":   {"load_balancing.cross_zone.enabled"},
		"Attributes.member.2.Value": {"true"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ModifyLoadBalancerAttributes: %d %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "deletion_protection.enabled") {
		t.Errorf("the modify response does not echo the attribute it set: %s", body)
	}

	// And it is readable back — AWS answers Describe from the same store.
	rec = elbv2Call(t, h.DescribeLoadBalancerAttributes, url.Values{"LoadBalancerArn": {arn}})
	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeLoadBalancerAttributes: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"deletion_protection.enabled", "load_balancing.cross_zone.enabled", "true"} {
		if !strings.Contains(body, want) {
			t.Errorf("Describe response is missing %q: %s", want, body)
		}
	}
}

func TestModifyLoadBalancerAttributes_mergesRatherThanReplaces(t *testing.T) {
	h := attrHandler(t)
	arn := createNLB(t, h, "nlb-merge")

	elbv2Call(t, h.ModifyLoadBalancerAttributes, url.Values{
		"LoadBalancerArn":           {arn},
		"Attributes.member.1.Key":   {"idle_timeout.timeout_seconds"},
		"Attributes.member.1.Value": {"120"},
	})
	// A second call naming only one attribute must not drop the first: AWS
	// merges into the set rather than replacing it, and CloudFormation relies
	// on that when it updates one property of many.
	elbv2Call(t, h.ModifyLoadBalancerAttributes, url.Values{
		"LoadBalancerArn":           {arn},
		"Attributes.member.1.Key":   {"deletion_protection.enabled"},
		"Attributes.member.1.Value": {"true"},
	})

	rec := elbv2Call(t, h.DescribeLoadBalancerAttributes, url.Values{"LoadBalancerArn": {arn}})
	body := rec.Body.String()
	if !strings.Contains(body, "idle_timeout.timeout_seconds") {
		t.Errorf("the second modify dropped the attribute the first set: %s", body)
	}
	if !strings.Contains(body, "deletion_protection.enabled") {
		t.Errorf("the second modify did not land: %s", body)
	}
}

func TestLoadBalancerAttributes_unknownARNIsNotFound(t *testing.T) {
	h := attrHandler(t)
	missing := "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/net/nope/0123456789abcdef"
	for name, fn := range map[string]http.HandlerFunc{
		"ModifyLoadBalancerAttributes":   h.ModifyLoadBalancerAttributes,
		"DescribeLoadBalancerAttributes": h.DescribeLoadBalancerAttributes,
	} {
		// 400, not 404: the ELBv2 API reference gives LoadBalancerNotFound an
		// HTTP status of 400, as it does every one of its documented errors.
		// These answered 404 while the code lived in its own helper here; it
		// comes from identifierScope now, with the Describe* paths (#1718).
		rec := elbv2Call(t, fn, url.Values{"LoadBalancerArn": {missing}})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s on an unknown ARN = %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "LoadBalancerNotFound") {
			t.Errorf("%s error code: %s", name, rec.Body.String())
		}
	}
}

func TestLoadBalancerAttributes_survivesDescribeLoadBalancers(t *testing.T) {
	// The attributes live on the stored load balancer, so the record has to
	// still describe correctly with them set.
	h := attrHandler(t)
	arn := createNLB(t, h, "nlb-describe")
	elbv2Call(t, h.ModifyLoadBalancerAttributes, url.Values{
		"LoadBalancerArn":           {arn},
		"Attributes.member.1.Key":   {"access_logs.s3.enabled"},
		"Attributes.member.1.Value": {"false"},
	})
	rec := elbv2Call(t, h.DescribeLoadBalancers, url.Values{"LoadBalancerArns.member.1": {arn}})
	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeLoadBalancers: %d %s", rec.Code, rec.Body.String())
	}
	if got := xmlValue(t, rec.Body.String(), "LoadBalancerArn"); got != arn {
		t.Errorf("DescribeLoadBalancers returned %q, want %q", got, arn)
	}
}

// Package elbv2_test contains integration tests for the ELBv2 emulator.
//
// Run: go test ./tests/integration/elbv2/...
package elbv2_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func elbCall(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	params.Set("Action", action)
	params.Set("Version", "2015-12-01")
	resp, err := http.PostForm(srv.URL+"/", params)
	if err != nil {
		t.Fatalf("elbCall %s: %v", action, err)
	}
	return resp
}

func decodeXML(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := xml.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
}

func createLB(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := elbCall(t, srv, "CreateLoadBalancer", url.Values{
		"Name":             {name},
		"Type":             {"application"},
		"Scheme":           {"internet-facing"},
		"Subnets.member.1": {"subnet-aaa111"},
		"Subnets.member.2": {"subnet-bbb222"},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			LoadBalancers struct {
				Member []struct {
					LoadBalancerArn string `xml:"LoadBalancerArn"`
				} `xml:"member"`
			} `xml:"LoadBalancers"`
		} `xml:"CreateLoadBalancerResult"`
	}
	decodeXML(t, resp, &out)
	if len(out.Result.LoadBalancers.Member) == 0 {
		t.Fatal("expected at least one load balancer in CreateLoadBalancer response")
	}
	arn := out.Result.LoadBalancers.Member[0].LoadBalancerArn
	if arn == "" {
		t.Fatal("expected LoadBalancerArn to be non-empty")
	}
	return arn
}

func createTG(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := elbCall(t, srv, "CreateTargetGroup", url.Values{
		"Name":       {name},
		"Protocol":   {"HTTP"},
		"Port":       {"80"},
		"VpcId":      {"vpc-12345"},
		"TargetType": {"instance"},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			TargetGroups struct {
				Member []struct {
					TargetGroupArn string `xml:"TargetGroupArn"`
				} `xml:"member"`
			} `xml:"TargetGroups"`
		} `xml:"CreateTargetGroupResult"`
	}
	decodeXML(t, resp, &out)
	if len(out.Result.TargetGroups.Member) == 0 {
		t.Fatal("expected at least one target group in CreateTargetGroup response")
	}
	arn := out.Result.TargetGroups.Member[0].TargetGroupArn
	if arn == "" {
		t.Fatal("expected TargetGroupArn to be non-empty")
	}
	return arn
}

// ── CreateLoadBalancer ────────────────────────────────────────────────────────

func TestCreateLoadBalancer_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))

	// When: CreateLoadBalancer is called
	arn := createLB(t, srv, "my-alb")

	// Then: a valid ARN is returned
	if !strings.Contains(arn, "loadbalancer") {
		t.Errorf("expected ARN to contain 'loadbalancer', got %q", arn)
	}
}

func TestCreateLoadBalancer_missingName(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))

	// When: CreateLoadBalancer is called without a name
	resp := elbCall(t, srv, "CreateLoadBalancer", url.Values{
		"Type": {"application"},
	})
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	// Then: 400 is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ── DescribeLoadBalancers ─────────────────────────────────────────────────────

func TestDescribeLoadBalancers_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))

	// When: DescribeLoadBalancers is called
	resp := elbCall(t, srv, "DescribeLoadBalancers", url.Values{})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			LoadBalancers struct {
				Member []struct{ LoadBalancerArn string } `xml:"member"`
			} `xml:"LoadBalancers"`
		} `xml:"DescribeLoadBalancersResult"`
	}
	decodeXML(t, resp, &out)

	// Then: no load balancers returned
	if len(out.Result.LoadBalancers.Member) != 0 {
		t.Errorf("expected 0 LBs, got %d", len(out.Result.LoadBalancers.Member))
	}
}

func TestDescribeLoadBalancers_afterCreate(t *testing.T) {
	// Given: two load balancers exist
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))
	createLB(t, srv, "alb-alpha")
	createLB(t, srv, "alb-beta")

	// When: DescribeLoadBalancers is called
	resp := elbCall(t, srv, "DescribeLoadBalancers", url.Values{})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			LoadBalancers struct {
				Member []struct{ LoadBalancerArn string } `xml:"member"`
			} `xml:"LoadBalancers"`
		} `xml:"DescribeLoadBalancersResult"`
	}
	decodeXML(t, resp, &out)

	// Then: 2 LBs are returned
	if len(out.Result.LoadBalancers.Member) != 2 {
		t.Errorf("expected 2 LBs, got %d", len(out.Result.LoadBalancers.Member))
	}
}

// ── DeleteLoadBalancer ────────────────────────────────────────────────────────

func TestDeleteLoadBalancer_success(t *testing.T) {
	// Given: an LB exists
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))
	arn := createLB(t, srv, "delete-me")

	// When: DeleteLoadBalancer is called
	resp := elbCall(t, srv, "DeleteLoadBalancer", url.Values{
		"LoadBalancerArn": {arn},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	// Then: LB is gone from DescribeLoadBalancers
	descResp := elbCall(t, srv, "DescribeLoadBalancers", url.Values{})
	var out struct {
		Result struct {
			LoadBalancers struct {
				Member []struct{ LoadBalancerArn string } `xml:"member"`
			} `xml:"LoadBalancers"`
		} `xml:"DescribeLoadBalancersResult"`
	}
	decodeXML(t, descResp, &out)
	if len(out.Result.LoadBalancers.Member) != 0 {
		t.Errorf("expected 0 LBs after delete, got %d", len(out.Result.LoadBalancers.Member))
	}
}

// ── CreateTargetGroup ────────────────────────────────────────────────────────

func TestCreateTargetGroup_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))

	// When: CreateTargetGroup is called
	arn := createTG(t, srv, "my-tg")

	// Then: a valid ARN is returned
	if !strings.Contains(arn, "targetgroup") {
		t.Errorf("expected ARN to contain 'targetgroup', got %q", arn)
	}
}

// ── DescribeTargetGroups ─────────────────────────────────────────────────────

func TestDescribeTargetGroups_afterCreate(t *testing.T) {
	// Given: a target group exists
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))
	createTG(t, srv, "tg-one")

	// When: DescribeTargetGroups is called
	resp := elbCall(t, srv, "DescribeTargetGroups", url.Values{})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			TargetGroups struct {
				Member []struct{ TargetGroupArn string } `xml:"member"`
			} `xml:"TargetGroups"`
		} `xml:"DescribeTargetGroupsResult"`
	}
	decodeXML(t, resp, &out)

	// Then: 1 TG is returned
	if len(out.Result.TargetGroups.Member) != 1 {
		t.Errorf("expected 1 TG, got %d", len(out.Result.TargetGroups.Member))
	}
}

// ── CreateListener ────────────────────────────────────────────────────────────

func TestCreateListener_success(t *testing.T) {
	// Given: an LB and TG exist
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))
	lbArn := createLB(t, srv, "my-alb")
	tgArn := createTG(t, srv, "my-tg")

	// When: CreateListener is called
	resp := elbCall(t, srv, "CreateListener", url.Values{
		"LoadBalancerArn":                        {lbArn},
		"Protocol":                               {"HTTP"},
		"Port":                                   {"80"},
		"DefaultActions.member.1.Type":           {"forward"},
		"DefaultActions.member.1.TargetGroupArn": {tgArn},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			Listeners struct {
				Member []struct {
					ListenerArn string `xml:"ListenerArn"`
				} `xml:"member"`
			} `xml:"Listeners"`
		} `xml:"CreateListenerResult"`
	}
	decodeXML(t, resp, &out)

	// Then: a valid listener ARN is returned
	if len(out.Result.Listeners.Member) == 0 || out.Result.Listeners.Member[0].ListenerArn == "" {
		t.Error("expected a ListenerArn to be returned")
	}
}

// ── DescribeListeners ────────────────────────────────────────────────────────

func TestDescribeListeners_afterCreate(t *testing.T) {
	// Given: a listener exists
	srv := helpers.NewTestServer(t, helpers.WithServices("elbv2"))
	lbArn := createLB(t, srv, "my-alb")
	tgArn := createTG(t, srv, "my-tg")
	elbCall(t, srv, "CreateListener", url.Values{
		"LoadBalancerArn":                        {lbArn},
		"Protocol":                               {"HTTP"},
		"Port":                                   {"80"},
		"DefaultActions.member.1.Type":           {"forward"},
		"DefaultActions.member.1.TargetGroupArn": {tgArn},
	}).Body.Close()

	// When: DescribeListeners is called
	resp := elbCall(t, srv, "DescribeListeners", url.Values{
		"LoadBalancerArn": {lbArn},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			Listeners struct {
				Member []struct{ ListenerArn string } `xml:"member"`
			} `xml:"Listeners"`
		} `xml:"DescribeListenersResult"`
	}
	decodeXML(t, resp, &out)

	// Then: 1 listener is returned
	if len(out.Result.Listeners.Member) != 1 {
		t.Errorf("expected 1 listener, got %d", len(out.Result.Listeners.Member))
	}
}

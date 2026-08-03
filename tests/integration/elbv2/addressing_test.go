package elbv2_test

// addressing_test.go — naming a load balancer, a target group or a listener and
// getting back the one you named. Shares elbCall, createLB, createTG and
// decodeXML with elbv2_test.go.

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// createListener attaches a listener forwarding to tgArn and returns its ARN.
func createListener(t *testing.T, srv *helpers.TestServer, lbArn, tgArn string) string {
	t.Helper()
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
	if len(out.Result.Listeners.Member) == 0 {
		t.Fatal("expected a listener in the CreateListener response")
	}
	return out.Result.Listeners.Member[0].ListenerArn
}

func TestCreateLoadBalancer_arnUsesTheAWSTypeAbbreviation(t *testing.T) {
	// Given/When: an application load balancer is created.
	srv := helpers.NewTestServer(t)
	arn := createLB(t, srv, "abbrev-alb")

	// Then: its ARN carries "app", the abbreviation AWS uses — never the full
	// type name the API takes. Anything that parses the ARN (CDK's Arn.split,
	// IAM resource patterns) is written against the abbreviation.
	if !strings.Contains(arn, ":loadbalancer/app/abbrev-alb/") {
		t.Errorf("ARN %q should carry loadbalancer/app/<name>/<id>", arn)
	}
}

func TestCreateListener_arnIdentifiesItsLoadBalancer(t *testing.T) {
	// Given: a load balancer and a target group.
	srv := helpers.NewTestServer(t)
	lbArn := createLB(t, srv, "arn-alb")
	tgArn := createTG(t, srv, "arn-tg")

	// When: a listener is created on it.
	listenerArn := createListener(t, srv, lbArn, tgArn)

	// Then: the listener ARN repeats the load balancer's own name and id, as
	// AWS's listener/app/{name}/{lb-id}/{listener-id} does — so the listener
	// can be traced back to the load balancer it belongs to.
	lbSuffix := lbArn[strings.Index(lbArn, ":loadbalancer/")+len(":loadbalancer/"):]
	if !strings.Contains(listenerArn, ":listener/"+lbSuffix+"/") {
		t.Errorf("listener ARN %q should carry the load balancer's %q", listenerArn, lbSuffix)
	}
}

func TestDescribeLoadBalancers_filtersByName(t *testing.T) {
	// Given: two load balancers.
	srv := helpers.NewTestServer(t)
	createLB(t, srv, "named-one")
	wanted := createLB(t, srv, "named-two")

	// When: one is described by name.
	resp := elbCall(t, srv, "DescribeLoadBalancers", url.Values{"Names.member.1": {"named-two"}})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			LoadBalancers struct {
				Member []struct {
					LoadBalancerArn string `xml:"LoadBalancerArn"`
				} `xml:"member"`
			} `xml:"LoadBalancers"`
		} `xml:"DescribeLoadBalancersResult"`
	}
	decodeXML(t, resp, &out)

	// Then: only that one comes back. Ignoring Names hands the caller whichever
	// load balancer happens to sort first, and every ARN taken from it — the
	// one a listener is then created against — belongs to the wrong one.
	if len(out.Result.LoadBalancers.Member) != 1 {
		t.Fatalf("expected 1 load balancer, got %d", len(out.Result.LoadBalancers.Member))
	}
	if got := out.Result.LoadBalancers.Member[0].LoadBalancerArn; got != wanted {
		t.Errorf("described %q, want %q", got, wanted)
	}
}

func TestDescribeTargetGroups_filtersByName(t *testing.T) {
	// Given: two target groups.
	srv := helpers.NewTestServer(t)
	createTG(t, srv, "tg-one")
	wanted := createTG(t, srv, "tg-two")

	// When: one is described by name.
	resp := elbCall(t, srv, "DescribeTargetGroups", url.Values{"Names.member.1": {"tg-two"}})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			TargetGroups struct {
				Member []struct {
					TargetGroupArn string `xml:"TargetGroupArn"`
				} `xml:"member"`
			} `xml:"TargetGroups"`
		} `xml:"DescribeTargetGroupsResult"`
	}
	decodeXML(t, resp, &out)

	// Then: only that one comes back.
	if len(out.Result.TargetGroups.Member) != 1 {
		t.Fatalf("expected 1 target group, got %d", len(out.Result.TargetGroups.Member))
	}
	if got := out.Result.TargetGroups.Member[0].TargetGroupArn; got != wanted {
		t.Errorf("described %q, want %q", got, wanted)
	}
}

func TestDescribeListeners_filtersByListenerArn(t *testing.T) {
	// Given: two load balancers, each with a listener.
	srv := helpers.NewTestServer(t)
	lb1 := createLB(t, srv, "lsn-alb1")
	lb2 := createLB(t, srv, "lsn-alb2")
	createListener(t, srv, lb1, createTG(t, srv, "lsn-tg1"))
	wanted := createListener(t, srv, lb2, createTG(t, srv, "lsn-tg2"))

	// When: one listener is described by its own ARN.
	resp := elbCall(t, srv, "DescribeListeners", url.Values{"ListenerArns.member.1": {wanted}})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Result struct {
			Listeners struct {
				Member []struct {
					ListenerArn string `xml:"ListenerArn"`
				} `xml:"member"`
			} `xml:"Listeners"`
		} `xml:"DescribeListenersResult"`
	}
	decodeXML(t, resp, &out)

	// Then: only that listener comes back.
	if len(out.Result.Listeners.Member) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(out.Result.Listeners.Member))
	}
	if got := out.Result.Listeners.Member[0].ListenerArn; got != wanted {
		t.Errorf("described %q, want %q", got, wanted)
	}
}

func TestLoadBalancers_eachForwardsToItsOwnTargets(t *testing.T) {
	// Given: two load balancers, each with its own listener, target group and
	// backend — the shape any stack with more than one service has.
	srv := helpers.NewTestServer(t)
	hostA, portA, _ := backend(t, "from A")
	hostB, portB, _ := backend(t, "from B")

	lbA := createLB(t, srv, "pair-alb-a")
	lbB := createLB(t, srv, "pair-alb-b")
	tgA := createTG(t, srv, "pair-tg-a")
	tgB := createTG(t, srv, "pair-tg-b")
	createListener(t, srv, lbA, tgA)
	createListener(t, srv, lbB, tgB)

	for _, reg := range []struct {
		tg   string
		host string
		port int
	}{{tgA, hostA, portA}, {tgB, hostB, portB}} {
		resp := elbCall(t, srv, "RegisterTargets", url.Values{
			"TargetGroupArn":        {reg.tg},
			"Targets.member.1.Id":   {reg.host},
			"Targets.member.1.Port": {strconv.Itoa(reg.port)},
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}

	// When: each load balancer's DNS name is requested.
	// Then: each answers from its own backend, rather than one of them serving
	// 503 or the other one's application.
	for _, want := range []struct {
		arn  string
		body string
	}{{lbA, "from A"}, {lbB, "from B"}} {
		got := getViaLoadBalancer(t, srv, lbDNSName(t, srv, want.arn), "/")
		helpers.AssertStatus(t, got, http.StatusOK)
		body, _ := io.ReadAll(got.Body)
		got.Body.Close()
		if !strings.Contains(string(body), want.body) {
			t.Errorf("load balancer %s answered %q, want %q", want.arn, body, want.body)
		}
	}
}

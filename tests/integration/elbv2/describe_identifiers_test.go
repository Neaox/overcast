package elbv2_test

// describe_identifiers_test.go — a Describe*'s explicit ARN or Name list is a
// question about named resources, not a filter (#1718).
//
// ELBv2 draws the same line EC2 does (#1708, PR #1846): a Describe naming
// nothing is a listing, and an empty one is a legitimate 200; a Describe naming
// an ARN or a Name asserts that resource exists, and AWS answers
// LoadBalancerNotFound / TargetGroupNotFound / ListenerNotFound when it does
// not. Terraform's and CloudFormation's refresh logic reads those exceptions to
// mean "gone, drop it from state"; handed a 200 with an empty list instead, a
// provider that only inspects the exception concludes the resource is fine and
// then indexes into an empty list.
//
// Every case here pins one half of that distinction, including the half that
// must *not* have changed: a Describe with no identifiers is still an empty 200.
//
// Shares elbCall, createLB, createTG and decodeXML with elbv2_test.go, and
// createListener with addressing_test.go.

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// elbErrorBody is the Query-protocol ErrorResponse envelope ELBv2 answers with.
type elbErrorBody struct {
	Error struct {
		Type    string `xml:"Type"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
}

// assertELBError checks the status and error code of a failed call and returns
// the message, so a case can also assert that the message names the identifier
// the caller got wrong.
func assertELBError(t *testing.T, resp *http.Response, status int, code string) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, status, body)
	}
	var out elbErrorBody
	if err := xml.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal ErrorResponse: %v\nbody: %s", err, body)
	}
	if out.Error.Code != code {
		t.Fatalf("error code = %q, want %q: %s", out.Error.Code, code, body)
	}
	return out.Error.Message
}

// A well-formed ARN for a resource the region does not hold, per operation.
const (
	absentLBArn       = "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/absent/0123456789abcdef"
	absentTGArn       = "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/absent/0123456789abcdef"
	absentListenerArn = "arn:aws:elasticloadbalancing:us-east-1:000000000000:listener/app/absent/0123456789abcdef/fedcba9876543210"
)

func TestDescribe_unknownExplicitARNIsNotFound(t *testing.T) {
	cases := []struct {
		action string
		param  string
		arn    string
		code   string
	}{
		{"DescribeLoadBalancers", "LoadBalancerArns.member.1", absentLBArn, "LoadBalancerNotFound"},
		{"DescribeTargetGroups", "TargetGroupArns.member.1", absentTGArn, "TargetGroupNotFound"},
		{"DescribeListeners", "ListenerArns.member.1", absentListenerArn, "ListenerNotFound"},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := elbCall(t, srv, tc.action, url.Values{tc.param: {tc.arn}})
			msg := assertELBError(t, resp, http.StatusBadRequest, tc.code)
			if !strings.Contains(msg, tc.arn) {
				t.Errorf("message %q does not name the ARN %q", msg, tc.arn)
			}
		})
	}
}

// The Names form asserts existence exactly as the ARN form does — a Terraform
// data source looking a load balancer up by name is the common way to hit this.
func TestDescribe_unknownExplicitNameIsNotFound(t *testing.T) {
	cases := []struct {
		action string
		code   string
	}{
		{"DescribeLoadBalancers", "LoadBalancerNotFound"},
		{"DescribeTargetGroups", "TargetGroupNotFound"},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := elbCall(t, srv, tc.action, url.Values{"Names.member.1": {"no-such-thing"}})
			msg := assertELBError(t, resp, http.StatusBadRequest, tc.code)
			if !strings.Contains(msg, "no-such-thing") {
				t.Errorf("message %q does not name the requested name", msg)
			}
		})
	}
}

// An ARN that is not an ARN for this resource is a bad request, not a deleted
// resource. AWS separates the two so a typo, a truncated value or a target
// group ARN passed where a load balancer ARN belongs fails loudly instead of
// reading as "it is gone".
func TestDescribe_malformedARNIsValidationError(t *testing.T) {
	cases := []struct {
		action string
		param  string
		value  string
	}{
		{"DescribeLoadBalancers", "LoadBalancerArns.member.1", "not-an-arn"},
		{"DescribeTargetGroups", "TargetGroupArns.member.1", "not-an-arn"},
		{"DescribeListeners", "ListenerArns.member.1", "not-an-arn"},
		// The right service, the wrong resource: a target group ARN handed to
		// DescribeLoadBalancers.
		{"DescribeLoadBalancers", "LoadBalancerArns.member.1", absentTGArn},
	}

	for _, tc := range cases {
		t.Run(tc.action+"/"+tc.value, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := elbCall(t, srv, tc.action, url.Values{tc.param: {tc.value}})
			msg := assertELBError(t, resp, http.StatusBadRequest, "ValidationError")
			if !strings.Contains(msg, tc.value) {
				t.Errorf("message %q does not name the value %q", msg, tc.value)
			}
		})
	}
}

// A mixed request fails rather than answering with the part that resolved: a
// caller that named two load balancers and got one back would have to compare
// the result against its own request to notice.
func TestDescribeLoadBalancers_mixedKnownAndUnknownFails(t *testing.T) {
	srv := helpers.NewTestServer(t)
	known := createLB(t, srv, "mixed-alb")

	resp := elbCall(t, srv, "DescribeLoadBalancers", url.Values{
		"LoadBalancerArns.member.1": {known},
		"LoadBalancerArns.member.2": {absentLBArn},
	})
	msg := assertELBError(t, resp, http.StatusBadRequest, "LoadBalancerNotFound")
	if !strings.Contains(msg, absentLBArn) {
		t.Errorf("message %q names the wrong ARN", msg)
	}
}

// Shape is checked across the whole list before any of it is resolved, which is
// where AWS checks it: a malformed value fails request validation ahead of the
// lookup, so a well-formed-but-absent ARN listed first does not get to answer
// before it.
func TestDescribeTargetGroups_malformedARNWinsOverAnUnknownOne(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := elbCall(t, srv, "DescribeTargetGroups", url.Values{
		"TargetGroupArns.member.1": {absentTGArn},
		"TargetGroupArns.member.2": {"not-an-arn"},
	})
	assertELBError(t, resp, http.StatusBadRequest, "ValidationError")
}

// The regression risk #1718 names, and the reason this is not "Describe should
// error when it finds nothing": a Describe that names no identifier is a
// listing, and an account with nothing in it is a legitimate empty 200.
func TestDescribe_noIdentifiersIsStillAnEmpty200(t *testing.T) {
	for _, action := range []string{"DescribeLoadBalancers", "DescribeTargetGroups", "DescribeListeners"} {
		t.Run(action, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := elbCall(t, srv, action, url.Values{})
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusOK)
			body, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(body), "<member>") {
				t.Errorf("%s with no identifiers returned members: %s", action, body)
			}
		})
	}
}

// And a Describe naming nothing against an account that does hold resources
// still lists them, so the new resolution cannot have turned an unselected
// Describe into an error.
func TestDescribe_noIdentifiersStillListsTheRegion(t *testing.T) {
	srv := helpers.NewTestServer(t)
	lbArn := createLB(t, srv, "listing-alb")
	tgArn := createTG(t, srv, "listing-tg")
	listenerArn := createListener(t, srv, lbArn, tgArn)

	for _, tc := range []struct {
		action string
		want   string
	}{
		{"DescribeLoadBalancers", lbArn},
		{"DescribeTargetGroups", tgArn},
		{"DescribeListeners", listenerArn},
	} {
		t.Run(tc.action, func(t *testing.T) {
			resp := elbCall(t, srv, tc.action, url.Values{})
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusOK)
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), tc.want) {
				t.Errorf("%s with no identifiers did not return %s: %s", tc.action, tc.want, body)
			}
		})
	}
}

// DescribeListeners' LoadBalancerArn is an explicit identifier too — it names
// one load balancer rather than describing a property listeners are matched on.
func TestDescribeListeners_unknownLoadBalancerArnIsNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := elbCall(t, srv, "DescribeListeners", url.Values{"LoadBalancerArn": {absentLBArn}})
	msg := assertELBError(t, resp, http.StatusBadRequest, "LoadBalancerNotFound")
	if !strings.Contains(msg, absentLBArn) {
		t.Errorf("message %q does not name the ARN", msg)
	}
}

// A load balancer that exists and has no listeners is still an empty 200 — the
// identifier resolved, and "no listeners on it" is the answer.
func TestDescribeListeners_knownLoadBalancerWithNoListenersIsEmpty200(t *testing.T) {
	srv := helpers.NewTestServer(t)
	lbArn := createLB(t, srv, "quiet-alb")

	resp := elbCall(t, srv, "DescribeListeners", url.Values{"LoadBalancerArn": {lbArn}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<member>") {
		t.Errorf("DescribeListeners on a listener-less load balancer returned members: %s", body)
	}
}

// ── Create-time validation ───────────────────────────────────────────────────

// A load balancer with no subnets cannot exist on AWS — subnets are what give
// it its network presence — so accepting one produces a resource whose shape no
// real deployment could have.
func TestCreateLoadBalancer_withoutSubnetsIsRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := elbCall(t, srv, "CreateLoadBalancer", url.Values{
		"Name": {"subnetless-alb"},
		"Type": {"application"},
	})
	assertELBError(t, resp, http.StatusBadRequest, "ValidationError")
}

// SubnetMappings is the other way to name them, and satisfies the same rule.
func TestCreateLoadBalancer_subnetMappingsSatisfyTheSubnetRule(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := elbCall(t, srv, "CreateLoadBalancer", url.Values{
		"Name":                             {"mapped-nlb"},
		"Type":                             {"network"},
		"SubnetMappings.member.1.SubnetId": {"subnet-0123456789abcdef0"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestCreateTargetGroup_rejectsAnUnknownProtocol(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := elbCall(t, srv, "CreateTargetGroup", url.Values{
		"Name":     {"bogus-proto-tg"},
		"Protocol": {"HTTPX"},
		"Port":     {"80"},
		"VpcId":    {"vpc-12345"},
	})
	msg := assertELBError(t, resp, http.StatusBadRequest, "ValidationError")
	if !strings.Contains(msg, "HTTPX") {
		t.Errorf("message %q does not name the rejected protocol", msg)
	}
}

func TestCreateTargetGroup_rejectsAPortOutsideTheLegalRange(t *testing.T) {
	for _, port := range []string{"0", "70000", "-1"} {
		t.Run(port, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := elbCall(t, srv, "CreateTargetGroup", url.Values{
				"Name":     {"bad-port-tg"},
				"Protocol": {"HTTP"},
				"Port":     {port},
				"VpcId":    {"vpc-12345"},
			})
			assertELBError(t, resp, http.StatusBadRequest, "ValidationError")
		})
	}
}

// A lambda target group carries neither a protocol nor a port, so the two
// checks above must not turn "absent" into "invalid".
func TestCreateTargetGroup_lambdaNeedsNoProtocolOrPort(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := elbCall(t, srv, "CreateTargetGroup", url.Values{
		"Name":       {"lambda-tg"},
		"TargetType": {"lambda"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ── ARN shape ────────────────────────────────────────────────────────────────

// AWS ends a load balancer, target group and listener ARN with sixteen
// lowercase hex characters. Overcast minted a truncated UUID, which for target
// groups and listeners was twelve characters and carried a "-" — anything that
// parses the suffix, or asserts the shape, disagrees with AWS.
func TestARNs_endInSixteenLowercaseHexCharacters(t *testing.T) {
	srv := helpers.NewTestServer(t)
	lbArn := createLB(t, srv, "hex-alb")
	tgArn := createTG(t, srv, "hex-tg")
	listenerArn := createListener(t, srv, lbArn, tgArn)

	suffix := regexp.MustCompile(`^[0-9a-f]{16}$`)
	for _, tc := range []struct{ name, arn string }{
		{"load balancer", lbArn},
		{"target group", tgArn},
		{"listener", listenerArn},
	} {
		parts := strings.Split(tc.arn, "/")
		if got := parts[len(parts)-1]; !suffix.MatchString(got) {
			t.Errorf("%s ARN %q ends in %q, want 16 lowercase hex characters", tc.name, tc.arn, got)
		}
	}
}

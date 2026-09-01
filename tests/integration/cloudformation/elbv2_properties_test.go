package cloudformation_test

// elbv2_properties_test.go — AWS::ElasticLoadBalancingV2::TargetGroup,
// LoadBalancer and Listener property threading, pinning issue #527.
//
// elbv2TargetGroupHandler.Create (provisioner_query_rest_coverage.go) sent
// CreateTargetGroup only Name, Protocol, Port and VpcId: TargetType and the
// entire HealthCheck* family were parsed out of the template and then
// dropped on the floor before they ever reached the Query request, even
// though the elbv2 service itself stores and echoes them faithfully. That
// matters most for the Fargate path specifically — an awsvpc service
// registers "ip" targets, and a target group provisioned without TargetType
// silently came back as the "instance" default. elbv2LoadBalancerHandler.Create
// had the matching gap for Scheme: internet-facing vs internal never
// reached CreateLoadBalancer either.
//
// These tests do not touch Docker or ECS — they prove the CloudFormation
// provisioner threads the properties through to the stored elbv2 records by
// reading them back with DescribeTargetGroups/DescribeLoadBalancers/
// DescribeTargetGroupAttributes/DescribeTags, the same way
// elasticache_properties_test.go pins ElastiCache's equivalent gap.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// elbv2Query sends an ELBv2 Query protocol request.
func elbv2Query(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2015-12-01")
	body := strings.NewReader(params.Encode())
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("elbv2Query %s: %v", action, err)
	}
	return resp
}

// fargateShapedStackTemplate is an ALB + "ip" target group + HTTP listener —
// the shape a CDK ApplicationLoadBalancedFargateService synthesizes. It
// exercises exactly the properties the issue's Definition of Done names:
// TargetType, the HealthCheck* block, and the load balancer's Scheme.
const fargateShapedStackTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppLB": {
      "Type": "AWS::ElasticLoadBalancingV2::LoadBalancer",
      "Properties": {
        "Name": "cfn-fargate-alb",
        "Type": "application",
        "Scheme": "internal",
        "Subnets": ["subnet-aaa111", "subnet-bbb222"],
        "Tags": [{ "Key": "app", "Value": "fargate-demo" }]
      }
    },
    "AppTG": {
      "Type": "AWS::ElasticLoadBalancingV2::TargetGroup",
      "Properties": {
        "Name": "cfn-fargate-tg",
        "Protocol": "HTTP",
        "Port": 8080,
        "VpcId": "vpc-12345",
        "TargetType": "ip",
        "HealthCheckPath": "/healthz",
        "HealthCheckProtocol": "HTTP",
        "HealthCheckIntervalSeconds": 15,
        "HealthCheckTimeoutSeconds": 10,
        "HealthyThresholdCount": 3,
        "UnhealthyThresholdCount": 4,
        "Matcher": { "HttpCode": "200-299" },
        "TargetGroupAttributes": [
          { "Key": "deregistration_delay.timeout_seconds", "Value": "30" }
        ],
        "Tags": [{ "Key": "app", "Value": "fargate-demo" }]
      }
    },
    "AppListener": {
      "Type": "AWS::ElasticLoadBalancingV2::Listener",
      "Properties": {
        "LoadBalancerArn": { "Ref": "AppLB" },
        "Protocol": "HTTP",
        "Port": 80,
        "DefaultActions": [
          { "Type": "forward", "TargetGroupArn": { "Ref": "AppTG" } }
        ]
      }
    }
  },
  "Outputs": {
    "TargetGroupArn": { "Value": { "Ref": "AppTG" } },
    "LoadBalancerArn": { "Value": { "Ref": "AppLB" } }
  }
}`

// TestCreateStack_ELBv2FargateShape_targetTypeAndHealthCheckThreaded pins
// the priority-1 gap from issue #527: TargetType and the whole HealthCheck*
// family were dropped between CDK and the emulator.
func TestCreateStack_ELBv2FargateShape_targetTypeAndHealthCheckThreaded(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"elbv2-fargate-stack"},
		"TemplateBody": []string{fargateShapedStackTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "elbv2-fargate-stack", "CREATE_COMPLETE")

	dr := elbv2Query(t, srv, "DescribeTargetGroups", url.Values{
		"Names": []string{"cfn-fargate-tg"},
	})
	defer dr.Body.Close()
	helpers.AssertStatus(t, dr, http.StatusOK)
	body := string(readBody(t, dr))

	if !strings.Contains(body, "<TargetType>ip</TargetType>") {
		t.Fatalf("expected TargetType=ip to round-trip from Create, got: %s", body)
	}
	if strings.Contains(body, "<TargetType>instance</TargetType>") {
		t.Fatalf("target group fell back to the instance default despite TargetType=ip, got: %s", body)
	}
	if !strings.Contains(body, "<HealthCheckPath>/healthz</HealthCheckPath>") {
		t.Fatalf("expected HealthCheckPath to round-trip from Create, got: %s", body)
	}
	if !strings.Contains(body, "<HealthCheckIntervalSeconds>15</HealthCheckIntervalSeconds>") {
		t.Fatalf("expected HealthCheckIntervalSeconds=15 to round-trip from Create, got: %s", body)
	}
	if !strings.Contains(body, "<HealthCheckTimeoutSeconds>10</HealthCheckTimeoutSeconds>") {
		t.Fatalf("expected HealthCheckTimeoutSeconds=10 to round-trip from Create, got: %s", body)
	}
	if !strings.Contains(body, "<HealthyThresholdCount>3</HealthyThresholdCount>") {
		t.Fatalf("expected HealthyThresholdCount=3 to round-trip from Create, got: %s", body)
	}
	if !strings.Contains(body, "<UnhealthyThresholdCount>4</UnhealthyThresholdCount>") {
		t.Fatalf("expected UnhealthyThresholdCount=4 to round-trip from Create, got: %s", body)
	}
	if !strings.Contains(body, "<HttpCode>200-299</HttpCode>") {
		t.Fatalf("expected Matcher.HttpCode=200-299 to round-trip from Create, got: %s", body)
	}

	// TargetGroupAttributes has no field on CreateTargetGroup in the real
	// API — it only ever reaches a target group through
	// ModifyTargetGroupAttributes, so this proves the provisioner made that
	// follow-up call rather than silently dropping the property.
	tgArn := extractBetween(body, "<TargetGroupArn>", "</TargetGroupArn>")
	if tgArn == "" {
		t.Fatalf("expected a TargetGroupArn in DescribeTargetGroups body: %s", body)
	}
	attrResp := elbv2Query(t, srv, "DescribeTargetGroupAttributes", url.Values{
		"TargetGroupArn": []string{tgArn},
	})
	defer attrResp.Body.Close()
	helpers.AssertStatus(t, attrResp, http.StatusOK)
	attrBody := string(readBody(t, attrResp))
	if !strings.Contains(attrBody, "<Key>deregistration_delay.timeout_seconds</Key>") ||
		!strings.Contains(attrBody, "<Value>30</Value>") {
		t.Fatalf("expected the deregistration_delay attribute set at Create to be stored, got: %s", attrBody)
	}

	tagResp := elbv2Query(t, srv, "DescribeTags", url.Values{
		"ResourceArns.member.1": []string{tgArn},
	})
	defer tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)
	tagBody := string(readBody(t, tagResp))
	if !strings.Contains(tagBody, "<Key>app</Key>") || !strings.Contains(tagBody, "<Value>fargate-demo</Value>") {
		t.Fatalf("expected the app=fargate-demo tag supplied at Create to be stored, got: %s", tagBody)
	}
}

// TestCreateStack_ELBv2FargateShape_schemeThreaded pins the LoadBalancer
// half of issue #527: Scheme was stored and echoed by the elbv2 service but
// CreateLoadBalancer's Query request never carried it, so internal vs
// internet-facing never round-tripped from a template.
func TestCreateStack_ELBv2FargateShape_schemeThreaded(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"elbv2-fargate-scheme-stack"},
		"TemplateBody": []string{fargateShapedStackTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "elbv2-fargate-scheme-stack", "CREATE_COMPLETE")

	dr := elbv2Query(t, srv, "DescribeLoadBalancers", url.Values{
		"Names": []string{"cfn-fargate-alb"},
	})
	defer dr.Body.Close()
	helpers.AssertStatus(t, dr, http.StatusOK)
	body := string(readBody(t, dr))

	if !strings.Contains(body, "<Scheme>internal</Scheme>") {
		t.Fatalf("expected Scheme=internal to round-trip from Create, got: %s", body)
	}
	if strings.Contains(body, "<Scheme>internet-facing</Scheme>") {
		t.Fatalf("load balancer fell back to the internet-facing default despite Scheme=internal, got: %s", body)
	}

	lbArn := extractBetween(body, "<LoadBalancerArn>", "</LoadBalancerArn>")
	if lbArn == "" {
		t.Fatalf("expected a LoadBalancerArn in DescribeLoadBalancers body: %s", body)
	}
	tagResp := elbv2Query(t, srv, "DescribeTags", url.Values{
		"ResourceArns.member.1": []string{lbArn},
	})
	defer tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)
	tagBody := string(readBody(t, tagResp))
	if !strings.Contains(tagBody, "<Key>app</Key>") || !strings.Contains(tagBody, "<Value>fargate-demo</Value>") {
		t.Fatalf("expected the app=fargate-demo tag supplied at Create to be stored, got: %s", tagBody)
	}
}

// redirectListenerStackTemplate is a plain HTTP listener whose DefaultActions
// entry is a "redirect" action — the shape a CDK HTTP->HTTPS redirect
// listener synthesizes to. Priority 2 of issue #527: CreateListener only
// ever forwarded the Type and TargetGroupArn members of an action, so a
// redirect action's RedirectConfig was parsed out of the template and
// dropped, leaving a bare "redirect" action with no target.
const redirectListenerStackTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "RedirLB": {
      "Type": "AWS::ElasticLoadBalancingV2::LoadBalancer",
      "Properties": {
        "Name": "cfn-redirect-alb",
        "Type": "application",
        "Subnets": ["subnet-aaa111"]
      }
    },
    "RedirListener": {
      "Type": "AWS::ElasticLoadBalancingV2::Listener",
      "Properties": {
        "LoadBalancerArn": { "Ref": "RedirLB" },
        "Protocol": "HTTP",
        "Port": 80,
        "DefaultActions": [
          {
            "Type": "redirect",
            "RedirectConfig": {
              "Protocol": "HTTPS",
              "Port": "443",
              "StatusCode": "HTTP_301"
            }
          }
        ]
      }
    }
  }
}`

func TestCreateStack_ELBv2RedirectListener_redirectConfigThreaded(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"elbv2-redirect-stack"},
		"TemplateBody": []string{redirectListenerStackTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "elbv2-redirect-stack", "CREATE_COMPLETE")

	lbResp := elbv2Query(t, srv, "DescribeLoadBalancers", url.Values{
		"Names": []string{"cfn-redirect-alb"},
	})
	defer lbResp.Body.Close()
	helpers.AssertStatus(t, lbResp, http.StatusOK)
	lbBody := string(readBody(t, lbResp))
	lbArn := extractBetween(lbBody, "<LoadBalancerArn>", "</LoadBalancerArn>")
	if lbArn == "" {
		t.Fatalf("expected a LoadBalancerArn in DescribeLoadBalancers body: %s", lbBody)
	}

	dr := elbv2Query(t, srv, "DescribeListeners", url.Values{
		"LoadBalancerArn": []string{lbArn},
	})
	defer dr.Body.Close()
	helpers.AssertStatus(t, dr, http.StatusOK)
	body := string(readBody(t, dr))

	if !strings.Contains(body, "<Protocol>HTTPS</Protocol>") {
		t.Fatalf("expected the redirect action's RedirectConfig.Protocol=HTTPS to round-trip, got: %s", body)
	}
	if !strings.Contains(body, "<Port>443</Port>") {
		t.Fatalf("expected the redirect action's RedirectConfig.Port=443 to round-trip, got: %s", body)
	}
	if !strings.Contains(body, "<StatusCode>HTTP_301</StatusCode>") {
		t.Fatalf("expected the redirect action's RedirectConfig.StatusCode=HTTP_301 to round-trip, got: %s", body)
	}
}

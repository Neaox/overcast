package cloudformation_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestCFN_APIGateway_TopologyRegion verifies that an API Gateway REST API
// created via CloudFormation in a non-default region (ap-southeast-2)
// appears in the correct region on the topology map.
func TestCFN_APIGateway_TopologyRegion(t *testing.T) {
	srv := helpers.NewTestServer(t,
		helpers.WithRegion("us-east-1"),
		helpers.WithServices("cloudformation", "apigateway"),
	)

	template := `{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Resources": {
			"RestApi": {
				"Type": "AWS::ApiGateway::RestApi",
				"Properties": {
					"Name": "api-l-ase2-web-push-service"
				}
			},
			"MessagesResource": {
				"Type": "AWS::ApiGateway::Resource",
				"Properties": {
					"RestApiId": {"Ref": "RestApi"},
					"ParentId": {"Fn::GetAtt": ["RestApi", "RootResourceId"]},
					"PathPart": "messages"
				}
			}
		}
	}`

	// Deploy the stack with SigV4 credential scope = ap-southeast-2.
	params := url.Values{}
	params.Set("Action", "CreateStack")
	params.Set("Version", "2010-05-15")
	params.Set("StackName", "cf-l-ase2-web-push-service-api")
	params.Set("TemplateBody", template)
	body := strings.NewReader(params.Encode())
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Simulate CDK deploying to ap-southeast-2 via SigV4 credential scope.
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20260424/ap-southeast-2/cloudformation/aws4_request, "+
			"SignedHeaders=host, Signature=fakesig")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateStack: expected 200, got %d", resp.StatusCode)
	}

	// Wait for the stack to finish creating.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dParams := url.Values{}
		dParams.Set("Action", "DescribeStacks")
		dParams.Set("Version", "2010-05-15")
		dParams.Set("StackName", "cf-l-ase2-web-push-service-api")
		dReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(dParams.Encode()))
		dReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		dReq.Header.Set("Authorization",
			"AWS4-HMAC-SHA256 Credential=test/20260424/ap-southeast-2/cloudformation/aws4_request, "+
				"SignedHeaders=host, Signature=fakesig")
		dResp, err := http.DefaultClient.Do(dReq)
		if err != nil {
			t.Fatalf("DescribeStacks: %v", err)
		}
		b, _ := io.ReadAll(dResp.Body)
		dResp.Body.Close()
		bodyStr := string(b)
		if strings.Contains(bodyStr, "CREATE_COMPLETE") {
			break
		}
		if strings.Contains(bodyStr, "CREATE_FAILED") || strings.Contains(bodyStr, "ROLLBACK") {
			t.Fatalf("Stack creation failed: %s", bodyStr)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Fetch topology.
	topoResp, err := http.Get(srv.URL + "/_topology")
	if err != nil {
		t.Fatalf("GET /_topology: %v", err)
	}
	defer topoResp.Body.Close()
	topoBody, _ := io.ReadAll(topoResp.Body)

	var topo struct {
		Nodes []struct {
			ID           string `json:"id"`
			Service      string `json:"service"`
			Label        string `json:"label"`
			Region       string `json:"region"`
			ProtocolType string `json:"protocolType"`
			RouteCount   *int   `json:"routeCount"`
			StageCount   *int   `json:"stageCount"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(topoBody, &topo); err != nil {
		t.Fatalf("parse topology: %v (%s)", err, string(topoBody))
	}

	// Find the API Gateway node.
	var found bool
	for _, node := range topo.Nodes {
		if node.Service != "apigateway" {
			continue
		}
		found = true
		t.Logf("API Gateway node: %+v", node)

		// Verify region is ap-southeast-2, NOT us-east-1.
		if node.Region != "ap-southeast-2" {
			t.Errorf("expected region ap-southeast-2, got %q (node ID: %s)", node.Region, node.ID)
		}

		// Verify the node ID starts with the correct region.
		if !strings.HasPrefix(node.ID, "ap-southeast-2::apigateway::") {
			t.Errorf("expected node ID to start with ap-southeast-2::apigateway::, got %q", node.ID)
		}

		// Verify route count includes the child resource (at least root + messages = 2).
		if node.RouteCount == nil || *node.RouteCount < 1 {
			t.Errorf("expected at least 1 route, got %v", node.RouteCount)
		}

		// Also verify the API exists when listing with ap-southeast-2 region.
		listReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/restapis", nil)
		listReq.Header.Set("Authorization",
			fmt.Sprintf("AWS4-HMAC-SHA256 Credential=test/20260424/ap-southeast-2/apigateway/aws4_request, "+
				"SignedHeaders=host, Signature=fakesig"))
		listResp, err := http.DefaultClient.Do(listReq)
		if err != nil {
			t.Fatalf("GET /restapis: %v", err)
		}
		defer listResp.Body.Close()
		listBody, _ := io.ReadAll(listResp.Body)
		var listResult struct {
			Item []struct {
				ID string `json:"id"`
			} `json:"item"`
		}
		json.Unmarshal(listBody, &listResult)
		if len(listResult.Item) != 1 {
			t.Errorf("expected 1 REST API in ap-southeast-2 list, got %d", len(listResult.Item))
		}
	}

	if !found {
		t.Fatalf("no API Gateway node found in topology. All nodes: %s", string(topoBody))
	}
}

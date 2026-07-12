// Package ec2_test contains integration tests for the EC2 emulator.
//
// Run: go test ./tests/integration/ec2/...
package ec2_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ec2Query sends an EC2 Query protocol request.
func ec2Query(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2016-11-15")
	body := strings.NewReader(params.Encode())
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ec2Query %s: %v", action, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	return b
}

type ec2QueryErrorResponse struct {
	XMLName xml.Name `xml:"Response"`
	Errors  []struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Errors>Error"`
	RequestID string `xml:"RequestID"`
}

func assertEC2QueryError(t *testing.T, resp *http.Response, status int, code string) ec2QueryErrorResponse {
	t.Helper()
	helpers.AssertStatus(t, resp, status)
	helpers.AssertRequestID(t, resp)
	body := readBody(t, resp)
	var result ec2QueryErrorResponse
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal EC2 error: %v\nbody: %s", err, body)
	}
	if result.XMLName.Local != "Response" {
		t.Errorf("expected Response root, got %s", result.XMLName.Local)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected one error, got %d; body: %s", len(result.Errors), body)
	}
	if result.Errors[0].Code != code {
		t.Errorf("expected %s code, got %q", code, result.Errors[0].Code)
	}
	if result.RequestID == "" {
		t.Error("expected RequestID in error body")
	}
	return result
}

// ─── DescribeRegions ──────────────────────────────────────────────────────────

func TestDescribeRegions_success(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// When: DescribeRegions is called
	resp := ec2Query(t, srv, "DescribeRegions", nil)
	defer resp.Body.Close()

	// Then: 200 with at least one region
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "regionName") {
		t.Errorf("expected regionName in response body, got: %s", b)
	}
}

// ─── CreateVpc ────────────────────────────────────────────────────────────────

func TestCreateVpc_success(t *testing.T) {
	// Given: EC2 service
	srv := helpers.NewTestServer(t)

	// When: CreateVpc is called
	resp := ec2Query(t, srv, "CreateVpc", url.Values{
		"CidrBlock": []string{"10.0.0.0/16"},
	})
	defer resp.Body.Close()

	// Then: 200 with vpc element containing vpcId
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName   xml.Name `xml:"CreateVpcResponse"`
		RequestID string   `xml:"requestId"`
		Vpc       struct {
			VpcID string `xml:"vpcId"`
			CIDR  string `xml:"cidrBlock"`
			State string `xml:"state"`
		} `xml:"vpc"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v\nbody: %s", err, b)
	}
	if result.Vpc.VpcID == "" {
		t.Errorf("expected vpcId to be set, body: %s", b)
	}
	if result.Vpc.State != "available" {
		t.Errorf("expected state=available, got %q", result.Vpc.State)
	}
}

func TestCreateVpc_mainRouteTable(t *testing.T) {
	// Given: EC2 service
	srv := helpers.NewTestServer(t)

	// When: CreateVpc is called
	createResp := ec2Query(t, srv, "CreateVpc", url.Values{
		"CidrBlock": []string{"10.0.0.0/16"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	var createResult struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
			CIDR  string `xml:"cidrBlock"`
		} `xml:"vpc"`
	}
	createBody := readBody(t, createResp)
	if err := xml.Unmarshal(createBody, &createResult); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v\nbody: %s", err, createBody)
	}

	// Then: DescribeRouteTables returns the VPC's main route table
	describeResp := ec2Query(t, srv, "DescribeRouteTables", url.Values{
		"Filter.1.Name":    []string{"vpc-id"},
		"Filter.1.Value.1": []string{createResult.Vpc.VpcID},
	})
	defer describeResp.Body.Close()
	helpers.AssertStatus(t, describeResp, http.StatusOK)
	var describeResult struct {
		RouteTables []struct {
			RouteTableID string `xml:"routeTableId"`
			VpcID        string `xml:"vpcId"`
			Routes       []struct {
				DestinationCidrBlock string `xml:"destinationCidrBlock"`
				GatewayID            string `xml:"gatewayId"`
				Origin               string `xml:"origin"`
				State                string `xml:"state"`
			} `xml:"routeSet>item"`
			Associations []struct {
				AssociationID string `xml:"routeTableAssociationId"`
				RouteTableID  string `xml:"routeTableId"`
				Main          bool   `xml:"main"`
			} `xml:"associationSet>item"`
		} `xml:"routeTableSet>item"`
	}
	describeBody := readBody(t, describeResp)
	if err := xml.Unmarshal(describeBody, &describeResult); err != nil {
		t.Fatalf("unmarshal DescribeRouteTablesResponse: %v\nbody: %s", err, describeBody)
	}
	if len(describeResult.RouteTables) != 1 {
		t.Fatalf("expected one route table, got %d; body: %s", len(describeResult.RouteTables), describeBody)
	}
	rt := describeResult.RouteTables[0]
	if rt.RouteTableID == "" || !strings.HasPrefix(rt.RouteTableID, "rtb-") {
		t.Errorf("expected routeTableId starting with rtb-, got %q", rt.RouteTableID)
	}
	if rt.VpcID != createResult.Vpc.VpcID {
		t.Errorf("expected vpcId %q, got %q", createResult.Vpc.VpcID, rt.VpcID)
	}
	if len(rt.Associations) != 1 || !rt.Associations[0].Main {
		t.Fatalf("expected one main association, got %#v", rt.Associations)
	}
	if rt.Associations[0].RouteTableID != rt.RouteTableID {
		t.Errorf("expected association routeTableId %q, got %q", rt.RouteTableID, rt.Associations[0].RouteTableID)
	}
	if len(rt.Routes) != 1 {
		t.Fatalf("expected one local route, got %d", len(rt.Routes))
	}
	if rt.Routes[0].DestinationCidrBlock != createResult.Vpc.CIDR || rt.Routes[0].GatewayID != "local" || rt.Routes[0].State != "active" {
		t.Errorf("unexpected local route: %#v", rt.Routes[0])
	}
}

func TestDescribeVpnGateways_noGateways(t *testing.T) {
	// Given: EC2 service
	srv := helpers.NewTestServer(t)

	// When: VPN gateways are described for a VPC without an attached gateway
	resp := ec2Query(t, srv, "DescribeVpnGateways", url.Values{
		"Filter.1.Name":    []string{"attachment.vpc-id"},
		"Filter.1.Value.1": []string{"vpc-12345678"},
		"Filter.2.Name":    []string{"attachment.state"},
		"Filter.2.Value.1": []string{"attached"},
		"Filter.3.Name":    []string{"state"},
		"Filter.3.Value.1": []string{"available"},
	})
	defer resp.Body.Close()

	// Then: EC2 returns a well-formed empty Query response
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	type describeVpnGatewaysResponse struct {
		XMLName    xml.Name   `xml:"DescribeVpnGatewaysResponse"`
		RequestID  string     `xml:"requestId"`
		GatewaySet []struct{} `xml:"vpnGatewaySet>item"`
	}
	body := readBody(t, resp)
	var result describeVpnGatewaysResponse
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeVpnGatewaysResponse: %v\nbody: %s", err, body)
	}
	if result.XMLName.Local != "DescribeVpnGatewaysResponse" {
		t.Errorf("expected DescribeVpnGatewaysResponse root, got %s", result.XMLName.Local)
	}
	if result.RequestID == "" {
		t.Error("expected requestId in response body")
	}
	if !strings.Contains(string(body), "<vpnGatewaySet>") && !strings.Contains(string(body), "<vpnGatewaySet/>") {
		t.Fatalf("expected empty vpnGatewaySet element; body: %s", body)
	}
	if len(result.GatewaySet) != 0 {
		t.Fatalf("expected no VPN gateways, got %d; body: %s", len(result.GatewaySet), body)
	}
}

func TestVpnGatewayLifecycle_success(t *testing.T) {
	// Given: a VPC and a virtual private gateway exist
	srv := helpers.NewTestServer(t)
	vpcResp := ec2Query(t, srv, "CreateVpc", url.Values{
		"CidrBlock": []string{"10.10.0.0/16"},
	})
	defer vpcResp.Body.Close()
	helpers.AssertStatus(t, vpcResp, http.StatusOK)
	type createVpcResponse struct {
		VpcID string `xml:"vpc>vpcId"`
	}
	var createdVpc createVpcResponse
	if err := xml.Unmarshal(readBody(t, vpcResp), &createdVpc); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v", err)
	}
	if createdVpc.VpcID == "" {
		t.Fatal("expected VPC ID")
	}

	createResp := ec2Query(t, srv, "CreateVpnGateway", url.Values{
		"Type":          []string{"ipsec.1"},
		"AmazonSideAsn": []string{"65001"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	type vpnGatewayItem struct {
		VpnGatewayID  string `xml:"vpnGatewayId"`
		State         string `xml:"state"`
		Type          string `xml:"type"`
		AmazonSideAsn int64  `xml:"amazonSideAsn"`
		Attachments   []struct {
			VpcID string `xml:"vpcId"`
			State string `xml:"state"`
		} `xml:"attachments>item"`
	}
	type createVpnGatewayResponse struct {
		Gateway vpnGatewayItem `xml:"vpnGateway"`
	}
	var created createVpnGatewayResponse
	if err := xml.Unmarshal(readBody(t, createResp), &created); err != nil {
		t.Fatalf("unmarshal CreateVpnGatewayResponse: %v", err)
	}
	if !strings.HasPrefix(created.Gateway.VpnGatewayID, "vgw-") {
		t.Fatalf("expected vgw- ID, got %q", created.Gateway.VpnGatewayID)
	}
	if created.Gateway.State != "available" || created.Gateway.Type != "ipsec.1" || created.Gateway.AmazonSideAsn != 65001 {
		t.Fatalf("unexpected created gateway: %#v", created.Gateway)
	}
	if len(created.Gateway.Attachments) != 0 {
		t.Fatalf("expected no initial attachments, got %#v", created.Gateway.Attachments)
	}

	// When: the gateway is attached and described using CDK-style filters
	attachResp := ec2Query(t, srv, "AttachVpnGateway", url.Values{
		"VpnGatewayId": []string{created.Gateway.VpnGatewayID},
		"VpcId":        []string{createdVpc.VpcID},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)
	type attachVpnGatewayResponse struct {
		Attachment struct {
			VpcID string `xml:"vpcId"`
			State string `xml:"state"`
		} `xml:"attachment"`
	}
	var attached attachVpnGatewayResponse
	if err := xml.Unmarshal(readBody(t, attachResp), &attached); err != nil {
		t.Fatalf("unmarshal AttachVpnGatewayResponse: %v", err)
	}
	if attached.Attachment.VpcID != createdVpc.VpcID || attached.Attachment.State != "attached" {
		t.Fatalf("unexpected attachment: %#v", attached.Attachment)
	}

	describeResp := ec2Query(t, srv, "DescribeVpnGateways", url.Values{
		"Filter.1.Name":    []string{"attachment.vpc-id"},
		"Filter.1.Value.1": []string{createdVpc.VpcID},
		"Filter.2.Name":    []string{"attachment.state"},
		"Filter.2.Value.1": []string{"attached"},
		"Filter.3.Name":    []string{"state"},
		"Filter.3.Value.1": []string{"available"},
		"Filter.4.Name":    []string{"amazon-side-asn"},
		"Filter.4.Value.1": []string{"65001"},
	})
	defer describeResp.Body.Close()

	// Then: the attached gateway is returned with its attachment metadata
	helpers.AssertStatus(t, describeResp, http.StatusOK)
	type describeVpnGatewaysLifecycleResponse struct {
		Gateways []vpnGatewayItem `xml:"vpnGatewaySet>item"`
	}
	var described describeVpnGatewaysLifecycleResponse
	if err := xml.Unmarshal(readBody(t, describeResp), &described); err != nil {
		t.Fatalf("unmarshal DescribeVpnGatewaysResponse: %v", err)
	}
	if len(described.Gateways) != 1 {
		t.Fatalf("expected one gateway, got %d: %#v", len(described.Gateways), described.Gateways)
	}
	got := described.Gateways[0]
	if got.VpnGatewayID != created.Gateway.VpnGatewayID || got.State != "available" || got.Type != "ipsec.1" || got.AmazonSideAsn != 65001 {
		t.Fatalf("unexpected described gateway: %#v", got)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].VpcID != createdVpc.VpcID || got.Attachments[0].State != "attached" {
		t.Fatalf("unexpected described attachments: %#v", got.Attachments)
	}

	// And: after detaching and deleting, it is no longer described
	detachResp := ec2Query(t, srv, "DetachVpnGateway", url.Values{
		"VpnGatewayId": []string{created.Gateway.VpnGatewayID},
		"VpcId":        []string{createdVpc.VpcID},
	})
	defer detachResp.Body.Close()
	helpers.AssertStatus(t, detachResp, http.StatusOK)

	deleteResp := ec2Query(t, srv, "DeleteVpnGateway", url.Values{
		"VpnGatewayId": []string{created.Gateway.VpnGatewayID},
	})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)

	describeDeletedResp := ec2Query(t, srv, "DescribeVpnGateways", url.Values{
		"VpnGatewayId.1": []string{created.Gateway.VpnGatewayID},
	})
	defer describeDeletedResp.Body.Close()
	helpers.AssertStatus(t, describeDeletedResp, http.StatusOK)
	var describedDeleted describeVpnGatewaysLifecycleResponse
	if err := xml.Unmarshal(readBody(t, describeDeletedResp), &describedDeleted); err != nil {
		t.Fatalf("unmarshal DescribeVpnGatewaysResponse after delete: %v", err)
	}
	if len(describedDeleted.Gateways) != 0 {
		t.Fatalf("expected deleted gateway to be absent, got %#v", describedDeleted.Gateways)
	}
}

func TestEC2QueryError_unsupportedAction(t *testing.T) {
	// Given: EC2 service
	srv := helpers.NewTestServer(t)

	// When: an unsupported EC2 action is called
	resp := ec2Query(t, srv, "DescribeCarrierGateways", nil)
	defer resp.Body.Close()

	// Then: the 501 body uses EC2's SDK-compatible Query error envelope
	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	helpers.AssertRequestID(t, resp)
	if got := resp.Header.Get("x-emulator-unsupported"); got != "true" {
		t.Errorf("expected x-emulator-unsupported true, got %q", got)
	}
	assertEC2QueryError(t, resp, http.StatusNotImplemented, "NotImplemented")
}

func TestEC2QueryError_implementedActionErrors(t *testing.T) {
	// Given: EC2 service
	srv := helpers.NewTestServer(t)
	cases := []struct {
		name   string
		action string
		params url.Values
		code   string
	}{
		{
			name:   "missing required CreateVpc parameter",
			action: "CreateVpc",
			code:   "MissingParameter",
		},
		{
			name:   "missing required CreateVpcEndpoint parameter",
			action: "CreateVpcEndpoint",
			params: url.Values{"ServiceName": []string{"com.amazonaws.us-east-1.s3"}},
			code:   "MissingParameter",
		},
		{
			name:   "missing route table resource",
			action: "DeleteRouteTable",
			params: url.Values{"RouteTableId": []string{"rtb-doesnotexist"}},
			code:   "InvalidRouteTableID.NotFound",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: an implemented EC2 action returns an error
			resp := ec2Query(t, srv, tc.action, tc.params)
			defer resp.Body.Close()

			// Then: the error uses EC2's documented Query error envelope
			assertEC2QueryError(t, resp, http.StatusBadRequest, tc.code)
		})
	}
}

// ─── DescribeVpcs ─────────────────────────────────────────────────────────────

func TestDescribeVpcs_success(t *testing.T) {
	// Given: a VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	cr.Body.Close()

	// When: DescribeVpcs is called
	resp := ec2Query(t, srv, "DescribeVpcs", nil)
	defer resp.Body.Close()

	// Then: 200 with vpcSet containing our VPC
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "vpcId") {
		t.Errorf("expected vpcId in response, got: %s", b)
	}
}

// ─── CreateSubnet ─────────────────────────────────────────────────────────────

func TestCreateSubnet_success(t *testing.T) {
	// Given: a VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		XMLName xml.Name `xml:"CreateVpcResponse"`
		Vpc     struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	vbody := readBody(t, cr)
	xml.Unmarshal(vbody, &vpc) //nolint:errcheck

	// When: CreateSubnet is called
	resp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{vpc.Vpc.VpcID},
		"CidrBlock": []string{"10.0.1.0/24"},
	})
	defer resp.Body.Close()

	// Then: 200 with subnet element containing subnetId
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "subnetId") {
		t.Errorf("expected subnetId in response, got: %s", b)
	}
}

// ─── CreateSecurityGroup ──────────────────────────────────────────────────────

func TestCreateSecurityGroup_success(t *testing.T) {
	// Given: a VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		XMLName xml.Name `xml:"CreateVpcResponse"`
		Vpc     struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	vbody := readBody(t, cr)
	xml.Unmarshal(vbody, &vpc) //nolint:errcheck

	// When: CreateSecurityGroup is called
	resp := ec2Query(t, srv, "CreateSecurityGroup", url.Values{
		"GroupName":   []string{"test-sg"},
		"Description": []string{"test"},
		"VpcId":       []string{vpc.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 200 with groupId
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "groupId") {
		t.Errorf("expected groupId in response, got: %s", b)
	}
}

// ─── DeleteVpc ────────────────────────────────────────────────────────────────

func TestDeleteVpc_success(t *testing.T) {
	// Given: a VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		XMLName xml.Name `xml:"CreateVpcResponse"`
		Vpc     struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	vbody := readBody(t, cr)
	xml.Unmarshal(vbody, &vpc) //nolint:errcheck

	// When: DeleteVpc is called
	resp := ec2Query(t, srv, "DeleteVpc", url.Values{
		"VpcId": []string{vpc.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── RunInstances ─────────────────────────────────────────────────────────────

func TestRunInstances_success(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// When: RunInstances is called
	resp := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":      []string{"ami-12345678"},
		"InstanceType": []string{"t3.micro"},
		"MinCount":     []string{"1"},
		"MaxCount":     []string{"1"},
	})
	defer resp.Body.Close()

	// Then: 200 with instanceId starting with "i-"
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName       xml.Name `xml:"RunInstancesResponse"`
		ReservationID string   `xml:"reservationId"`
		OwnerID       string   `xml:"ownerId"`
		Instances     []struct {
			InstanceID    string `xml:"instanceId"`
			ImageID       string `xml:"imageId"`
			InstanceState struct {
				Code int    `xml:"code"`
				Name string `xml:"name"`
			} `xml:"instanceState"`
			InstanceType string `xml:"instanceType"`
		} `xml:"instancesSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal RunInstancesResponse: %v\nbody: %s", err, b)
	}
	if len(result.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(result.Instances))
	}
	inst := result.Instances[0]
	if !strings.HasPrefix(inst.InstanceID, "i-") {
		t.Errorf("expected instanceId starting with 'i-', got %q", inst.InstanceID)
	}
	if inst.ImageID != "ami-12345678" {
		t.Errorf("expected imageId=ami-12345678, got %q", inst.ImageID)
	}
	if inst.InstanceState.Name != "pending" {
		t.Errorf("expected state=pending, got %q", inst.InstanceState.Name)
	}
	if inst.InstanceType != "t3.micro" {
		t.Errorf("expected instanceType=t3.micro, got %q", inst.InstanceType)
	}
}

// ─── DescribeInstances (with instances) ───────────────────────────────────────

func TestDescribeInstances_withInstances(t *testing.T) {
	// Given: a running instance
	srv := helpers.NewTestServer(t)
	runResp := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":  []string{"ami-abcdef00"},
		"MinCount": []string{"1"},
		"MaxCount": []string{"1"},
	})
	defer runResp.Body.Close()
	helpers.AssertStatus(t, runResp, http.StatusOK)
	var runResult struct {
		Instances []struct {
			InstanceID string `xml:"instanceId"`
		} `xml:"instancesSet>item"`
	}
	xml.Unmarshal(readBody(t, runResp), &runResult) //nolint:errcheck
	instID := runResult.Instances[0].InstanceID

	// When: DescribeInstances is called
	resp := ec2Query(t, srv, "DescribeInstances", nil)
	defer resp.Body.Close()

	// Then: 200 with the instance in the reservation set
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	body := string(b)
	if !strings.Contains(body, instID) {
		t.Errorf("expected instanceId %q in DescribeInstances response, got: %s", instID, body)
	}
}

// ─── TerminateInstances ──────────────────────────────────────────────────────

func TestTerminateInstances_success(t *testing.T) {
	// Given: a launched instance
	srv := helpers.NewTestServer(t)
	runResp := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":  []string{"ami-abcdef00"},
		"MinCount": []string{"1"},
		"MaxCount": []string{"1"},
	})
	defer runResp.Body.Close()
	helpers.AssertStatus(t, runResp, http.StatusOK)
	var runResult struct {
		Instances []struct {
			InstanceID string `xml:"instanceId"`
		} `xml:"instancesSet>item"`
	}
	xml.Unmarshal(readBody(t, runResp), &runResult) //nolint:errcheck
	instID := runResult.Instances[0].InstanceID

	// When: TerminateInstances is called
	resp := ec2Query(t, srv, "TerminateInstances", url.Values{
		"InstanceId.1": []string{instID},
	})
	defer resp.Body.Close()

	// Then: 200 with previousState and currentState (shutting-down)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var termResult struct {
		XMLName   xml.Name `xml:"TerminateInstancesResponse"`
		Instances []struct {
			InstanceID    string `xml:"instanceId"`
			PreviousState struct {
				Name string `xml:"name"`
			} `xml:"previousState"`
			CurrentState struct {
				Name string `xml:"name"`
			} `xml:"currentState"`
		} `xml:"instancesSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &termResult); err != nil {
		t.Fatalf("unmarshal TerminateInstancesResponse: %v\nbody: %s", err, b)
	}
	if len(termResult.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(termResult.Instances))
	}
	item := termResult.Instances[0]
	if item.InstanceID != instID {
		t.Errorf("expected instanceId=%s, got %s", instID, item.InstanceID)
	}
	if item.CurrentState.Name != "shutting-down" {
		t.Errorf("expected currentState=shutting-down, got %q", item.CurrentState.Name)
	}
}

func TestTerminateInstances_batch(t *testing.T) {
	// Given: 3 launched instances
	srv := helpers.NewTestServer(t)
	instanceIDs := make([]string, 3)
	for i := range instanceIDs {
		runResp := ec2Query(t, srv, "RunInstances", url.Values{
			"ImageId":  []string{"ami-abcdef00"},
			"MinCount": []string{"1"},
			"MaxCount": []string{"1"},
		})
		helpers.AssertStatus(t, runResp, http.StatusOK)
		var runResult struct {
			Instances []struct {
				InstanceID string `xml:"instanceId"`
			} `xml:"instancesSet>item"`
		}
		xml.Unmarshal(readBody(t, runResp), &runResult) //nolint:errcheck
		runResp.Body.Close()
		instanceIDs[i] = runResult.Instances[0].InstanceID
	}

	// When: TerminateInstances is called with all 3 instance IDs
	resp := ec2Query(t, srv, "TerminateInstances", url.Values{
		"InstanceId.1": []string{instanceIDs[0]},
		"InstanceId.2": []string{instanceIDs[1]},
		"InstanceId.3": []string{instanceIDs[2]},
	})
	defer resp.Body.Close()

	// Then: 200 with 3 items, each shutting-down with previous state pending or running
	helpers.AssertStatus(t, resp, http.StatusOK)
	var termResult struct {
		XMLName   xml.Name `xml:"TerminateInstancesResponse"`
		Instances []struct {
			InstanceID    string `xml:"instanceId"`
			PreviousState struct {
				Code int    `xml:"code"`
				Name string `xml:"name"`
			} `xml:"previousState"`
			CurrentState struct {
				Code int    `xml:"code"`
				Name string `xml:"name"`
			} `xml:"currentState"`
		} `xml:"instancesSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &termResult); err != nil {
		t.Fatalf("unmarshal TerminateInstancesResponse: %v\nbody: %s", err, b)
	}
	if len(termResult.Instances) != 3 {
		t.Fatalf("expected 3 instances, got %d; body: %s", len(termResult.Instances), b)
	}

	returned := make(map[string]bool, 3)
	for _, item := range termResult.Instances {
		returned[item.InstanceID] = true
		if item.CurrentState.Code != 32 {
			t.Errorf("instance %s: expected currentState code=32, got %d", item.InstanceID, item.CurrentState.Code)
		}
		if item.CurrentState.Name != "shutting-down" {
			t.Errorf("instance %s: expected currentState=shutting-down, got %q", item.InstanceID, item.CurrentState.Name)
		}
		if item.PreviousState.Code != 0 && item.PreviousState.Code != 16 {
			t.Errorf("instance %s: expected previousState code=0 or 16, got %d", item.InstanceID, item.PreviousState.Code)
		}
	}
	for _, id := range instanceIDs {
		if !returned[id] {
			t.Errorf("instance %s not found in terminate response", id)
		}
	}
}

// ─── StopInstances ───────────────────────────────────────────────────────────

func TestStopInstances_success(t *testing.T) {
	// Given: a running instance (use mock clock to transition to running)
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	runResp := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":  []string{"ami-abcdef00"},
		"MinCount": []string{"1"},
		"MaxCount": []string{"1"},
	})
	defer runResp.Body.Close()
	helpers.AssertStatus(t, runResp, http.StatusOK)
	var runResult struct {
		Instances []struct {
			InstanceID string `xml:"instanceId"`
		} `xml:"instancesSet>item"`
	}
	xml.Unmarshal(readBody(t, runResp), &runResult) //nolint:errcheck
	instID := runResult.Instances[0].InstanceID

	// Advance clock to trigger pending → running
	srv.Clock.Add(1 * time.Second)

	// When: StopInstances is called
	resp := ec2Query(t, srv, "StopInstances", url.Values{
		"InstanceId.1": []string{instID},
	})
	defer resp.Body.Close()

	// Then: 200 with previousState=running and currentState=stopping
	helpers.AssertStatus(t, resp, http.StatusOK)
	var stopResult struct {
		XMLName   xml.Name `xml:"StopInstancesResponse"`
		Instances []struct {
			InstanceID    string `xml:"instanceId"`
			PreviousState struct {
				Name string `xml:"name"`
			} `xml:"previousState"`
			CurrentState struct {
				Name string `xml:"name"`
			} `xml:"currentState"`
		} `xml:"instancesSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &stopResult); err != nil {
		t.Fatalf("unmarshal StopInstancesResponse: %v\nbody: %s", err, b)
	}
	if len(stopResult.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(stopResult.Instances))
	}
	item := stopResult.Instances[0]
	if item.PreviousState.Name != "running" {
		t.Errorf("expected previousState=running, got %q", item.PreviousState.Name)
	}
	if item.CurrentState.Name != "stopping" {
		t.Errorf("expected currentState=stopping, got %q", item.CurrentState.Name)
	}
}

// ─── DescribeInstances (filter by state) ──────────────────────────────────────

func TestDescribeInstances_filterByState(t *testing.T) {
	// Given: a terminated instance (use mock clock)
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	runResp := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":  []string{"ami-abcdef00"},
		"MinCount": []string{"1"},
		"MaxCount": []string{"1"},
	})
	defer runResp.Body.Close()
	helpers.AssertStatus(t, runResp, http.StatusOK)
	var runResult struct {
		Instances []struct {
			InstanceID string `xml:"instanceId"`
		} `xml:"instancesSet>item"`
	}
	xml.Unmarshal(readBody(t, runResp), &runResult) //nolint:errcheck
	instID := runResult.Instances[0].InstanceID

	// Terminate the instance
	termResp := ec2Query(t, srv, "TerminateInstances", url.Values{
		"InstanceId.1": []string{instID},
	})
	termResp.Body.Close()
	helpers.AssertStatus(t, termResp, http.StatusOK)

	// Advance clock to complete shutting-down → terminated
	srv.Clock.Add(1 * time.Second)

	// When: DescribeInstances with filter instance-state-name=terminated
	resp := ec2Query(t, srv, "DescribeInstances", url.Values{
		"Filter.1.Name":    []string{"instance-state-name"},
		"Filter.1.Value.1": []string{"terminated"},
	})
	defer resp.Body.Close()

	// Then: 200 with the terminated instance
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	body := string(b)
	if !strings.Contains(body, instID) {
		t.Errorf("expected instanceId %q in filtered response, got: %s", instID, body)
	}
	if !strings.Contains(body, "<name>terminated</name>") {
		t.Errorf("expected terminated state in response, got: %s", body)
	}
}

// ─── AuthorizeSecurityGroupIngress ────────────────────────────────────────────

func TestAuthorizeSecurityGroupIngress_success(t *testing.T) {
	// Given: a VPC and a security group
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		XMLName xml.Name `xml:"CreateVpcResponse"`
		Vpc     struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	sgResp := ec2Query(t, srv, "CreateSecurityGroup", url.Values{
		"GroupName":        []string{"test-sg"},
		"GroupDescription": []string{"test security group"},
		"VpcId":            []string{vpc.Vpc.VpcID},
	})
	defer sgResp.Body.Close()
	helpers.AssertStatus(t, sgResp, http.StatusOK)
	var sgResult struct {
		XMLName xml.Name `xml:"CreateSecurityGroupResponse"`
		GroupID string   `xml:"groupId"`
	}
	xml.Unmarshal(readBody(t, sgResp), &sgResult) //nolint:errcheck

	// When: AuthorizeSecurityGroupIngress is called with TCP port 80
	resp := ec2Query(t, srv, "AuthorizeSecurityGroupIngress", url.Values{
		"GroupId":                           []string{sgResult.GroupID},
		"IpPermissions.1.IpProtocol":        []string{"tcp"},
		"IpPermissions.1.FromPort":          []string{"80"},
		"IpPermissions.1.ToPort":            []string{"80"},
		"IpPermissions.1.IpRanges.1.CidrIp": []string{"0.0.0.0/0"},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	body := string(b)
	if !strings.Contains(body, "<return>true</return>") {
		t.Errorf("expected <return>true</return> in response, got: %s", body)
	}
}

// ─── DescribeSecurityGroups ──────────────────────────────────────────────────

func TestDescribeSecurityGroups_success(t *testing.T) {
	// Given: a VPC and a security group
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		XMLName xml.Name `xml:"CreateVpcResponse"`
		Vpc     struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	sgResp := ec2Query(t, srv, "CreateSecurityGroup", url.Values{
		"GroupName":        []string{"describe-test-sg"},
		"GroupDescription": []string{"test security group"},
		"VpcId":            []string{vpc.Vpc.VpcID},
	})
	defer sgResp.Body.Close()
	helpers.AssertStatus(t, sgResp, http.StatusOK)
	var sgResult struct {
		XMLName xml.Name `xml:"CreateSecurityGroupResponse"`
		GroupID string   `xml:"groupId"`
	}
	xml.Unmarshal(readBody(t, sgResp), &sgResult) //nolint:errcheck

	// When: DescribeSecurityGroups is called
	resp := ec2Query(t, srv, "DescribeSecurityGroups", nil)
	defer resp.Body.Close()

	// Then: 200 with the security group, including default egress rule
	helpers.AssertStatus(t, resp, http.StatusOK)
	var descResult struct {
		XMLName xml.Name `xml:"DescribeSecurityGroupsResponse"`
		Groups  []struct {
			GroupID             string `xml:"groupId"`
			GroupName           string `xml:"groupName"`
			GroupDescription    string `xml:"groupDescription"`
			VpcID               string `xml:"vpcId"`
			IpPermissionsEgress struct {
				Items []struct {
					IpProtocol string `xml:"ipProtocol"`
					IpRanges   struct {
						Items []struct {
							CidrIp string `xml:"cidrIp"`
						} `xml:"item"`
					} `xml:"ipRanges"`
				} `xml:"item"`
			} `xml:"ipPermissionsEgress"`
		} `xml:"securityGroupInfo>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &descResult); err != nil {
		t.Fatalf("unmarshal DescribeSecurityGroupsResponse: %v\nbody: %s", err, b)
	}
	found := false
	for _, g := range descResult.Groups {
		if g.GroupID == sgResult.GroupID {
			found = true
			if g.GroupName != "describe-test-sg" {
				t.Errorf("expected groupName=describe-test-sg, got %q", g.GroupName)
			}
			if g.VpcID != vpc.Vpc.VpcID {
				t.Errorf("expected vpcId=%s, got %q", vpc.Vpc.VpcID, g.VpcID)
			}
			// Verify default egress rule
			if len(g.IpPermissionsEgress.Items) == 0 {
				t.Error("expected at least one egress rule (default allow-all)")
			} else {
				egress := g.IpPermissionsEgress.Items[0]
				if egress.IpProtocol != "-1" {
					t.Errorf("expected egress ipProtocol=-1, got %q", egress.IpProtocol)
				}
			}
			break
		}
	}
	if !found {
		t.Errorf("security group %s not found in DescribeSecurityGroups response", sgResult.GroupID)
	}
}

func TestDescribeSecurityGroups_filterByVpcId(t *testing.T) {
	// Given: two security groups in different VPCs
	srv := helpers.NewTestServer(t)
	cr1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr1.Body.Close()
	helpers.AssertStatus(t, cr1, http.StatusOK)
	var vpc1 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr1), &vpc1) //nolint:errcheck

	cr2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	defer cr2.Body.Close()
	helpers.AssertStatus(t, cr2, http.StatusOK)
	var vpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr2), &vpc2) //nolint:errcheck

	sgResp1 := ec2Query(t, srv, "CreateSecurityGroup", url.Values{
		"GroupName": []string{"sg-vpc1"}, "GroupDescription": []string{"vpc1"}, "VpcId": []string{vpc1.Vpc.VpcID},
	})
	sgResp1.Body.Close()
	sgResp2 := ec2Query(t, srv, "CreateSecurityGroup", url.Values{
		"GroupName": []string{"sg-vpc2"}, "GroupDescription": []string{"vpc2"}, "VpcId": []string{vpc2.Vpc.VpcID},
	})
	defer sgResp2.Body.Close()
	var sg2 struct {
		GroupID string `xml:"groupId"`
	}
	xml.Unmarshal(readBody(t, sgResp2), &sg2) //nolint:errcheck

	// When: DescribeSecurityGroups with filter vpc-id
	resp := ec2Query(t, srv, "DescribeSecurityGroups", url.Values{
		"Filter.1.Name":    []string{"vpc-id"},
		"Filter.1.Value.1": []string{vpc2.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: only the SG from vpc2 is returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Groups []struct {
			GroupID string `xml:"groupId"`
			VpcID   string `xml:"vpcId"`
		} `xml:"securityGroupInfo>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	for _, g := range result.Groups {
		if g.VpcID != vpc2.Vpc.VpcID {
			t.Errorf("expected only groups in vpc %s, got group %s in vpc %s", vpc2.Vpc.VpcID, g.GroupID, g.VpcID)
		}
	}
	if len(result.Groups) == 0 {
		t.Error("expected at least one security group in response")
	}
}

// ─── RevokeSecurityGroupIngress ──────────────────────────────────────────────

func TestRevokeSecurityGroupIngress_success(t *testing.T) {
	// Given: a security group with an ingress rule
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	sgResp := ec2Query(t, srv, "CreateSecurityGroup", url.Values{
		"GroupName": []string{"revoke-sg"}, "GroupDescription": []string{"test"}, "VpcId": []string{vpc.Vpc.VpcID},
	})
	defer sgResp.Body.Close()
	helpers.AssertStatus(t, sgResp, http.StatusOK)
	var sgResult struct {
		GroupID string `xml:"groupId"`
	}
	xml.Unmarshal(readBody(t, sgResp), &sgResult) //nolint:errcheck

	// Authorize TCP 443
	authResp := ec2Query(t, srv, "AuthorizeSecurityGroupIngress", url.Values{
		"GroupId":                           []string{sgResult.GroupID},
		"IpPermissions.1.IpProtocol":        []string{"tcp"},
		"IpPermissions.1.FromPort":          []string{"443"},
		"IpPermissions.1.ToPort":            []string{"443"},
		"IpPermissions.1.IpRanges.1.CidrIp": []string{"10.0.0.0/8"},
	})
	authResp.Body.Close()
	helpers.AssertStatus(t, authResp, http.StatusOK)

	// When: RevokeSecurityGroupIngress is called with the same rule
	resp := ec2Query(t, srv, "RevokeSecurityGroupIngress", url.Values{
		"GroupId":                           []string{sgResult.GroupID},
		"IpPermissions.1.IpProtocol":        []string{"tcp"},
		"IpPermissions.1.FromPort":          []string{"443"},
		"IpPermissions.1.ToPort":            []string{"443"},
		"IpPermissions.1.IpRanges.1.CidrIp": []string{"10.0.0.0/8"},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "<return>true</return>") {
		t.Errorf("expected <return>true</return> in response, got: %s", b)
	}
}

// ─── DescribeSubnets ─────────────────────────────────────────────────────────

func TestDescribeSubnets_success(t *testing.T) {
	// Given: a VPC and a subnet
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	subResp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId": []string{vpc.Vpc.VpcID}, "CidrBlock": []string{"10.0.1.0/24"},
	})
	defer subResp.Body.Close()
	helpers.AssertStatus(t, subResp, http.StatusOK)
	var subResult struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	xml.Unmarshal(readBody(t, subResp), &subResult) //nolint:errcheck

	// When: DescribeSubnets is called
	resp := ec2Query(t, srv, "DescribeSubnets", nil)
	defer resp.Body.Close()

	// Then: 200 with the subnet in the response
	helpers.AssertStatus(t, resp, http.StatusOK)
	var descResult struct {
		XMLName xml.Name `xml:"DescribeSubnetsResponse"`
		Subnets []struct {
			SubnetID         string `xml:"subnetId"`
			VpcID            string `xml:"vpcId"`
			CidrBlock        string `xml:"cidrBlock"`
			AvailabilityZone string `xml:"availabilityZone"`
			State            string `xml:"state"`
		} `xml:"subnetSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &descResult); err != nil {
		t.Fatalf("unmarshal DescribeSubnetsResponse: %v\nbody: %s", err, b)
	}
	found := false
	for _, s := range descResult.Subnets {
		if s.SubnetID == subResult.Subnet.SubnetID {
			found = true
			if s.VpcID != vpc.Vpc.VpcID {
				t.Errorf("expected vpcId=%s, got %q", vpc.Vpc.VpcID, s.VpcID)
			}
			if s.State != "available" {
				t.Errorf("expected state=available, got %q", s.State)
			}
			break
		}
	}
	if !found {
		t.Errorf("subnet %s not found in DescribeSubnets response", subResult.Subnet.SubnetID)
	}
}

func TestDescribeSubnets_filterByVpcId(t *testing.T) {
	// Given: subnets in two different VPCs
	srv := helpers.NewTestServer(t)
	cr1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr1.Body.Close()
	helpers.AssertStatus(t, cr1, http.StatusOK)
	var vpc1 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr1), &vpc1) //nolint:errcheck

	cr2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	defer cr2.Body.Close()
	helpers.AssertStatus(t, cr2, http.StatusOK)
	var vpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr2), &vpc2) //nolint:errcheck

	sub1Resp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId": []string{vpc1.Vpc.VpcID}, "CidrBlock": []string{"10.0.1.0/24"},
	})
	sub1Resp.Body.Close()
	sub2Resp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId": []string{vpc2.Vpc.VpcID}, "CidrBlock": []string{"10.1.1.0/24"},
	})
	sub2Resp.Body.Close()

	// When: DescribeSubnets filtered by vpc-id of vpc2
	resp := ec2Query(t, srv, "DescribeSubnets", url.Values{
		"Filter.1.Name":    []string{"vpc-id"},
		"Filter.1.Value.1": []string{vpc2.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: only subnets from vpc2
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Subnets []struct {
			SubnetID string `xml:"subnetId"`
			VpcID    string `xml:"vpcId"`
		} `xml:"subnetSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	for _, s := range result.Subnets {
		if s.VpcID != vpc2.Vpc.VpcID {
			t.Errorf("expected only subnets in vpc %s, got subnet %s in vpc %s", vpc2.Vpc.VpcID, s.SubnetID, s.VpcID)
		}
	}
	if len(result.Subnets) == 0 {
		t.Error("expected at least one subnet in response")
	}
}

// ─── DescribeImages ──────────────────────────────────────────────────────────

func TestDescribeImages_returnsAMIs(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// When: DescribeImages is called without filters
	resp := ec2Query(t, srv, "DescribeImages", nil)
	defer resp.Body.Close()

	// Then: 200 with at least one AMI
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName   xml.Name `xml:"DescribeImagesResponse"`
		RequestID string   `xml:"requestId"`
		Images    []struct {
			ImageID            string `xml:"imageId"`
			Name               string `xml:"name"`
			ImageState         string `xml:"imageState"`
			Architecture       string `xml:"architecture"`
			VirtualizationType string `xml:"virtualizationType"`
			IsPublic           bool   `xml:"isPublic"`
		} `xml:"imagesSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal DescribeImagesResponse: %v\nbody: %s", err, b)
	}
	if len(result.Images) < 3 {
		t.Errorf("expected at least 3 AMIs, got %d", len(result.Images))
	}
	for _, img := range result.Images {
		if img.ImageState != "available" {
			t.Errorf("expected imageState=available, got %q for %s", img.ImageState, img.ImageID)
		}
		if img.Architecture != "x86_64" {
			t.Errorf("expected architecture=x86_64, got %q for %s", img.Architecture, img.ImageID)
		}
	}
}

func TestDescribeImages_filterByID(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// When: DescribeImages is called with a specific ImageId filter
	resp := ec2Query(t, srv, "DescribeImages", url.Values{
		"ImageId.1": []string{"ami-12345678"},
	})
	defer resp.Body.Close()

	// Then: 200 with exactly one matching AMI
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Images []struct {
			ImageID string `xml:"imageId"`
			Name    string `xml:"name"`
		} `xml:"imagesSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if len(result.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(result.Images))
	}
	if result.Images[0].ImageID != "ami-12345678" {
		t.Errorf("expected imageId=ami-12345678, got %q", result.Images[0].ImageID)
	}
}

// ─── CreateKeyPair ───────────────────────────────────────────────────────────

func TestCreateKeyPair_success(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// When: CreateKeyPair is called
	resp := ec2Query(t, srv, "CreateKeyPair", url.Values{
		"KeyName": []string{"my-test-key"},
	})
	defer resp.Body.Close()

	// Then: 200 with key name, fingerprint, and key material
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName        xml.Name `xml:"CreateKeyPairResponse"`
		KeyName        string   `xml:"keyName"`
		KeyFingerprint string   `xml:"keyFingerprint"`
		KeyMaterial    string   `xml:"keyMaterial"`
		KeyPairID      string   `xml:"keyPairId"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if result.KeyName != "my-test-key" {
		t.Errorf("expected keyName=my-test-key, got %q", result.KeyName)
	}
	if result.KeyFingerprint == "" {
		t.Error("expected keyFingerprint to be set")
	}
	if result.KeyMaterial == "" {
		t.Error("expected keyMaterial to be set")
	}
	if !strings.HasPrefix(result.KeyPairID, "key-") {
		t.Errorf("expected keyPairId starting with 'key-', got %q", result.KeyPairID)
	}
}

// ─── DescribeKeyPairs ────────────────────────────────────────────────────────

func TestDescribeKeyPairs_success(t *testing.T) {
	// Given: a key pair exists
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateKeyPair", url.Values{"KeyName": []string{"describe-kp-test"}})
	cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: DescribeKeyPairs is called
	resp := ec2Query(t, srv, "DescribeKeyPairs", nil)
	defer resp.Body.Close()

	// Then: 200 with the key pair in the list
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName xml.Name `xml:"DescribeKeyPairsResponse"`
		KeySet  []struct {
			KeyName        string `xml:"keyName"`
			KeyFingerprint string `xml:"keyFingerprint"`
			KeyPairID      string `xml:"keyPairId"`
		} `xml:"keySet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	found := false
	for _, kp := range result.KeySet {
		if kp.KeyName == "describe-kp-test" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("key pair 'describe-kp-test' not found in DescribeKeyPairs response")
	}
}

// ─── DeleteKeyPair ───────────────────────────────────────────────────────────

func TestDeleteKeyPair_success(t *testing.T) {
	// Given: a key pair exists
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateKeyPair", url.Values{"KeyName": []string{"delete-kp-test"}})
	cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: DeleteKeyPair is called
	resp := ec2Query(t, srv, "DeleteKeyPair", url.Values{
		"KeyName": []string{"delete-kp-test"},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "<return>true</return>") {
		t.Errorf("expected <return>true</return> in response, got: %s", b)
	}

	// And: key pair no longer appears in DescribeKeyPairs
	descResp := ec2Query(t, srv, "DescribeKeyPairs", url.Values{
		"KeyName.1": []string{"delete-kp-test"},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		KeySet []struct {
			KeyName string `xml:"keyName"`
		} `xml:"keySet>item"`
	}
	db := readBody(t, descResp)
	xml.Unmarshal(db, &descResult) //nolint:errcheck
	if len(descResult.KeySet) != 0 {
		t.Errorf("expected 0 key pairs after delete, got %d", len(descResult.KeySet))
	}
}

// ─── CreateRouteTable ────────────────────────────────────────────────────────

func TestCreateRouteTable_success(t *testing.T) {
	// Given: a VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	// When: CreateRouteTable is called
	resp := ec2Query(t, srv, "CreateRouteTable", url.Values{
		"VpcId": []string{vpc.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 200 with route table containing a local route
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName    xml.Name `xml:"CreateRouteTableResponse"`
		RouteTable struct {
			RouteTableID string `xml:"routeTableId"`
			VpcID        string `xml:"vpcId"`
			Routes       []struct {
				DestinationCidrBlock string `xml:"destinationCidrBlock"`
				GatewayID            string `xml:"gatewayId"`
				Origin               string `xml:"origin"`
				State                string `xml:"state"`
			} `xml:"routeSet>item"`
		} `xml:"routeTable"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if !strings.HasPrefix(result.RouteTable.RouteTableID, "rtb-") {
		t.Errorf("expected routeTableId starting with 'rtb-', got %q", result.RouteTable.RouteTableID)
	}
	if result.RouteTable.VpcID != vpc.Vpc.VpcID {
		t.Errorf("expected vpcId=%s, got %q", vpc.Vpc.VpcID, result.RouteTable.VpcID)
	}
	if len(result.RouteTable.Routes) == 0 {
		t.Fatal("expected at least one route (local)")
	}
	localRoute := result.RouteTable.Routes[0]
	if localRoute.GatewayID != "local" {
		t.Errorf("expected local route gatewayId=local, got %q", localRoute.GatewayID)
	}
	if localRoute.DestinationCidrBlock != "10.0.0.0/16" {
		t.Errorf("expected local route destination=10.0.0.0/16, got %q", localRoute.DestinationCidrBlock)
	}
}

// ─── DescribeRouteTables ─────────────────────────────────────────────────────

func TestDescribeRouteTables_success(t *testing.T) {
	// Given: a route table
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	rtResp := ec2Query(t, srv, "CreateRouteTable", url.Values{"VpcId": []string{vpc.Vpc.VpcID}})
	defer rtResp.Body.Close()
	helpers.AssertStatus(t, rtResp, http.StatusOK)
	var rtResult struct {
		RouteTable struct {
			RouteTableID string `xml:"routeTableId"`
		} `xml:"routeTable"`
	}
	xml.Unmarshal(readBody(t, rtResp), &rtResult) //nolint:errcheck

	// When: DescribeRouteTables is called
	resp := ec2Query(t, srv, "DescribeRouteTables", nil)
	defer resp.Body.Close()

	// Then: 200 with the route table present
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName       xml.Name `xml:"DescribeRouteTablesResponse"`
		RouteTableSet []struct {
			RouteTableID string `xml:"routeTableId"`
			VpcID        string `xml:"vpcId"`
		} `xml:"routeTableSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	found := false
	for _, rt := range result.RouteTableSet {
		if rt.RouteTableID == rtResult.RouteTable.RouteTableID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("route table %s not found in DescribeRouteTables response", rtResult.RouteTable.RouteTableID)
	}
}

// ─── DeleteRouteTable ────────────────────────────────────────────────────────

func TestDeleteRouteTable_success(t *testing.T) {
	// Given: a route table
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	rtResp := ec2Query(t, srv, "CreateRouteTable", url.Values{"VpcId": []string{vpc.Vpc.VpcID}})
	defer rtResp.Body.Close()
	helpers.AssertStatus(t, rtResp, http.StatusOK)
	var rtResult struct {
		RouteTable struct {
			RouteTableID string `xml:"routeTableId"`
		} `xml:"routeTable"`
	}
	xml.Unmarshal(readBody(t, rtResp), &rtResult) //nolint:errcheck

	// When: DeleteRouteTable is called
	resp := ec2Query(t, srv, "DeleteRouteTable", url.Values{
		"RouteTableId": []string{rtResult.RouteTable.RouteTableID},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "<return>true</return>") {
		t.Errorf("expected <return>true</return> in response, got: %s", b)
	}
}

// ─── CreateRoute ─────────────────────────────────────────────────────────────

func TestCreateRoute_success(t *testing.T) {
	// Given: a route table and an internet gateway
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	rtResp := ec2Query(t, srv, "CreateRouteTable", url.Values{"VpcId": []string{vpc.Vpc.VpcID}})
	defer rtResp.Body.Close()
	helpers.AssertStatus(t, rtResp, http.StatusOK)
	var rtResult struct {
		RouteTable struct {
			RouteTableID string `xml:"routeTableId"`
		} `xml:"routeTable"`
	}
	xml.Unmarshal(readBody(t, rtResp), &rtResult) //nolint:errcheck

	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	helpers.AssertStatus(t, igwResp, http.StatusOK)
	var igwResult struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igwResult) //nolint:errcheck

	// When: CreateRoute is called
	resp := ec2Query(t, srv, "CreateRoute", url.Values{
		"RouteTableId":         []string{rtResult.RouteTable.RouteTableID},
		"DestinationCidrBlock": []string{"0.0.0.0/0"},
		"GatewayId":            []string{igwResult.InternetGateway.InternetGatewayID},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "<return>true</return>") {
		t.Errorf("expected <return>true</return> in response, got: %s", b)
	}

	// And: the route appears in DescribeRouteTables
	descResp := ec2Query(t, srv, "DescribeRouteTables", url.Values{
		"RouteTableId.1": []string{rtResult.RouteTable.RouteTableID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		RouteTableSet []struct {
			Routes []struct {
				DestinationCidrBlock string `xml:"destinationCidrBlock"`
				GatewayID            string `xml:"gatewayId"`
			} `xml:"routeSet>item"`
		} `xml:"routeTableSet>item"`
	}
	db := readBody(t, descResp)
	xml.Unmarshal(db, &descResult) //nolint:errcheck
	if len(descResult.RouteTableSet) == 0 {
		t.Fatal("expected at least one route table")
	}
	foundRoute := false
	for _, route := range descResult.RouteTableSet[0].Routes {
		if route.DestinationCidrBlock == "0.0.0.0/0" {
			foundRoute = true
			break
		}
	}
	if !foundRoute {
		t.Error("expected 0.0.0.0/0 route in route table after CreateRoute")
	}
}

// ─── CreateInternetGateway ───────────────────────────────────────────────────

func TestCreateInternetGateway_success(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// When: CreateInternetGateway is called
	resp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer resp.Body.Close()

	// Then: 200 with internetGatewayId starting with "igw-"
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName         xml.Name `xml:"CreateInternetGatewayResponse"`
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if !strings.HasPrefix(result.InternetGateway.InternetGatewayID, "igw-") {
		t.Errorf("expected internetGatewayId starting with 'igw-', got %q", result.InternetGateway.InternetGatewayID)
	}
}

// ─── AttachInternetGateway ───────────────────────────────────────────────────

func TestAttachInternetGateway_success(t *testing.T) {
	// Given: a VPC and an internet gateway
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	helpers.AssertStatus(t, igwResp, http.StatusOK)
	var igwResult struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igwResult) //nolint:errcheck

	// When: AttachInternetGateway is called
	resp := ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igwResult.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "<return>true</return>") {
		t.Errorf("expected <return>true</return> in response, got: %s", b)
	}

	// And: the attachment appears in DescribeInternetGateways
	descResp := ec2Query(t, srv, "DescribeInternetGateways", url.Values{
		"InternetGatewayId.1": []string{igwResult.InternetGateway.InternetGatewayID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		InternetGatewaySet []struct {
			InternetGatewayID string `xml:"internetGatewayId"`
			Attachments       []struct {
				VpcID string `xml:"vpcId"`
				State string `xml:"state"`
			} `xml:"attachmentSet>item"`
		} `xml:"internetGatewaySet>item"`
	}
	db := readBody(t, descResp)
	xml.Unmarshal(db, &descResult) //nolint:errcheck
	if len(descResult.InternetGatewaySet) == 0 {
		t.Fatal("expected at least one internet gateway")
	}
	igw := descResult.InternetGatewaySet[0]
	if len(igw.Attachments) == 0 {
		t.Fatal("expected at least one attachment")
	}
	if igw.Attachments[0].VpcID != vpc.Vpc.VpcID {
		t.Errorf("expected attachment vpcId=%s, got %q", vpc.Vpc.VpcID, igw.Attachments[0].VpcID)
	}
}

// ─── DetachInternetGateway ───────────────────────────────────────────────────

func TestDetachInternetGateway_success(t *testing.T) {
	// Given: an internet gateway attached to a VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	helpers.AssertStatus(t, igwResp, http.StatusOK)
	var igwResult struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igwResult) //nolint:errcheck

	attachResp := ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igwResult.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	})
	attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// When: DetachInternetGateway is called
	resp := ec2Query(t, srv, "DetachInternetGateway", url.Values{
		"InternetGatewayId": []string{igwResult.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "<return>true</return>") {
		t.Errorf("expected <return>true</return> in response, got: %s", b)
	}
}

// ─── DeleteInternetGateway ───────────────────────────────────────────────────

func TestDeleteInternetGateway_success(t *testing.T) {
	// Given: an unattached internet gateway
	srv := helpers.NewTestServer(t)
	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	helpers.AssertStatus(t, igwResp, http.StatusOK)
	var igwResult struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igwResult) //nolint:errcheck

	// When: DeleteInternetGateway is called
	resp := ec2Query(t, srv, "DeleteInternetGateway", url.Values{
		"InternetGatewayId": []string{igwResult.InternetGateway.InternetGatewayID},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "<return>true</return>") {
		t.Errorf("expected <return>true</return> in response, got: %s", b)
	}

	// And: the gateway is gone from DescribeInternetGateways
	descResp := ec2Query(t, srv, "DescribeInternetGateways", url.Values{
		"InternetGatewayId.1": []string{igwResult.InternetGateway.InternetGatewayID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		InternetGatewaySet []struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGatewaySet>item"`
	}
	db := readBody(t, descResp)
	xml.Unmarshal(db, &descResult) //nolint:errcheck
	if len(descResult.InternetGatewaySet) != 0 {
		t.Errorf("expected 0 internet gateways after delete, got %d", len(descResult.InternetGatewaySet))
	}
}

// ─── VPC Peering Connections ──────────────────────────────────────────────────

func TestCreateVpcPeeringConnection_success(t *testing.T) {
	// Given: two VPCs
	srv := helpers.NewTestServer(t)
	cr1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	var vpc1 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr1), &vpc1) //nolint:errcheck
	cr1.Body.Close()

	cr2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	var vpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr2), &vpc2) //nolint:errcheck
	cr2.Body.Close()

	// When: CreateVpcPeeringConnection is called
	resp := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"VpcId":     []string{vpc1.Vpc.VpcID},
		"PeerVpcId": []string{vpc2.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 200 with peering connection in pending-acceptance state
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		XMLName              xml.Name `xml:"CreateVpcPeeringConnectionResponse"`
		VpcPeeringConnection struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
			RequesterVpcInfo       struct {
				VpcId string `xml:"vpcId"`
			} `xml:"requesterVpcInfo"`
			AccepterVpcInfo struct {
				VpcId string `xml:"vpcId"`
			} `xml:"accepterVpcInfo"`
			Status struct {
				Code    string `xml:"code"`
				Message string `xml:"message"`
			} `xml:"status"`
		} `xml:"vpcPeeringConnection"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	pcx := result.VpcPeeringConnection
	if pcx.VpcPeeringConnectionId == "" {
		t.Errorf("expected peering connection ID, body: %s", b)
	}
	if !strings.HasPrefix(pcx.VpcPeeringConnectionId, "pcx-") {
		t.Errorf("expected pcx- prefix, got %q", pcx.VpcPeeringConnectionId)
	}
	if pcx.RequesterVpcInfo.VpcId != vpc1.Vpc.VpcID {
		t.Errorf("expected requester VPC %q, got %q", vpc1.Vpc.VpcID, pcx.RequesterVpcInfo.VpcId)
	}
	if pcx.AccepterVpcInfo.VpcId != vpc2.Vpc.VpcID {
		t.Errorf("expected accepter VPC %q, got %q", vpc2.Vpc.VpcID, pcx.AccepterVpcInfo.VpcId)
	}
	if pcx.Status.Code != "pending-acceptance" {
		t.Errorf("expected status pending-acceptance, got %q", pcx.Status.Code)
	}
}

func TestCreateVpcPeeringConnection_missingVpcId(t *testing.T) {
	// Given: one VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	cr.Body.Close()

	// When: CreateVpcPeeringConnection is called without VpcId
	resp := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"PeerVpcId": []string{"vpc-does-not-matter"},
	})
	defer resp.Body.Close()

	// Then: 400 MissingParameter
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "MissingParameter") {
		t.Errorf("expected MissingParameter error, got: %s", b)
	}
}

func TestCreateVpcPeeringConnection_nonexistentVpc(t *testing.T) {
	// Given: one VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck
	cr.Body.Close()

	// When: CreateVpcPeeringConnection references a non-existent peer VPC
	resp := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"VpcId":     []string{vpc.Vpc.VpcID},
		"PeerVpcId": []string{"vpc-nonexistent"},
	})
	defer resp.Body.Close()

	// Then: 400 with InvalidVpcID.NotFound
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "InvalidVpcID.NotFound") {
		t.Errorf("expected InvalidVpcID.NotFound, got: %s", b)
	}
}

func TestAcceptVpcPeeringConnection_success(t *testing.T) {
	// Given: a pending peering connection
	srv := helpers.NewTestServer(t)
	cr1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	var vpc1 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr1), &vpc1) //nolint:errcheck
	cr1.Body.Close()

	cr2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	var vpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr2), &vpc2) //nolint:errcheck
	cr2.Body.Close()

	createResp := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"VpcId":     []string{vpc1.Vpc.VpcID},
		"PeerVpcId": []string{vpc2.Vpc.VpcID},
	})
	var createResult struct {
		VpcPeeringConnection struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
		} `xml:"vpcPeeringConnection"`
	}
	xml.Unmarshal(readBody(t, createResp), &createResult) //nolint:errcheck
	createResp.Body.Close()
	pcxID := createResult.VpcPeeringConnection.VpcPeeringConnectionId

	// When: AcceptVpcPeeringConnection is called
	resp := ec2Query(t, srv, "AcceptVpcPeeringConnection", url.Values{
		"VpcPeeringConnectionId": []string{pcxID},
	})
	defer resp.Body.Close()

	// Then: 200 with status=active
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		VpcPeeringConnection struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
			Status                 struct {
				Code string `xml:"code"`
			} `xml:"status"`
		} `xml:"vpcPeeringConnection"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if result.VpcPeeringConnection.Status.Code != "active" {
		t.Errorf("expected status active, got %q", result.VpcPeeringConnection.Status.Code)
	}
}

func TestAcceptVpcPeeringConnection_notPending(t *testing.T) {
	// Given: an active peering connection (already accepted)
	srv := helpers.NewTestServer(t)
	cr1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	var vpc1 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr1), &vpc1) //nolint:errcheck
	cr1.Body.Close()

	cr2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	var vpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr2), &vpc2) //nolint:errcheck
	cr2.Body.Close()

	createResp := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"VpcId":     []string{vpc1.Vpc.VpcID},
		"PeerVpcId": []string{vpc2.Vpc.VpcID},
	})
	var createResult struct {
		VpcPeeringConnection struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
		} `xml:"vpcPeeringConnection"`
	}
	xml.Unmarshal(readBody(t, createResp), &createResult) //nolint:errcheck
	createResp.Body.Close()
	pcxID := createResult.VpcPeeringConnection.VpcPeeringConnectionId

	// Accept first time
	acceptResp := ec2Query(t, srv, "AcceptVpcPeeringConnection", url.Values{
		"VpcPeeringConnectionId": []string{pcxID},
	})
	acceptResp.Body.Close()

	// When: AcceptVpcPeeringConnection is called again
	resp := ec2Query(t, srv, "AcceptVpcPeeringConnection", url.Values{
		"VpcPeeringConnectionId": []string{pcxID},
	})
	defer resp.Body.Close()

	// Then: 400 with InvalidStateTransition
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "InvalidStateTransition") {
		t.Errorf("expected InvalidStateTransition error, got: %s", b)
	}
}

func TestDescribeVpcPeeringConnections_success(t *testing.T) {
	// Given: a peering connection
	srv := helpers.NewTestServer(t)
	cr1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	var vpc1 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr1), &vpc1) //nolint:errcheck
	cr1.Body.Close()

	cr2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	var vpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr2), &vpc2) //nolint:errcheck
	cr2.Body.Close()

	createResp := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"VpcId":     []string{vpc1.Vpc.VpcID},
		"PeerVpcId": []string{vpc2.Vpc.VpcID},
	})
	var createResult struct {
		VpcPeeringConnection struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
		} `xml:"vpcPeeringConnection"`
	}
	xml.Unmarshal(readBody(t, createResp), &createResult) //nolint:errcheck
	createResp.Body.Close()
	pcxID := createResult.VpcPeeringConnection.VpcPeeringConnectionId

	// When: DescribeVpcPeeringConnections is called
	resp := ec2Query(t, srv, "DescribeVpcPeeringConnections", nil)
	defer resp.Body.Close()

	// Then: 200 with at least one peering connection
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		VpcPeeringConnectionSet []struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
			RequesterVpcInfo       struct {
				VpcId string `xml:"vpcId"`
			} `xml:"requesterVpcInfo"`
			AccepterVpcInfo struct {
				VpcId string `xml:"vpcId"`
			} `xml:"accepterVpcInfo"`
			Status struct {
				Code string `xml:"code"`
			} `xml:"status"`
		} `xml:"vpcPeeringConnectionSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if len(result.VpcPeeringConnectionSet) == 0 {
		t.Fatalf("expected at least 1 peering connection, got 0")
	}
	found := false
	for _, pcx := range result.VpcPeeringConnectionSet {
		if pcx.VpcPeeringConnectionId == pcxID {
			found = true
			if pcx.RequesterVpcInfo.VpcId != vpc1.Vpc.VpcID {
				t.Errorf("expected requester VPC %q, got %q", vpc1.Vpc.VpcID, pcx.RequesterVpcInfo.VpcId)
			}
			if pcx.AccepterVpcInfo.VpcId != vpc2.Vpc.VpcID {
				t.Errorf("expected accepter VPC %q, got %q", vpc2.Vpc.VpcID, pcx.AccepterVpcInfo.VpcId)
			}
		}
	}
	if !found {
		t.Errorf("peering connection %q not found in response", pcxID)
	}
}

func TestDescribeVpcPeeringConnections_filterByID(t *testing.T) {
	// Given: two peering connections
	srv := helpers.NewTestServer(t)
	cr1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	var vpc1 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr1), &vpc1) //nolint:errcheck
	cr1.Body.Close()

	cr2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	var vpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr2), &vpc2) //nolint:errcheck
	cr2.Body.Close()

	cr3 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.2.0.0/16"}})
	var vpc3 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr3), &vpc3) //nolint:errcheck
	cr3.Body.Close()

	// Create two peering connections
	createResp1 := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"VpcId":     []string{vpc1.Vpc.VpcID},
		"PeerVpcId": []string{vpc2.Vpc.VpcID},
	})
	var cr1result struct {
		VpcPeeringConnection struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
		} `xml:"vpcPeeringConnection"`
	}
	xml.Unmarshal(readBody(t, createResp1), &cr1result) //nolint:errcheck
	createResp1.Body.Close()
	pcxID1 := cr1result.VpcPeeringConnection.VpcPeeringConnectionId

	createResp2 := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"VpcId":     []string{vpc2.Vpc.VpcID},
		"PeerVpcId": []string{vpc3.Vpc.VpcID},
	})
	createResp2.Body.Close()

	// When: DescribeVpcPeeringConnections with ID filter
	resp := ec2Query(t, srv, "DescribeVpcPeeringConnections", url.Values{
		"VpcPeeringConnectionId.1": []string{pcxID1},
	})
	defer resp.Body.Close()

	// Then: only the filtered peering connection is returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		VpcPeeringConnectionSet []struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
		} `xml:"vpcPeeringConnectionSet>item"`
	}
	b := readBody(t, resp)
	xml.Unmarshal(b, &result) //nolint:errcheck
	if len(result.VpcPeeringConnectionSet) != 1 {
		t.Fatalf("expected 1 peering connection, got %d: %s", len(result.VpcPeeringConnectionSet), b)
	}
	if result.VpcPeeringConnectionSet[0].VpcPeeringConnectionId != pcxID1 {
		t.Errorf("expected %q, got %q", pcxID1, result.VpcPeeringConnectionSet[0].VpcPeeringConnectionId)
	}
}

func TestDeleteVpcPeeringConnection_success(t *testing.T) {
	// Given: a peering connection
	srv := helpers.NewTestServer(t)
	cr1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	var vpc1 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr1), &vpc1) //nolint:errcheck
	cr1.Body.Close()

	cr2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	var vpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr2), &vpc2) //nolint:errcheck
	cr2.Body.Close()

	createResp := ec2Query(t, srv, "CreateVpcPeeringConnection", url.Values{
		"VpcId":     []string{vpc1.Vpc.VpcID},
		"PeerVpcId": []string{vpc2.Vpc.VpcID},
	})
	var createResult struct {
		VpcPeeringConnection struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
		} `xml:"vpcPeeringConnection"`
	}
	xml.Unmarshal(readBody(t, createResp), &createResult) //nolint:errcheck
	createResp.Body.Close()
	pcxID := createResult.VpcPeeringConnection.VpcPeeringConnectionId

	// When: DeleteVpcPeeringConnection is called
	resp := ec2Query(t, srv, "DeleteVpcPeeringConnection", url.Values{
		"VpcPeeringConnectionId": []string{pcxID},
	})
	defer resp.Body.Close()

	// Then: 200 with return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "<return>true</return>") {
		t.Errorf("expected <return>true</return>, got: %s", b)
	}

	// And: status transitions to deleted
	descResp := ec2Query(t, srv, "DescribeVpcPeeringConnections", url.Values{
		"VpcPeeringConnectionId.1": []string{pcxID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		VpcPeeringConnectionSet []struct {
			VpcPeeringConnectionId string `xml:"vpcPeeringConnectionId"`
			Status                 struct {
				Code string `xml:"code"`
			} `xml:"status"`
		} `xml:"vpcPeeringConnectionSet>item"`
	}
	db := readBody(t, descResp)
	xml.Unmarshal(db, &descResult) //nolint:errcheck
	if len(descResult.VpcPeeringConnectionSet) != 1 {
		t.Fatalf("expected 1 peering connection after delete, got %d", len(descResult.VpcPeeringConnectionSet))
	}
	if descResult.VpcPeeringConnectionSet[0].Status.Code != "deleted" {
		t.Errorf("expected status deleted, got %q", descResult.VpcPeeringConnectionSet[0].Status.Code)
	}
}

func TestDeleteVpcPeeringConnection_notFound(t *testing.T) {
	// Given: no peering connections
	srv := helpers.NewTestServer(t)

	// When: DeleteVpcPeeringConnection is called with non-existent ID
	resp := ec2Query(t, srv, "DeleteVpcPeeringConnection", url.Values{
		"VpcPeeringConnectionId": []string{"pcx-nonexistent"},
	})
	defer resp.Body.Close()

	// Then: 400
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── VPC full lifecycle ──────────────────────────────────────────────────────

func TestVpc_fullLifecycle(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// When: a VPC is created
	createResp := ec2Query(t, srv, "CreateVpc", url.Values{
		"CidrBlock": []string{"10.99.0.0/16"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	var created struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
			State string `xml:"state"`
			CIDR  string `xml:"cidrBlock"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, createResp), &created) //nolint:errcheck
	vpcID := created.Vpc.VpcID
	if vpcID == "" {
		t.Fatal("expected vpcId to be set")
	}

	// Then: it appears in DescribeVpcs
	descResp := ec2Query(t, srv, "DescribeVpcs", nil)
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		VpcSet []struct {
			VpcID string `xml:"vpcId"`
			State string `xml:"state"`
		} `xml:"vpcSet>item"`
	}
	xml.Unmarshal(readBody(t, descResp), &descResult) //nolint:errcheck
	found := false
	for _, v := range descResult.VpcSet {
		if v.VpcID == vpcID {
			found = true
			if v.State != "available" {
				t.Errorf("expected state=available, got %q", v.State)
			}
		}
	}
	if !found {
		t.Errorf("VPC %s not found in DescribeVpcs", vpcID)
	}

	// When: the VPC is deleted
	delResp := ec2Query(t, srv, "DeleteVpc", url.Values{"VpcId": []string{vpcID}})
	defer delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusOK)

	// Then: it no longer appears in DescribeVpcs
	desc2Resp := ec2Query(t, srv, "DescribeVpcs", nil)
	defer desc2Resp.Body.Close()
	helpers.AssertStatus(t, desc2Resp, http.StatusOK)
	var desc2Result struct {
		VpcSet []struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpcSet>item"`
	}
	xml.Unmarshal(readBody(t, desc2Resp), &desc2Result) //nolint:errcheck
	for _, v := range desc2Result.VpcSet {
		if v.VpcID == vpcID {
			t.Errorf("VPC %s should not appear after deletion", vpcID)
		}
	}
}

// ─── AttachInternetGateway state verification ────────────────────────────────

func TestAttachInternetGateway_stateIsAttached(t *testing.T) {
	// Given: a VPC and an internet gateway
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	helpers.AssertStatus(t, igwResp, http.StatusOK)
	var igw struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igw) //nolint:errcheck

	// When: AttachInternetGateway is called
	attachResp := ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// Then: the attachment state is "attached" (not "available")
	descResp := ec2Query(t, srv, "DescribeInternetGateways", url.Values{
		"InternetGatewayId.1": []string{igw.InternetGateway.InternetGatewayID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		InternetGatewaySet []struct {
			Attachments []struct {
				VpcID string `xml:"vpcId"`
				State string `xml:"state"`
			} `xml:"attachmentSet>item"`
		} `xml:"internetGatewaySet>item"`
	}
	xml.Unmarshal(readBody(t, descResp), &descResult) //nolint:errcheck
	if len(descResult.InternetGatewaySet) == 0 {
		t.Fatal("expected at least one internet gateway")
	}
	atts := descResult.InternetGatewaySet[0].Attachments
	if len(atts) == 0 {
		t.Fatal("expected at least one attachment")
	}
	if atts[0].State != "attached" {
		t.Errorf("expected attachment state 'attached', got %q", atts[0].State)
	}
	if atts[0].VpcID != vpc.Vpc.VpcID {
		t.Errorf("expected attachment vpcId=%s, got %q", vpc.Vpc.VpcID, atts[0].VpcID)
	}
}

func TestAttachInternetGateway_alreadyAttached(t *testing.T) {
	// Given: an IGW already attached to a VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	var igw struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igw) //nolint:errcheck

	ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	}).Body.Close()

	// When: AttachInternetGateway is called again for the same VPC
	resp := ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 400 Resource.AlreadyAssociated
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "Resource.AlreadyAssociated") {
		t.Errorf("expected Resource.AlreadyAssociated error, got: %s", b)
	}
}

func TestAttachInternetGateway_invalidVpc(t *testing.T) {
	// Given: an internet gateway but no VPC
	srv := helpers.NewTestServer(t)
	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	var igw struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igw) //nolint:errcheck

	// When: AttachInternetGateway is called with non-existent VPC
	resp := ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
		"VpcId":             []string{"vpc-nonexistent"},
	})
	defer resp.Body.Close()

	// Then: 400 InvalidVpcID.NotFound
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "InvalidVpcID.NotFound") {
		t.Errorf("expected InvalidVpcID.NotFound error, got: %s", b)
	}
}

func TestAttachInternetGateway_missingParams(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// When: AttachInternetGateway is called without required params
	resp := ec2Query(t, srv, "AttachInternetGateway", nil)
	defer resp.Body.Close()

	// Then: 400 MissingParameter
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "MissingParameter") {
		t.Errorf("expected MissingParameter error, got: %s", b)
	}
}

func TestDetachInternetGateway_notAttached(t *testing.T) {
	// Given: an IGW and VPC that are NOT attached
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	var igw struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igw) //nolint:errcheck

	// When: DetachInternetGateway is called
	resp := ec2Query(t, srv, "DetachInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 400 Gateway.NotAttached
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "Gateway.NotAttached") {
		t.Errorf("expected Gateway.NotAttached error, got: %s", b)
	}
}

func TestDeleteInternetGateway_attachedFails(t *testing.T) {
	// Given: an internet gateway attached to a VPC
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	var igw struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igw) //nolint:errcheck

	ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	}).Body.Close()

	// When: DeleteInternetGateway is called while still attached
	resp := ec2Query(t, srv, "DeleteInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
	})
	defer resp.Body.Close()

	// Then: 400 DependencyViolation
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "DependencyViolation") {
		t.Errorf("expected DependencyViolation error, got: %s", b)
	}
}

// ─── VPC + IGW full lifecycle ────────────────────────────────────────────────

func TestVpcWithIGW_fullLifecycle(t *testing.T) {
	// Given: the EC2 service
	srv := helpers.NewTestServer(t)

	// Step 1: Create a VPC
	vpcResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"172.16.0.0/16"}})
	defer vpcResp.Body.Close()
	helpers.AssertStatus(t, vpcResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, vpcResp), &vpc) //nolint:errcheck

	// Step 2: Create a subnet in the VPC
	subResp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{vpc.Vpc.VpcID},
		"CidrBlock": []string{"172.16.1.0/24"},
	})
	defer subResp.Body.Close()
	helpers.AssertStatus(t, subResp, http.StatusOK)
	var subnet struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
			VpcID    string `xml:"vpcId"`
		} `xml:"subnet"`
	}
	xml.Unmarshal(readBody(t, subResp), &subnet) //nolint:errcheck
	if subnet.Subnet.VpcID != vpc.Vpc.VpcID {
		t.Errorf("expected subnet.vpcId=%s, got %q", vpc.Vpc.VpcID, subnet.Subnet.VpcID)
	}

	// Step 3: Create and attach an internet gateway
	igwResp := ec2Query(t, srv, "CreateInternetGateway", nil)
	defer igwResp.Body.Close()
	helpers.AssertStatus(t, igwResp, http.StatusOK)
	var igw struct {
		InternetGateway struct {
			InternetGatewayID string `xml:"internetGatewayId"`
		} `xml:"internetGateway"`
	}
	xml.Unmarshal(readBody(t, igwResp), &igw) //nolint:errcheck

	attachResp := ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	})
	defer attachResp.Body.Close()
	helpers.AssertStatus(t, attachResp, http.StatusOK)

	// Verify attachment state is "attached"
	descIGW := ec2Query(t, srv, "DescribeInternetGateways", url.Values{
		"Filter.1.Name":    []string{"attachment.vpc-id"},
		"Filter.1.Value.1": []string{vpc.Vpc.VpcID},
	})
	defer descIGW.Body.Close()
	helpers.AssertStatus(t, descIGW, http.StatusOK)
	var igwDesc struct {
		InternetGatewaySet []struct {
			InternetGatewayID string `xml:"internetGatewayId"`
			Attachments       []struct {
				VpcID string `xml:"vpcId"`
				State string `xml:"state"`
			} `xml:"attachmentSet>item"`
		} `xml:"internetGatewaySet>item"`
	}
	xml.Unmarshal(readBody(t, descIGW), &igwDesc) //nolint:errcheck
	if len(igwDesc.InternetGatewaySet) != 1 {
		t.Fatalf("expected 1 IGW filtered by VPC, got %d", len(igwDesc.InternetGatewaySet))
	}
	if igwDesc.InternetGatewaySet[0].Attachments[0].State != "attached" {
		t.Errorf("expected state=attached, got %q", igwDesc.InternetGatewaySet[0].Attachments[0].State)
	}

	// Step 4: Detach the internet gateway
	detachResp := ec2Query(t, srv, "DetachInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
		"VpcId":             []string{vpc.Vpc.VpcID},
	})
	defer detachResp.Body.Close()
	helpers.AssertStatus(t, detachResp, http.StatusOK)

	// Verify attachment removed
	descIGW2 := ec2Query(t, srv, "DescribeInternetGateways", url.Values{
		"InternetGatewayId.1": []string{igw.InternetGateway.InternetGatewayID},
	})
	defer descIGW2.Body.Close()
	helpers.AssertStatus(t, descIGW2, http.StatusOK)
	var igwDesc2 struct {
		InternetGatewaySet []struct {
			Attachments []struct {
				VpcID string `xml:"vpcId"`
			} `xml:"attachmentSet>item"`
		} `xml:"internetGatewaySet>item"`
	}
	xml.Unmarshal(readBody(t, descIGW2), &igwDesc2) //nolint:errcheck
	if len(igwDesc2.InternetGatewaySet) == 1 && len(igwDesc2.InternetGatewaySet[0].Attachments) != 0 {
		t.Error("expected no attachments after detach")
	}

	// Step 5: Delete IGW, then delete subnet, then delete VPC
	delIGW := ec2Query(t, srv, "DeleteInternetGateway", url.Values{
		"InternetGatewayId": []string{igw.InternetGateway.InternetGatewayID},
	})
	delIGW.Body.Close()
	helpers.AssertStatus(t, delIGW, http.StatusOK)

	delSub := ec2Query(t, srv, "DeleteSubnet", url.Values{
		"SubnetId": []string{subnet.Subnet.SubnetID},
	})
	delSub.Body.Close()
	helpers.AssertStatus(t, delSub, http.StatusOK)

	delVPC := ec2Query(t, srv, "DeleteVpc", url.Values{
		"VpcId": []string{vpc.Vpc.VpcID},
	})
	delVPC.Body.Close()
	helpers.AssertStatus(t, delVPC, http.StatusOK)
}

// ─── Tags ─────────────────────────────────────────────────────────────────────

func TestCreateTags_andDescribeTags(t *testing.T) {
	// Given: a VPC
	srv := helpers.NewTestServer(t)
	vpcResp := ec2Query(t, srv, "CreateVpc", url.Values{
		"CidrBlock": []string{"10.0.0.0/16"},
	})
	helpers.AssertStatus(t, vpcResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, vpcResp), &vpc) //nolint:errcheck
	vpcResp.Body.Close()

	// When: CreateTags is called
	resp := ec2Query(t, srv, "CreateTags", url.Values{
		"ResourceId.1": []string{vpc.Vpc.VpcID},
		"Tag.1.Key":    []string{"Name"},
		"Tag.1.Value":  []string{"my-vpc"},
		"Tag.2.Key":    []string{"env"},
		"Tag.2.Value":  []string{"test"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeTags returns the tags
	descResp := ec2Query(t, srv, "DescribeTags", url.Values{
		"Filter.1.Name":    []string{"resource-id"},
		"Filter.1.Value.1": []string{vpc.Vpc.VpcID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	body := string(readBody(t, descResp))
	if !strings.Contains(body, "Name") || !strings.Contains(body, "my-vpc") {
		t.Errorf("expected Name=my-vpc in DescribeTags response, got: %s", body)
	}
}

func TestDeleteTags(t *testing.T) {
	// Given: a tagged VPC
	srv := helpers.NewTestServer(t)
	vpcResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	helpers.AssertStatus(t, vpcResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, vpcResp), &vpc) //nolint:errcheck
	vpcResp.Body.Close()

	createTags := ec2Query(t, srv, "CreateTags", url.Values{
		"ResourceId.1": []string{vpc.Vpc.VpcID},
		"Tag.1.Key":    []string{"Name"},
		"Tag.1.Value":  []string{"tagged"},
	})
	helpers.AssertStatus(t, createTags, http.StatusOK)
	createTags.Body.Close()

	// When: DeleteTags removes the tag
	delResp := ec2Query(t, srv, "DeleteTags", url.Values{
		"ResourceId.1": []string{vpc.Vpc.VpcID},
		"Tag.1.Key":    []string{"Name"},
	})
	defer delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusOK)

	// Then: DescribeTags shows no tags for this resource
	descResp := ec2Query(t, srv, "DescribeTags", url.Values{
		"Filter.1.Name":    []string{"resource-id"},
		"Filter.1.Value.1": []string{vpc.Vpc.VpcID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	body := string(readBody(t, descResp))
	if strings.Contains(body, "Name") {
		t.Errorf("expected no Name tag after DeleteTags, got: %s", body)
	}
}

// ─── Elastic IPs ──────────────────────────────────────────────────────────────

func TestAllocateAddress_andDescribe(t *testing.T) {
	// Given: EC2 service
	srv := helpers.NewTestServer(t)

	// When: AllocateAddress is called
	resp := ec2Query(t, srv, "AllocateAddress", url.Values{
		"Domain": []string{"vpc"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var alloc struct {
		AllocationID string `xml:"allocationId"`
		PublicIP     string `xml:"publicIp"`
		Domain       string `xml:"domain"`
	}
	xml.Unmarshal(readBody(t, resp), &alloc) //nolint:errcheck
	if !strings.HasPrefix(alloc.AllocationID, "eipalloc-") {
		t.Errorf("expected eipalloc- prefix, got %q", alloc.AllocationID)
	}
	if alloc.PublicIP == "" {
		t.Error("expected non-empty publicIp")
	}

	// Then: DescribeAddresses returns the allocation
	descResp := ec2Query(t, srv, "DescribeAddresses", url.Values{
		"AllocationId.1": []string{alloc.AllocationID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	body := string(readBody(t, descResp))
	if !strings.Contains(body, alloc.AllocationID) {
		t.Errorf("expected allocation in DescribeAddresses, got: %s", body)
	}
}

func TestReleaseAddress(t *testing.T) {
	// Given: an allocated EIP
	srv := helpers.NewTestServer(t)
	allocResp := ec2Query(t, srv, "AllocateAddress", url.Values{"Domain": []string{"vpc"}})
	helpers.AssertStatus(t, allocResp, http.StatusOK)
	var alloc struct {
		AllocationID string `xml:"allocationId"`
	}
	xml.Unmarshal(readBody(t, allocResp), &alloc) //nolint:errcheck
	allocResp.Body.Close()

	// When: ReleaseAddress is called
	resp := ec2Query(t, srv, "ReleaseAddress", url.Values{
		"AllocationId": []string{alloc.AllocationID},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeAddresses returns no results for that allocation
	descResp := ec2Query(t, srv, "DescribeAddresses", nil)
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	body := string(readBody(t, descResp))
	if strings.Contains(body, alloc.AllocationID) {
		t.Errorf("expected allocation to be removed after release, got: %s", body)
	}
}

func TestAssociateAddress_andDisassociate(t *testing.T) {
	// Given: an EIP and a VPC (to simulate an instance)
	srv := helpers.NewTestServer(t)
	allocResp := ec2Query(t, srv, "AllocateAddress", url.Values{"Domain": []string{"vpc"}})
	helpers.AssertStatus(t, allocResp, http.StatusOK)
	var alloc struct {
		AllocationID string `xml:"allocationId"`
	}
	xml.Unmarshal(readBody(t, allocResp), &alloc) //nolint:errcheck
	allocResp.Body.Close()

	// When: AssociateAddress is called
	assocResp := ec2Query(t, srv, "AssociateAddress", url.Values{
		"AllocationId": []string{alloc.AllocationID},
		"InstanceId":   []string{"i-fake123"},
	})
	defer assocResp.Body.Close()
	helpers.AssertStatus(t, assocResp, http.StatusOK)
	var assoc struct {
		AssociationID string `xml:"associationId"`
	}
	xml.Unmarshal(readBody(t, assocResp), &assoc) //nolint:errcheck
	if !strings.HasPrefix(assoc.AssociationID, "eipassoc-") {
		t.Errorf("expected eipassoc- prefix, got %q", assoc.AssociationID)
	}

	// Then: DisassociateAddress succeeds
	disassocResp := ec2Query(t, srv, "DisassociateAddress", url.Values{
		"AssociationId": []string{assoc.AssociationID},
	})
	defer disassocResp.Body.Close()
	helpers.AssertStatus(t, disassocResp, http.StatusOK)
}

// ─── NAT Gateways ─────────────────────────────────────────────────────────────

func TestNatGateway_lifecycle(t *testing.T) {
	// Given: a VPC, subnet, and EIP
	srv := helpers.NewTestServer(t)

	vpcResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	helpers.AssertStatus(t, vpcResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, vpcResp), &vpc) //nolint:errcheck
	vpcResp.Body.Close()

	subResp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{vpc.Vpc.VpcID},
		"CidrBlock": []string{"10.0.1.0/24"},
	})
	helpers.AssertStatus(t, subResp, http.StatusOK)
	var subnet struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	xml.Unmarshal(readBody(t, subResp), &subnet) //nolint:errcheck
	subResp.Body.Close()

	allocResp := ec2Query(t, srv, "AllocateAddress", url.Values{"Domain": []string{"vpc"}})
	helpers.AssertStatus(t, allocResp, http.StatusOK)
	var alloc struct {
		AllocationID string `xml:"allocationId"`
	}
	xml.Unmarshal(readBody(t, allocResp), &alloc) //nolint:errcheck
	allocResp.Body.Close()

	// When: CreateNatGateway is called
	createResp := ec2Query(t, srv, "CreateNatGateway", url.Values{
		"SubnetId":     []string{subnet.Subnet.SubnetID},
		"AllocationId": []string{alloc.AllocationID},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)

	var natgw struct {
		NatGateway struct {
			NatGatewayID string `xml:"natGatewayId"`
			SubnetID     string `xml:"subnetId"`
			VpcID        string `xml:"vpcId"`
			State        string `xml:"state"`
		} `xml:"natGateway"`
	}
	xml.Unmarshal(readBody(t, createResp), &natgw) //nolint:errcheck
	if !strings.HasPrefix(natgw.NatGateway.NatGatewayID, "nat-") {
		t.Errorf("expected nat- prefix, got %q", natgw.NatGateway.NatGatewayID)
	}
	if natgw.NatGateway.SubnetID != subnet.Subnet.SubnetID {
		t.Errorf("expected subnetId=%s, got %q", subnet.Subnet.SubnetID, natgw.NatGateway.SubnetID)
	}

	// Then: DescribeNatGateways returns it
	descResp := ec2Query(t, srv, "DescribeNatGateways", url.Values{
		"NatGatewayId.1": []string{natgw.NatGateway.NatGatewayID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	body := string(readBody(t, descResp))
	if !strings.Contains(body, natgw.NatGateway.NatGatewayID) {
		t.Errorf("expected NAT gateway in describe response")
	}

	// Finally: DeleteNatGateway
	delResp := ec2Query(t, srv, "DeleteNatGateway", url.Values{
		"NatGatewayId": []string{natgw.NatGateway.NatGatewayID},
	})
	defer delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusOK)
}

// ─── Network Interfaces ──────────────────────────────────────────────────────

func TestNetworkInterface_lifecycle(t *testing.T) {
	// Given: a VPC and subnet
	srv := helpers.NewTestServer(t)
	vpcResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	helpers.AssertStatus(t, vpcResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, vpcResp), &vpc) //nolint:errcheck
	vpcResp.Body.Close()

	subResp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{vpc.Vpc.VpcID},
		"CidrBlock": []string{"10.0.1.0/24"},
	})
	helpers.AssertStatus(t, subResp, http.StatusOK)
	var subnet struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	xml.Unmarshal(readBody(t, subResp), &subnet) //nolint:errcheck
	subResp.Body.Close()

	// When: CreateNetworkInterface
	createResp := ec2Query(t, srv, "CreateNetworkInterface", url.Values{
		"SubnetId":    []string{subnet.Subnet.SubnetID},
		"Description": []string{"test eni"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	body := string(readBody(t, createResp))
	if !strings.Contains(body, "eni-") {
		t.Fatalf("expected eni- in response, got: %s", body)
	}

	// Extract ENI ID from response
	idx := strings.Index(body, "eni-")
	eniID := body[idx:]
	if end := strings.IndexAny(eniID, "<\""); end > 0 {
		eniID = eniID[:end]
	}

	// Then: DescribeNetworkInterfaces returns it
	descResp := ec2Query(t, srv, "DescribeNetworkInterfaces", url.Values{
		"NetworkInterfaceId.1": []string{eniID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	descBody := string(readBody(t, descResp))
	if !strings.Contains(descBody, eniID) {
		t.Errorf("expected ENI in describe response")
	}

	// Finally: DeleteNetworkInterface
	delResp := ec2Query(t, srv, "DeleteNetworkInterface", url.Values{
		"NetworkInterfaceId": []string{eniID},
	})
	defer delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusOK)
}

// ─── VPC Attributes ──────────────────────────────────────────────────────────

func TestModifyVpcAttribute_andDescribe(t *testing.T) {
	// Given: a VPC
	srv := helpers.NewTestServer(t)
	vpcResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.2.0.0/16"}})
	helpers.AssertStatus(t, vpcResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, vpcResp), &vpc) //nolint:errcheck
	vpcResp.Body.Close()

	// When: ModifyVpcAttribute is called
	modResp := ec2Query(t, srv, "ModifyVpcAttribute", url.Values{
		"VpcId":                  []string{vpc.Vpc.VpcID},
		"EnableDnsSupport.Value": []string{"true"},
	})
	defer modResp.Body.Close()
	helpers.AssertStatus(t, modResp, http.StatusOK)

	// Then: DescribeVpcAttribute returns the attribute
	descResp := ec2Query(t, srv, "DescribeVpcAttribute", url.Values{
		"VpcId":     []string{vpc.Vpc.VpcID},
		"Attribute": []string{"enableDnsSupport"},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	body := string(readBody(t, descResp))
	if !strings.Contains(body, "true") {
		t.Errorf("expected enableDnsSupport=true in response, got: %s", body)
	}
}

func TestModifySubnetAttribute(t *testing.T) {
	// Given: a VPC with a subnet
	srv := helpers.NewTestServer(t)
	vpcResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.3.0.0/16"}})
	helpers.AssertStatus(t, vpcResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, vpcResp), &vpc) //nolint:errcheck
	vpcResp.Body.Close()

	subResp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{vpc.Vpc.VpcID},
		"CidrBlock": []string{"10.3.1.0/24"},
	})
	helpers.AssertStatus(t, subResp, http.StatusOK)
	var subnet struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	xml.Unmarshal(readBody(t, subResp), &subnet) //nolint:errcheck
	subResp.Body.Close()

	// When: ModifySubnetAttribute is called
	modResp := ec2Query(t, srv, "ModifySubnetAttribute", url.Values{
		"SubnetId":                  []string{subnet.Subnet.SubnetID},
		"MapPublicIpOnLaunch.Value": []string{"true"},
	})
	defer modResp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, modResp, http.StatusOK)
}

// ─── DHCP Options ─────────────────────────────────────────────────────────────

func TestDescribeDhcpOptions(t *testing.T) {
	// Given: EC2 service
	srv := helpers.NewTestServer(t)

	// When: DescribeDhcpOptions is called
	resp := ec2Query(t, srv, "DescribeDhcpOptions", nil)
	defer resp.Body.Close()

	// Then: 200 with at least the default DHCP options
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))
	if !strings.Contains(body, "domain-name") {
		t.Errorf("expected domain-name in DHCP options, got: %s", body)
	}
}

// ─── Account Attributes ──────────────────────────────────────────────────────

func TestDescribeAccountAttributes(t *testing.T) {
	// Given: EC2 service
	srv := helpers.NewTestServer(t)

	// When: DescribeAccountAttributes is called
	resp := ec2Query(t, srv, "DescribeAccountAttributes", nil)
	defer resp.Body.Close()

	// Then: 200 with supported-platforms
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))
	if !strings.Contains(body, "supported-platforms") {
		t.Errorf("expected supported-platforms in response, got: %s", body)
	}
	if !strings.Contains(body, "VPC") {
		t.Errorf("expected VPC in supported-platforms values, got: %s", body)
	}
}

func TestDescribeSubnets_filterBySubnetId(t *testing.T) {
	// Given: a VPC and a subnet
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.50.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, cr), &vpc) //nolint:errcheck

	subResp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{vpc.Vpc.VpcID},
		"CidrBlock": []string{"10.50.1.0/24"},
	})
	defer subResp.Body.Close()
	helpers.AssertStatus(t, subResp, http.StatusOK)
	var sub struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	xml.Unmarshal(readBody(t, subResp), &sub) //nolint:errcheck

	// When: DescribeSubnets is called with SubnetId.1 filter
	resp := ec2Query(t, srv, "DescribeSubnets", url.Values{
		"SubnetId.1": []string{sub.Subnet.SubnetID},
	})
	defer resp.Body.Close()

	// Then: exactly that subnet is returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Subnets []struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnetSet>item"`
	}
	b := readBody(t, resp)
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if len(result.Subnets) == 0 {
		t.Errorf("DescribeSubnets with SubnetId.1 filter: no subnets returned\nbody: %s", b)
	}
	if len(result.Subnets) > 0 && result.Subnets[0].SubnetID != sub.Subnet.SubnetID {
		t.Errorf("expected subnet %s, got %s", sub.Subnet.SubnetID, result.Subnets[0].SubnetID)
	}
}

// ─── ModifyInstanceAttribute ─────────────────────────────────────────────────

func TestModifyInstanceAttribute_instanceType(t *testing.T) {
	// Given: a running instance
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	runResp := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":      []string{"ami-12345678"},
		"MinCount":     []string{"1"},
		"MaxCount":     []string{"1"},
		"InstanceType": []string{"t3.micro"},
	})
	helpers.AssertStatus(t, runResp, http.StatusOK)
	var runResult struct {
		Instances []struct {
			InstanceID string `xml:"instanceId"`
		} `xml:"instancesSet>item"`
	}
	xml.Unmarshal(readBody(t, runResp), &runResult)
	if len(runResult.Instances) == 0 {
		t.Fatal("no instances in RunInstances response")
	}
	instanceID := runResult.Instances[0].InstanceID

	// When: ModifyInstanceAttribute changes the instance type
	resp := ec2Query(t, srv, "ModifyInstanceAttribute", url.Values{
		"InstanceId":         []string{instanceID},
		"InstanceType.Value": []string{"t3.small"},
	})
	defer resp.Body.Close()

	// Then: 200 return=true
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	var result struct {
		Return bool `xml:"return"`
	}
	xml.Unmarshal(b, &result)
	if !result.Return {
		t.Errorf("expected return=true, body: %s", b)
	}

	// And: DescribeInstances reflects the new type
	descResp := ec2Query(t, srv, "DescribeInstances", url.Values{
		"InstanceId.1": []string{instanceID},
	})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		Reservations []struct {
			Instances []struct {
				InstanceType string `xml:"instanceType"`
			} `xml:"instancesSet>item"`
		} `xml:"reservationSet>item"`
	}
	xml.Unmarshal(readBody(t, descResp), &descResult)
	if len(descResult.Reservations) == 0 || len(descResult.Reservations[0].Instances) == 0 {
		t.Fatal("no instances in DescribeInstances response")
	}
	if got := descResult.Reservations[0].Instances[0].InstanceType; got != "t3.small" {
		t.Errorf("expected instanceType=t3.small, got %q", got)
	}
}

func TestModifyInstanceAttribute_unknownInstance(t *testing.T) {
	// Given: no instances
	srv := helpers.NewTestServer(t)

	// When: ModifyInstanceAttribute is called with a bogus instance ID
	resp := ec2Query(t, srv, "ModifyInstanceAttribute", url.Values{
		"InstanceId":         []string{"i-doesnotexist"},
		"InstanceType.Value": []string{"t3.small"},
	})
	defer resp.Body.Close()

	// Then: 400 error
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── VPC Endpoints ────────────────────────────────────────────────────────────

func TestCreateVpcEndpoint_gateway(t *testing.T) {
	// Given: a VPC
	srv := helpers.NewTestServer(t)
	crResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	helpers.AssertStatus(t, crResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, crResp), &vpc)

	// When: CreateVpcEndpoint is called for S3 (gateway type)
	resp := ec2Query(t, srv, "CreateVpcEndpoint", url.Values{
		"VpcId":           []string{vpc.Vpc.VpcID},
		"ServiceName":     []string{"com.amazonaws.us-east-1.s3"},
		"VpcEndpointType": []string{"Gateway"},
	})
	defer resp.Body.Close()

	// Then: 200 with a vpce- ID
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	var result struct {
		VpcEndpoint struct {
			VpcEndpointID string `xml:"vpcEndpointId"`
			State         string `xml:"state"`
			ServiceName   string `xml:"serviceName"`
		} `xml:"vpcEndpoint"`
	}
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if result.VpcEndpoint.VpcEndpointID == "" {
		t.Error("expected vpcEndpointId to be set")
	}
	if result.VpcEndpoint.State != "available" {
		t.Errorf("expected state=available, got %q", result.VpcEndpoint.State)
	}
	if result.VpcEndpoint.ServiceName != "com.amazonaws.us-east-1.s3" {
		t.Errorf("unexpected serviceName %q", result.VpcEndpoint.ServiceName)
	}
}

func TestDescribeVpcEndpoints_returnsCreated(t *testing.T) {
	// Given: a VPC with two endpoints
	srv := helpers.NewTestServer(t)
	crResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	helpers.AssertStatus(t, crResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, crResp), &vpc)

	for _, svc := range []string{"com.amazonaws.us-east-1.s3", "com.amazonaws.us-east-1.dynamodb"} {
		r := ec2Query(t, srv, "CreateVpcEndpoint", url.Values{
			"VpcId":           []string{vpc.Vpc.VpcID},
			"ServiceName":     []string{svc},
			"VpcEndpointType": []string{"Gateway"},
		})
		helpers.AssertStatus(t, r, http.StatusOK)
		r.Body.Close()
	}

	// When: DescribeVpcEndpoints is called
	resp := ec2Query(t, srv, "DescribeVpcEndpoints", url.Values{
		"Filter.1.Name":    []string{"vpc-id"},
		"Filter.1.Value.1": []string{vpc.Vpc.VpcID},
	})
	defer resp.Body.Close()

	// Then: 200 with two endpoints
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	var result struct {
		VpcEndpoints []struct {
			VpcEndpointID string `xml:"vpcEndpointId"`
		} `xml:"vpcEndpointSet>item"`
	}
	if err := xml.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, b)
	}
	if len(result.VpcEndpoints) != 2 {
		t.Errorf("expected 2 vpc endpoints, got %d\nbody: %s", len(result.VpcEndpoints), b)
	}
}

func TestDeleteVpcEndpoints_removes(t *testing.T) {
	// Given: a VPC with an endpoint
	srv := helpers.NewTestServer(t)
	crResp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.2.0.0/16"}})
	helpers.AssertStatus(t, crResp, http.StatusOK)
	var vpc struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	xml.Unmarshal(readBody(t, crResp), &vpc)

	epResp := ec2Query(t, srv, "CreateVpcEndpoint", url.Values{
		"VpcId":           []string{vpc.Vpc.VpcID},
		"ServiceName":     []string{"com.amazonaws.us-east-1.s3"},
		"VpcEndpointType": []string{"Gateway"},
	})
	helpers.AssertStatus(t, epResp, http.StatusOK)
	var epResult struct {
		VpcEndpoint struct {
			VpcEndpointID string `xml:"vpcEndpointId"`
		} `xml:"vpcEndpoint"`
	}
	xml.Unmarshal(readBody(t, epResp), &epResult)
	endpointID := epResult.VpcEndpoint.VpcEndpointID

	// When: DeleteVpcEndpoints is called
	delResp := ec2Query(t, srv, "DeleteVpcEndpoints", url.Values{
		"VpcEndpointId.1": []string{endpointID},
	})
	defer delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusOK)

	// Then: DescribeVpcEndpoints returns zero endpoints
	descResp := ec2Query(t, srv, "DescribeVpcEndpoints", url.Values{})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var descResult struct {
		VpcEndpoints []struct {
			VpcEndpointID string `xml:"vpcEndpointId"`
		} `xml:"vpcEndpointSet>item"`
	}
	xml.Unmarshal(readBody(t, descResp), &descResult)
	if len(descResult.VpcEndpoints) != 0 {
		t.Errorf("expected 0 endpoints after delete, got %d", len(descResult.VpcEndpoints))
	}
}

// ─── VPC network strategy ─────────────────────────────────────────────────────

func TestCreateVpc_strictStrategy_rejectsOverlap(t *testing.T) {
	// Given: an EC2 server with the strict VPC network strategy and one VPC
	srv := helpers.NewTestServer(t, helpers.WithEC2VPCStrategy("strict"))
	r1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer r1.Body.Close()
	helpers.AssertStatus(t, r1, http.StatusOK)

	// When: a second VPC is created with an overlapping CIDR
	r2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/24"}})
	defer r2.Body.Close()

	// Then: 400 with InvalidVpc.Range
	helpers.AssertStatus(t, r2, http.StatusBadRequest)
	body := string(readBody(t, r2))
	if !strings.Contains(body, "InvalidVpc.Range") {
		t.Errorf("expected InvalidVpc.Range error code, got: %s", body)
	}
}

func TestCreateVpc_strictStrategy_acceptsNonOverlap(t *testing.T) {
	// Given: an EC2 server with the strict strategy and one VPC
	srv := helpers.NewTestServer(t, helpers.WithEC2VPCStrategy("strict"))
	r1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer r1.Body.Close()
	helpers.AssertStatus(t, r1, http.StatusOK)

	// When: a second VPC with a non-overlapping CIDR is created
	r2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	defer r2.Body.Close()

	// Then: 200 — strict only rejects overlap, not distinct CIDRs
	helpers.AssertStatus(t, r2, http.StatusOK)
}

func TestCreateVpc_sharedStrategy_acceptsOverlap(t *testing.T) {
	// Given: an EC2 server with the default shared strategy
	srv := helpers.NewTestServer(t)

	// When: two VPCs with the same CIDR are created
	r1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer r1.Body.Close()
	helpers.AssertStatus(t, r1, http.StatusOK)

	r2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer r2.Body.Close()

	// Then: both succeed
	helpers.AssertStatus(t, r2, http.StatusOK)
}

func TestDescribeVpcs_networkStatusTag(t *testing.T) {
	// Given: a VPC exists
	srv := helpers.NewTestServer(t)
	cr := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: DescribeVpcs is called
	resp := ec2Query(t, srv, "DescribeVpcs", nil)
	defer resp.Body.Close()

	// Then: the response contains the synthetic overcast:network-status tag
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))
	if !strings.Contains(body, "overcast:network-status") {
		t.Errorf("expected overcast:network-status tag in DescribeVpcs response, got: %s", body)
	}
}

func TestCreateVpc_remappedStrategy_overlapGetsShadowCIDR(t *testing.T) {
	// Given: remapped strategy and debug endpoints are enabled
	srv := helpers.NewTestServer(t,
		helpers.WithEC2VPCStrategy("remapped"),
		helpers.WithDebug(true),
	)

	// When: two overlapping VPCs are created
	r1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer r1.Body.Close()
	helpers.AssertStatus(t, r1, http.StatusOK)

	r2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/24"}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusOK)

	// Then: debug view shows at least one VPC with a distinct shadow Docker CIDR.
	// In test environments without Docker, NetworkStatus may be "unbacked".
	resp, err := http.Get(srv.URL + "/_debug/ec2/vpcs")
	if err != nil {
		t.Fatalf("GET /_debug/ec2/vpcs: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var vpcs []struct {
		VpcID           string `json:"vpcId"`
		CidrBlock       string `json:"cidrBlock"`
		NetworkStatus   string `json:"networkStatus"`
		DockerCidrBlock string `json:"dockerCidrBlock"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&vpcs); err != nil {
		t.Fatalf("decode /_debug/ec2/vpcs: %v", err)
	}

	foundShadow := false
	for _, v := range vpcs {
		if v.DockerCidrBlock == "" {
			continue
		}
		foundShadow = true
		if v.NetworkStatus != "remapped" && v.NetworkStatus != "unbacked" {
			t.Fatalf("expected shadow-mapped VPC %s to be remapped or unbacked, got %q", v.VpcID, v.NetworkStatus)
		}
		if v.DockerCidrBlock == v.CidrBlock {
			t.Fatalf("expected shadow-mapped VPC %s to use distinct DockerCidrBlock, got same %s", v.VpcID, v.CidrBlock)
		}
	}
	if !foundShadow {
		t.Fatal("expected at least one VPC with a non-empty DockerCidrBlock")
	}
}

func TestRunInstances_remappedStrategy_returnsUserCIDRPrivateIP(t *testing.T) {
	// Given: remapped strategy with overlapping VPCs and a subnet in the remapped VPC
	srv := helpers.NewTestServer(t, helpers.WithEC2VPCStrategy("remapped"))

	vpc1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer vpc1.Body.Close()
	helpers.AssertStatus(t, vpc1, http.StatusOK)

	vpc2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/24"}})
	defer vpc2.Body.Close()
	helpers.AssertStatus(t, vpc2, http.StatusOK)

	var createVpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	if err := xml.Unmarshal(readBody(t, vpc2), &createVpc2); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v", err)
	}

	createSubnet := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{createVpc2.Vpc.VpcID},
		"CidrBlock": []string{"10.0.0.0/28"},
	})
	defer createSubnet.Body.Close()
	helpers.AssertStatus(t, createSubnet, http.StatusOK)

	var subnetResp struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	if err := xml.Unmarshal(readBody(t, createSubnet), &subnetResp); err != nil {
		t.Fatalf("unmarshal CreateSubnetResponse: %v", err)
	}

	// When: launching an instance in that subnet
	run := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":  []string{"ami-12345678"},
		"MinCount": []string{"1"},
		"MaxCount": []string{"1"},
		"SubnetId": []string{subnetResp.Subnet.SubnetID},
	})
	defer run.Body.Close()
	helpers.AssertStatus(t, run, http.StatusOK)

	// Then: private IP surfaced by EC2 stays in user CIDR space, not shadow 100.64/10.
	body := string(readBody(t, run))
	if strings.Contains(body, "100.") {
		t.Fatalf("expected API private IP in user CIDR space, got response: %s", body)
	}
	if !strings.Contains(body, "privateIpAddress") {
		t.Fatalf("expected privateIpAddress in RunInstances response, got: %s", body)
	}
}

func TestCreateNetworkInterface_remappedStrategy_returnsUserCIDRPrivateIP(t *testing.T) {
	// Given: remapped strategy and an overlapping VPC with a subnet
	srv := helpers.NewTestServer(t, helpers.WithEC2VPCStrategy("remapped"))

	vpc1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/16"}})
	defer vpc1.Body.Close()
	helpers.AssertStatus(t, vpc1, http.StatusOK)

	vpc2 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.1.0.0/24"}})
	defer vpc2.Body.Close()
	helpers.AssertStatus(t, vpc2, http.StatusOK)

	var createVpc2 struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	if err := xml.Unmarshal(readBody(t, vpc2), &createVpc2); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v", err)
	}

	createSubnet := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{createVpc2.Vpc.VpcID},
		"CidrBlock": []string{"10.1.0.0/28"},
	})
	defer createSubnet.Body.Close()
	helpers.AssertStatus(t, createSubnet, http.StatusOK)

	var subnetResp struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	if err := xml.Unmarshal(readBody(t, createSubnet), &subnetResp); err != nil {
		t.Fatalf("unmarshal CreateSubnetResponse: %v", err)
	}

	// When: creating a network interface in that subnet
	eni := ec2Query(t, srv, "CreateNetworkInterface", url.Values{
		"SubnetId": []string{subnetResp.Subnet.SubnetID},
	})
	defer eni.Body.Close()
	helpers.AssertStatus(t, eni, http.StatusOK)

	// Then: privateIpAddress is translated into user CIDR space.
	body := string(readBody(t, eni))
	if strings.Contains(body, "100.") {
		t.Fatalf("expected API private IP in user CIDR space, got response: %s", body)
	}
	if !strings.Contains(body, "privateIpAddress") {
		t.Fatalf("expected privateIpAddress in CreateNetworkInterface response, got: %s", body)
	}
}

// ─── VPC network strategy: CreateTime ───────────────────────────────────────

func TestCreateVpc_setsCreateTime(t *testing.T) {
	// Given: debug endpoints enabled
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// When: a VPC is created
	r1 := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.0.0.0/16"}})
	defer r1.Body.Close()
	helpers.AssertStatus(t, r1, http.StatusOK)

	// Then: the debug endpoint shows a non-zero CreateTime
	resp, err := http.Get(srv.URL + "/_debug/ec2/vpcs")
	if err != nil {
		t.Fatalf("GET /_debug/ec2/vpcs: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var vpcs []struct {
		VpcID      string `json:"vpcId"`
		CreateTime int64  `json:"createTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&vpcs); err != nil {
		t.Fatalf("decode /_debug/ec2/vpcs: %v", err)
	}
	if len(vpcs) != 1 {
		t.Fatalf("expected 1 VPC in debug output, got %d", len(vpcs))
	}
	if vpcs[0].CreateTime == 0 {
		t.Fatal("expected non-zero CreateTime on VPC")
	}
}

// ─── VPC network strategy: RunInstances NetworkStatus guard ─────────────────

func TestRunInstances_strictStrategy_nonConflictVpc_succeeds(t *testing.T) {
	// Given: strict strategy with a non-conflict VPC and subnet
	srv := helpers.NewTestServer(t, helpers.WithEC2VPCStrategy("strict"))
	createVPCAndSubnet := func() (vpcID, subnetID string) {
		r := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.2.0.0/16"}})
		defer r.Body.Close()
		helpers.AssertStatus(t, r, http.StatusOK)
		var cr struct {
			Vpc struct {
				VpcID string `xml:"vpcId"`
			} `xml:"vpc"`
		}
		if err := xml.Unmarshal(readBody(t, r), &cr); err != nil {
			t.Fatalf("unmarshal CreateVpcResponse: %v", err)
		}
		vpcID = cr.Vpc.VpcID

		sr := ec2Query(t, srv, "CreateSubnet", url.Values{
			"VpcId":     []string{vpcID},
			"CidrBlock": []string{"10.2.0.0/28"},
		})
		defer sr.Body.Close()
		helpers.AssertStatus(t, sr, http.StatusOK)
		var subR struct {
			Subnet struct {
				SubnetID string `xml:"subnetId"`
			} `xml:"subnet"`
		}
		if err := xml.Unmarshal(readBody(t, sr), &subR); err != nil {
			t.Fatalf("unmarshal CreateSubnetResponse: %v", err)
		}
		subnetID = subR.Subnet.SubnetID
		return
	}
	vpcID, subnetID := createVPCAndSubnet()
	_ = vpcID

	// When: RunInstances in that subnet
	run := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":  []string{"ami-12345678"},
		"MinCount": []string{"1"},
		"MaxCount": []string{"1"},
		"SubnetId": []string{subnetID},
	})
	defer run.Body.Close()

	// Then: succeeds (non-conflict VPC)
	helpers.AssertStatus(t, run, http.StatusOK)
}

func TestRunInstances_strictStrategy_conflictVpc_fails(t *testing.T) {
	// Given: strict strategy, a VPC+subnet, then mark VPC as conflict via store
	srv := helpers.NewTestServer(t, helpers.WithEC2VPCStrategy("strict"))

	r := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.3.0.0/16"}})
	defer r.Body.Close()
	helpers.AssertStatus(t, r, http.StatusOK)
	var cr struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	if err := xml.Unmarshal(readBody(t, r), &cr); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v", err)
	}

	sr := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{cr.Vpc.VpcID},
		"CidrBlock": []string{"10.3.0.0/28"},
	})
	defer sr.Body.Close()
	helpers.AssertStatus(t, sr, http.StatusOK)
	var subR struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	if err := xml.Unmarshal(readBody(t, sr), &subR); err != nil {
		t.Fatalf("unmarshal CreateSubnetResponse: %v", err)
	}

	// Mark the VPC as conflict directly in the store.
	raw, ok, err := srv.Store.Get(context.Background(), "ec2:vpcs", "us-east-1/"+cr.Vpc.VpcID)
	if err != nil || !ok {
		t.Fatalf("get VPC from store: err=%v, ok=%v", err, ok)
	}
	raw = strings.Replace(raw, `"NetworkStatus":"unbacked"`, `"NetworkStatus":"conflict"`, 1)
	if err := srv.Store.Set(context.Background(), "ec2:vpcs", "us-east-1/"+cr.Vpc.VpcID, raw); err != nil {
		t.Fatalf("set VPC in store: %v", err)
	}

	// When: RunInstances in the conflict VPC's subnet
	run := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":  []string{"ami-12345678"},
		"MinCount": []string{"1"},
		"MaxCount": []string{"1"},
		"SubnetId": []string{subR.Subnet.SubnetID},
	})
	defer run.Body.Close()

	// Then: fails with InvalidVpc.NetworkStatus
	helpers.AssertStatus(t, run, http.StatusBadRequest)
	body := string(readBody(t, run))
	if !strings.Contains(body, "InvalidVpc.NetworkStatus") {
		t.Errorf("expected InvalidVpc.NetworkStatus error, got: %s", body)
	}
	if !strings.Contains(body, "conflict") {
		t.Errorf("expected network status conflict in error message, got: %s", body)
	}
}

// ─── VPC network strategy: CreateNetworkInterface NetworkStatus guard ───────

func TestCreateNetworkInterface_strictStrategy_nonConflictVpc_succeeds(t *testing.T) {
	// Given: strict strategy with a non-conflict VPC and subnet
	srv := helpers.NewTestServer(t, helpers.WithEC2VPCStrategy("strict"))

	r := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.4.0.0/16"}})
	defer r.Body.Close()
	helpers.AssertStatus(t, r, http.StatusOK)
	var cr struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	if err := xml.Unmarshal(readBody(t, r), &cr); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v", err)
	}

	sr := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{cr.Vpc.VpcID},
		"CidrBlock": []string{"10.4.0.0/28"},
	})
	defer sr.Body.Close()
	helpers.AssertStatus(t, sr, http.StatusOK)
	var subR struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	if err := xml.Unmarshal(readBody(t, sr), &subR); err != nil {
		t.Fatalf("unmarshal CreateSubnetResponse: %v", err)
	}

	// When: creating a network interface in that subnet
	eni := ec2Query(t, srv, "CreateNetworkInterface", url.Values{
		"SubnetId": []string{subR.Subnet.SubnetID},
	})
	defer eni.Body.Close()

	// Then: succeeds (non-conflict VPC)
	helpers.AssertStatus(t, eni, http.StatusOK)
}

func TestCreateNetworkInterface_strictStrategy_conflictVpc_fails(t *testing.T) {
	// Given: strict strategy, a VPC+subnet, then mark VPC as conflict via store
	srv := helpers.NewTestServer(t, helpers.WithEC2VPCStrategy("strict"))

	r := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.5.0.0/16"}})
	defer r.Body.Close()
	helpers.AssertStatus(t, r, http.StatusOK)
	var cr struct {
		Vpc struct {
			VpcID string `xml:"vpcId"`
		} `xml:"vpc"`
	}
	if err := xml.Unmarshal(readBody(t, r), &cr); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v", err)
	}

	sr := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{cr.Vpc.VpcID},
		"CidrBlock": []string{"10.5.0.0/28"},
	})
	defer sr.Body.Close()
	helpers.AssertStatus(t, sr, http.StatusOK)
	var subR struct {
		Subnet struct {
			SubnetID string `xml:"subnetId"`
		} `xml:"subnet"`
	}
	if err := xml.Unmarshal(readBody(t, sr), &subR); err != nil {
		t.Fatalf("unmarshal CreateSubnetResponse: %v", err)
	}

	// Mark the VPC as conflict directly in the store.
	raw, ok, err := srv.Store.Get(context.Background(), "ec2:vpcs", "us-east-1/"+cr.Vpc.VpcID)
	if err != nil || !ok {
		t.Fatalf("get VPC from store: err=%v, ok=%v", err, ok)
	}
	raw = strings.Replace(raw, `"NetworkStatus":"unbacked"`, `"NetworkStatus":"conflict"`, 1)
	if err := srv.Store.Set(context.Background(), "ec2:vpcs", "us-east-1/"+cr.Vpc.VpcID, raw); err != nil {
		t.Fatalf("set VPC in store: %v", err)
	}

	// When: creating a network interface in the conflict VPC's subnet
	eni := ec2Query(t, srv, "CreateNetworkInterface", url.Values{
		"SubnetId": []string{subR.Subnet.SubnetID},
	})
	defer eni.Body.Close()

	// Then: fails with InvalidVpc.NetworkStatus
	helpers.AssertStatus(t, eni, http.StatusBadRequest)
	body := string(readBody(t, eni))
	if !strings.Contains(body, "InvalidVpc.NetworkStatus") {
		t.Errorf("expected InvalidVpc.NetworkStatus error, got: %s", body)
	}
	if !strings.Contains(body, "conflict") {
		t.Errorf("expected network status conflict in error message, got: %s", body)
	}
}

package ec2_test

// ec2_tagspecification_test.go — create-time TagSpecification.N, over the wire.
//
// This is the shape a CDK deployment sends: every resource CDK creates carries
// its tags on the create call itself, never as a follow-up CreateTags. The
// per-operation unit table in internal/services/ec2/tag_validation_test.go
// invokes each legacy handler function directly, so it stayed green while the
// typed operation registry — which is what an SDK request actually reaches
// through Service.DispatchQuery — dropped the tags on the floor for six of
// these operations. The test that catches that has to go through the router.
//
// Both halves of the contract are asserted per operation: the create response
// echoes the tags in its own `tagSet`, and DescribeTags finds them afterwards.
// The first without the second is what CreateKeyPair used to do.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// tagSpecParams builds one TagSpecification.N entry carrying a single tag.
func tagSpecParams(resourceType, key, value string) url.Values {
	return url.Values{
		"TagSpecification.1.ResourceType": {resourceType},
		"TagSpecification.1.Tag.1.Key":    {key},
		"TagSpecification.1.Tag.1.Value":  {value},
	}
}

// xmlFieldOf pulls one flat <tag>value</tag> element's content out of a
// response body — enough to recover the minted resource ID a create answers
// with, without decoding the whole document for a single field.
func xmlFieldOf(t *testing.T, body, tag string) string {
	t.Helper()
	m := regexp.MustCompile(`<` + tag + `>([^<]*)</` + tag + `>`).FindStringSubmatch(body)
	if len(m) < 2 {
		t.Fatalf("no <%s> in response: %s", tag, body)
	}
	return m[1]
}

// createTagCase is one create operation, the resource type its
// TagSpecification names, and how to reach it.
type createTagCase struct {
	// action is the EC2 Query action name.
	action string
	// resourceType is the TagSpecification.N.ResourceType AWS gives the
	// resource this call creates.
	resourceType string
	// setup creates whatever the call needs to exist first and returns the
	// request parameters, less the tag specification.
	setup func(t *testing.T, srv *helpers.TestServer) url.Values
	// idField is the response element carrying the created resource's ID.
	idField string
	// tagResourceID, if set, overrides idField as the identifier the tag
	// store keys on — CreateKeyPair tags a key by its name, matching
	// DeleteKeyPair's tag cleanup.
	tagResourceID func(params url.Values) string
}

func createTagCases() []createTagCase {
	return []createTagCase{
		{
			action:       "CreateVpc",
			resourceType: "vpc",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"CidrBlock": {"10.60.0.0/16"}}
			},
			idField: "vpcId",
		},
		{
			action:       "CreateSubnet",
			resourceType: "subnet",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"VpcId": {createVPCForTags(t, srv)}, "CidrBlock": {"10.60.1.0/24"}}
			},
			idField: "subnetId",
		},
		{
			action:       "CreateSecurityGroup",
			resourceType: "security-group",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{
					"GroupName":        {"tagspec-sg"},
					"GroupDescription": {"tag specification"},
					"VpcId":            {createVPCForTags(t, srv)},
				}
			},
			idField: "groupId",
		},
		{
			action:       "CreateInternetGateway",
			resourceType: "internet-gateway",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{}
			},
			idField: "internetGatewayId",
		},
		{
			action:       "CreateRouteTable",
			resourceType: "route-table",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"VpcId": {createVPCForTags(t, srv)}}
			},
			idField: "routeTableId",
		},
		{
			action:       "CreateNetworkInterface",
			resourceType: "network-interface",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"SubnetId": {createSubnetForTags(t, srv)}}
			},
			idField: "networkInterfaceId",
		},
		{
			action:       "CreateKeyPair",
			resourceType: "key-pair",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"KeyName": {"tagspec-key"}}
			},
			idField:       "keyPairId",
			tagResourceID: func(params url.Values) string { return params.Get("KeyName") },
		},
		{
			action:       "RunInstances",
			resourceType: "instance",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"ImageId": {"ami-12345678"}, "MinCount": {"1"}, "MaxCount": {"1"}}
			},
			idField: "instanceId",
		},
		{
			action:       "CreateVpcEndpoint",
			resourceType: "vpc-endpoint",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{
					"VpcId":       {createVPCForTags(t, srv)},
					"ServiceName": {"com.amazonaws.us-east-1.s3"},
				}
			},
			idField: "vpcEndpointId",
		},
		{
			action:       "CreateVpcPeeringConnection",
			resourceType: "vpc-peering-connection",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"VpcId": {createVPCForTags(t, srv)}, "PeerVpcId": {createVPCForTags(t, srv)}}
			},
			idField: "vpcPeeringConnectionId",
		},
		{
			action:       "CreateNatGateway",
			resourceType: "natgateway",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"SubnetId": {createSubnetForTags(t, srv)}}
			},
			idField: "natGatewayId",
		},
		{
			action:       "CreateVpnGateway",
			resourceType: "vpn-gateway",
			setup: func(t *testing.T, srv *helpers.TestServer) url.Values {
				return url.Values{"Type": {"ipsec.1"}}
			},
			idField: "vpnGatewayId",
		},
	}
}

// createVPCForTags creates a VPC over the wire and returns its ID.
func createVPCForTags(t *testing.T, srv *helpers.TestServer) string {
	t.Helper()
	resp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": {"10.61.0.0/16"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	return xmlFieldOf(t, string(readBody(t, resp)), "vpcId")
}

// createSubnetForTags creates a VPC and a subnet in it, returning the subnet ID.
func createSubnetForTags(t *testing.T, srv *helpers.TestServer) string {
	t.Helper()
	resp := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     {createVPCForTags(t, srv)},
		"CidrBlock": {"10.61.1.0/24"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	return xmlFieldOf(t, string(readBody(t, resp)), "subnetId")
}

// describeTagsFor returns the value DescribeTags reports for key on
// resourceID, or "" if it reports none.
func describeTagsFor(t *testing.T, srv *helpers.TestServer, resourceID, key string) string {
	t.Helper()
	resp := ec2Query(t, srv, "DescribeTags", url.Values{
		"Filter.1.Name":    {"resource-id"},
		"Filter.1.Value.1": {resourceID},
		"Filter.2.Name":    {"key"},
		"Filter.2.Value.1": {key},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var result struct {
		XMLName xml.Name `xml:"DescribeTagsResponse"`
		Tags    []struct {
			ResourceID string `xml:"resourceId"`
			Key        string `xml:"key"`
			Value      string `xml:"value"`
		} `xml:"tagSet>item"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeTagsResponse: %v\nbody: %s", err, body)
	}
	for _, tag := range result.Tags {
		if tag.ResourceID == resourceID && tag.Key == key {
			return tag.Value
		}
	}
	return ""
}

// TestCreate_tagSpecificationSurvivesTheWire is the regression gate for the
// typed-dispatch flip: a create carrying TagSpecification.N must answer with
// the tags in its own tagSet and leave them where DescribeTags can find them.
//
// Six of these operations — CreateVpc, CreateSubnet, CreateSecurityGroup,
// CreateInternetGateway, CreateRouteTable and CreateNetworkInterface — dropped
// both halves once their typed twin started answering the request, because
// their typed request struct never declared the field at all.
func TestCreate_tagSpecificationSurvivesTheWire(t *testing.T) {
	for _, tc := range createTagCases() {
		t.Run(tc.action, func(t *testing.T) {
			// Given: a create call carrying one create-time tag
			srv := helpers.NewTestServer(t)
			params := tc.setup(t, srv)
			for k, v := range tagSpecParams(tc.resourceType, "Name", "tagged-by-create") {
				params[k] = v
			}

			// When: it is sent over the wire
			resp := ec2Query(t, srv, tc.action, params)
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusOK)
			body := string(readBody(t, resp))

			// Then: the response embeds the tag in its own tagSet
			if !strings.Contains(body, "<key>Name</key>") || !strings.Contains(body, "<value>tagged-by-create</value>") {
				t.Errorf("%s response has no tagSet carrying Name=tagged-by-create:\n%s", tc.action, body)
			}

			// And: DescribeTags finds it on the created resource
			resourceID := xmlFieldOf(t, body, tc.idField)
			if tc.tagResourceID != nil {
				resourceID = tc.tagResourceID(params)
			}
			if got := describeTagsFor(t, srv, resourceID, "Name"); got != "tagged-by-create" {
				t.Errorf("DescribeTags for %s reports Name=%q, want %q — create-time tags were not persisted",
					resourceID, got, "tagged-by-create")
			}
		})
	}
}

// TestCreate_invalidTagSpecificationRefusesTheCall is the other half of the
// contract: AWS refuses the whole call over a tag it would not accept rather
// than creating the resource and dropping the tag. A typed body that ignores
// TagSpecification.N answers 200 and creates the resource, which is the
// failure this catches.
func TestCreate_invalidTagSpecificationRefusesTheCall(t *testing.T) {
	for _, tc := range createTagCases() {
		t.Run(tc.action, func(t *testing.T) {
			// Given: a create call whose create-time tag uses the reserved
			// aws: prefix
			srv := helpers.NewTestServer(t)
			params := tc.setup(t, srv)
			for k, v := range tagSpecParams(tc.resourceType, "aws:reserved", "x") {
				params[k] = v
			}

			// When: it is sent over the wire
			resp := ec2Query(t, srv, tc.action, params)
			defer resp.Body.Close()

			// Then: it is refused with the error the legacy path gave
			assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidParameterValue")
		})
	}
}

package cloudformation_test

// Coverage for #529: AWS::EC2::VPC's EnableDnsSupport/EnableDnsHostnames and
// AWS::EC2::Subnet's MapPublicIpOnLaunch are properties CDK always sets (a
// CDK Vpc construct sets both DNS attributes on every VPC it synthesizes, and
// MapPublicIpOnLaunch on every public subnet), but the CloudFormation
// provisioner never threaded them into the corresponding ModifyVpcAttribute /
// ModifySubnetAttribute calls, so DescribeVpcAttribute and DescribeSubnets
// reported the emulator's own CreateVpc/CreateSubnet defaults instead of what
// the template asked for.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

type xmlBoolAttribute struct {
	Value bool `xml:"value"`
}

type describeVpcAttributeResult struct {
	XMLName            xml.Name          `xml:"DescribeVpcAttributeResponse"`
	VpcID              string            `xml:"vpcId"`
	EnableDnsSupport   *xmlBoolAttribute `xml:"enableDnsSupport"`
	EnableDnsHostnames *xmlBoolAttribute `xml:"enableDnsHostnames"`
}

// describeVpcDNSAttribute calls DescribeVpcAttribute for one attribute and
// returns its boolean value.
func describeVpcDNSAttribute(t *testing.T, srv *helpers.TestServer, vpcID, attribute string) bool {
	t.Helper()
	resp := ec2Query(t, srv, "DescribeVpcAttribute", url.Values{
		"VpcId":     {vpcID},
		"Attribute": {attribute},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeVpcAttribute(%s) status = %d", attribute, resp.StatusCode)
	}
	var result describeVpcAttributeResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode DescribeVpcAttributeResponse: %v", err)
	}
	switch attribute {
	case "enableDnsSupport":
		if result.EnableDnsSupport == nil {
			t.Fatalf("DescribeVpcAttribute(enableDnsSupport) returned no value")
		}
		return result.EnableDnsSupport.Value
	case "enableDnsHostnames":
		if result.EnableDnsHostnames == nil {
			t.Fatalf("DescribeVpcAttribute(enableDnsHostnames) returned no value")
		}
		return result.EnableDnsHostnames.Value
	default:
		t.Fatalf("unsupported attribute %q", attribute)
		return false
	}
}

type describedSubnet struct {
	SubnetID            string `xml:"subnetId"`
	MapPublicIPOnLaunch bool   `xml:"mapPublicIpOnLaunch"`
}

func describeSubnetByID(t *testing.T, srv *helpers.TestServer, subnetID string) describedSubnet {
	t.Helper()
	resp := ec2Query(t, srv, "DescribeSubnets", url.Values{
		"SubnetId.1": {subnetID},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeSubnets status = %d", resp.StatusCode)
	}
	var result struct {
		Subnets []describedSubnet `xml:"subnetSet>item"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode DescribeSubnetsResponse: %v", err)
	}
	if len(result.Subnets) != 1 {
		t.Fatalf("DescribeSubnets returned %d subnets, want 1", len(result.Subnets))
	}
	return result.Subnets[0]
}

// TestCreateStack_VpcDnsAttributesAndMapPublicIp provisions a CDK-shaped VPC
// (DNS support off, DNS hostnames on — CDK's actual defaults for a Vpc
// construct with natGateways: 0) and a subnet with MapPublicIpOnLaunch set,
// then asserts the template's values, not the emulator's own CreateVpc /
// CreateSubnet defaults, come back on describe.
func TestCreateStack_VpcDnsAttributesAndMapPublicIp(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "vpc-dns-attributes-stack"
	const template = `{
  "Resources": {
    "TestVPC": {
      "Type": "AWS::EC2::VPC",
      "Properties": {
        "CidrBlock": "10.30.0.0/16",
        "EnableDnsSupport": false,
        "EnableDnsHostnames": true
      }
    },
    "TestSubnet": {
      "Type": "AWS::EC2::Subnet",
      "Properties": {
        "VpcId": { "Ref": "TestVPC" },
        "CidrBlock": "10.30.0.0/24",
        "AvailabilityZone": "us-east-1a",
        "MapPublicIpOnLaunch": true
      }
    }
  }
}`

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	vpcID := stackResourcePhysicalID(t, srv, stackName, "TestVPC")
	subnetID := stackResourcePhysicalID(t, srv, stackName, "TestSubnet")

	if got := describeVpcDNSAttribute(t, srv, vpcID, "enableDnsSupport"); got != false {
		t.Errorf("enableDnsSupport = %v, want false", got)
	}
	if got := describeVpcDNSAttribute(t, srv, vpcID, "enableDnsHostnames"); got != true {
		t.Errorf("enableDnsHostnames = %v, want true", got)
	}
	if sub := describeSubnetByID(t, srv, subnetID); !sub.MapPublicIPOnLaunch {
		t.Errorf("mapPublicIpOnLaunch = %v, want true", sub.MapPublicIPOnLaunch)
	}
}

// TestUpdateStack_VpcDnsAttributesAndMapPublicIp flips all three properties
// on an UpdateStack and asserts both that the new values land and that the
// update happened in place — CloudFormation's own VPC/Subnet resource
// providers do not replace on a DNS-attribute-only or
// MapPublicIpOnLaunch-only change.
func TestUpdateStack_VpcDnsAttributesAndMapPublicIp(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "vpc-dns-attributes-update-stack"
	const initialTemplate = `{
  "Resources": {
    "TestVPC": {
      "Type": "AWS::EC2::VPC",
      "Properties": {
        "CidrBlock": "10.31.0.0/16",
        "EnableDnsSupport": true,
        "EnableDnsHostnames": false
      }
    },
    "TestSubnet": {
      "Type": "AWS::EC2::Subnet",
      "Properties": {
        "VpcId": { "Ref": "TestVPC" },
        "CidrBlock": "10.31.0.0/24",
        "AvailabilityZone": "us-east-1a",
        "MapPublicIpOnLaunch": false
      }
    }
  }
}`
	const updatedTemplate = `{
  "Resources": {
    "TestVPC": {
      "Type": "AWS::EC2::VPC",
      "Properties": {
        "CidrBlock": "10.31.0.0/16",
        "EnableDnsSupport": false,
        "EnableDnsHostnames": true
      }
    },
    "TestSubnet": {
      "Type": "AWS::EC2::Subnet",
      "Properties": {
        "VpcId": { "Ref": "TestVPC" },
        "CidrBlock": "10.31.0.0/24",
        "AvailabilityZone": "us-east-1a",
        "MapPublicIpOnLaunch": true
      }
    }
  }
}`

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initialTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	vpcIDBefore := stackResourcePhysicalID(t, srv, stackName, "TestVPC")
	subnetIDBefore := stackResourcePhysicalID(t, srv, stackName, "TestSubnet")

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updatedTemplate},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	vpcIDAfter := stackResourcePhysicalID(t, srv, stackName, "TestVPC")
	subnetIDAfter := stackResourcePhysicalID(t, srv, stackName, "TestSubnet")
	if vpcIDAfter != vpcIDBefore {
		t.Errorf("VPC was replaced (before %q, after %q); a DNS-attribute-only change must update in place", vpcIDBefore, vpcIDAfter)
	}
	if subnetIDAfter != subnetIDBefore {
		t.Errorf("Subnet was replaced (before %q, after %q); a MapPublicIpOnLaunch-only change must update in place", subnetIDBefore, subnetIDAfter)
	}

	if got := describeVpcDNSAttribute(t, srv, vpcIDAfter, "enableDnsSupport"); got != false {
		t.Errorf("enableDnsSupport = %v after update, want false", got)
	}
	if got := describeVpcDNSAttribute(t, srv, vpcIDAfter, "enableDnsHostnames"); got != true {
		t.Errorf("enableDnsHostnames = %v after update, want true", got)
	}
	if sub := describeSubnetByID(t, srv, subnetIDAfter); !sub.MapPublicIPOnLaunch {
		t.Errorf("mapPublicIpOnLaunch = %v after update, want true", sub.MapPublicIPOnLaunch)
	}
}

// TestCreateStack_EIPDomainAndTags provisions an EIP whose Domain is set
// explicitly and which carries a CloudFormation-owned tag, then asserts both
// come back on DescribeAddresses instead of being dropped (the provisioner
// used to hardcode Domain=vpc and never apply Tags at all).
func TestCreateStack_EIPDomainAndTags(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "eip-domain-tags-stack"
	const template = `{
  "Resources": {
    "TestEIP": {
      "Type": "AWS::EC2::EIP",
      "Properties": {
        "Domain": "vpc",
        "Tags": [{"Key": "environment", "Value": "test"}]
      }
    }
  }
}`

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	allocID := stackResourcePhysicalID(t, srv, stackName, "TestEIP")

	resp := ec2Query(t, srv, "DescribeAddresses", url.Values{
		"AllocationId.1": {allocID},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Addresses []struct {
			AllocationID string `xml:"allocationId"`
			Domain       string `xml:"domain"`
			Tags         []struct {
				Key   string `xml:"key"`
				Value string `xml:"value"`
			} `xml:"tagSet>item"`
		} `xml:"addressesSet>item"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode DescribeAddressesResponse: %v", err)
	}
	if len(result.Addresses) != 1 {
		t.Fatalf("DescribeAddresses returned %d addresses, want 1", len(result.Addresses))
	}
	addr := result.Addresses[0]
	if addr.Domain != "vpc" {
		t.Errorf("domain = %q, want vpc", addr.Domain)
	}
	found := false
	for _, tag := range addr.Tags {
		if tag.Key == "environment" && tag.Value == "test" {
			found = true
		}
	}
	if !found {
		t.Errorf("tags = %+v, want environment=test present", addr.Tags)
	}
}

package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// This file extends ec2_teardown_test.go's coverage of EC2's half of the
// deletion-blocked sentinel to the three operations #1135 taught to answer
// DependencyViolation: DeleteSecurityGroup, DeleteSubnet, and DeleteVpc. Each
// test below follows the same shape as
// TestDeleteStack_ec2InternetGatewayBlockedByExternalAttachment: something
// outside the stack holds a reference CloudFormation's own teardown ordering
// cannot clear for itself, so the refusal has to survive as DELETE_FAILED
// rather than a claimed DELETE_COMPLETE over a resource that is still
// standing, and clearing the dependency lets a retry finish the teardown.

// TestDeleteStack_ec2SecurityGroupBlockedByExternalInstance covers
// DeleteSecurityGroup: it now answers DependencyViolation while an instance
// still references the group, matching
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteSecurityGroup.html
func TestDeleteStack_ec2SecurityGroupBlockedByExternalInstance(t *testing.T) {
	// Given: a stack owning a single security group. Mock clock so the
	// instance's shutting-down → terminated transition (500ms on a real
	// clock) can be advanced deterministically instead of slowing the test.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppSG": {
      "Type": "AWS::EC2::SecurityGroup",
      "Properties": { "GroupDescription": "app sg" }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ec2-sg-teardown-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "ec2-sg-teardown-stack", "CREATE_COMPLETE")

	sgID := listedStackResourcePhysicalID(t, srv, "ec2-sg-teardown-stack", "AppSG")

	// And: something outside the stack launches an instance into the group
	run := ec2Query(t, srv, "RunInstances", url.Values{
		"ImageId":           []string{"ami-12345678"},
		"MinCount":          []string{"1"},
		"MaxCount":          []string{"1"},
		"SecurityGroupId.1": []string{sgID},
	})
	runBody := string(readBody(t, run))
	run.Body.Close()
	helpers.AssertStatus(t, run, http.StatusOK)
	instanceID := xmlTagValue(runBody, "instanceId")
	if instanceID == "" {
		t.Fatalf("RunInstances returned no instanceId: %s", runBody)
	}

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"ec2-sg-teardown-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: the stack fails rather than claiming the group is gone
	waitForStackStatus(t, srv, "ec2-sg-teardown-stack", "DELETE_FAILED")

	// And: the group really is still there
	describe := ec2Query(t, srv, "DescribeSecurityGroups", url.Values{"GroupId.1": []string{sgID}})
	describeBody := string(readBody(t, describe))
	describe.Body.Close()
	helpers.AssertStatus(t, describe, http.StatusOK)
	if !strings.Contains(describeBody, sgID) {
		t.Errorf("DescribeSecurityGroups after failed teardown = %s, want %q still listed", describeBody, sgID)
	}

	// And: EC2's own refusal reached the stack event
	const wantMessage = "has a dependent object"
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{"ec2-sg-teardown-stack"}})
	eventsBody := string(readBody(t, events))
	events.Body.Close()
	if !strings.Contains(eventsBody, wantMessage) {
		t.Errorf("stack events do not carry EC2's refusal message %q:\n%s", wantMessage, eventsBody)
	}
	if !strings.Contains(eventsBody, "DependencyViolation") {
		t.Errorf("stack events do not carry EC2's DependencyViolation code:\n%s", eventsBody)
	}

	// And: the resource stays listed as DELETE_FAILED, with the reason
	status, reason := listedStackResource(t, srv, "ec2-sg-teardown-stack", "AppSG")
	if status != "DELETE_FAILED" {
		t.Errorf("ListStackResources AppSG status = %q, want DELETE_FAILED", status)
	}
	if !strings.Contains(reason, wantMessage) {
		t.Errorf("ListStackResources AppSG reason = %q, want it to contain %q", reason, wantMessage)
	}

	// And: clearing the dependency and retrying finishes the teardown
	term := ec2Query(t, srv, "TerminateInstances", url.Values{"InstanceId.1": []string{instanceID}})
	term.Body.Close()
	helpers.AssertStatus(t, term, http.StatusOK)
	srv.Clock.Add(1 * time.Second) // shutting-down → terminated

	retry := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"ec2-sg-teardown-stack"}})
	defer retry.Body.Close()
	helpers.AssertStatus(t, retry, http.StatusOK)
	waitForStackStatus(t, srv, "ec2-sg-teardown-stack", "DELETE_COMPLETE")
}

// TestDeleteStack_ec2SubnetBlockedByExternalNetworkInterface covers
// DeleteSubnet: it now answers DependencyViolation while a network interface
// still lives in the subnet, matching
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteSubnet.html
func TestDeleteStack_ec2SubnetBlockedByExternalNetworkInterface(t *testing.T) {
	// Given: a VPC created outside any stack, and a stack owning a subnet in it
	srv := helpers.NewTestServer(t)
	createVPC := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.78.0.0/16"}})
	vpcBody := string(readBody(t, createVPC))
	createVPC.Body.Close()
	helpers.AssertStatus(t, createVPC, http.StatusOK)
	vpcID := xmlTagValue(vpcBody, "vpcId")
	if vpcID == "" {
		t.Fatalf("CreateVpc returned no vpcId: %s", vpcBody)
	}

	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppSubnet": {
      "Type": "AWS::EC2::Subnet",
      "Properties": { "VpcId": "` + vpcID + `", "CidrBlock": "10.78.1.0/24" }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ec2-subnet-teardown-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "ec2-subnet-teardown-stack", "CREATE_COMPLETE")

	subnetID := listedStackResourcePhysicalID(t, srv, "ec2-subnet-teardown-stack", "AppSubnet")

	// And: something outside the stack creates a network interface in the subnet
	eni := ec2Query(t, srv, "CreateNetworkInterface", url.Values{"SubnetId": []string{subnetID}})
	eniBody := string(readBody(t, eni))
	eni.Body.Close()
	helpers.AssertStatus(t, eni, http.StatusOK)
	eniID := xmlTagValue(eniBody, "networkInterfaceId")
	if eniID == "" {
		t.Fatalf("CreateNetworkInterface returned no networkInterfaceId: %s", eniBody)
	}

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"ec2-subnet-teardown-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: the stack fails rather than claiming the subnet is gone
	waitForStackStatus(t, srv, "ec2-subnet-teardown-stack", "DELETE_FAILED")

	// And: the subnet really is still there
	describe := ec2Query(t, srv, "DescribeSubnets", url.Values{"SubnetId.1": []string{subnetID}})
	describeBody := string(readBody(t, describe))
	describe.Body.Close()
	helpers.AssertStatus(t, describe, http.StatusOK)
	if !strings.Contains(describeBody, subnetID) {
		t.Errorf("DescribeSubnets after failed teardown = %s, want %q still listed", describeBody, subnetID)
	}

	// And: EC2's own refusal reached the stack event
	const wantMessage = "has dependencies and cannot be deleted"
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{"ec2-subnet-teardown-stack"}})
	eventsBody := string(readBody(t, events))
	events.Body.Close()
	if !strings.Contains(eventsBody, wantMessage) {
		t.Errorf("stack events do not carry EC2's refusal message %q:\n%s", wantMessage, eventsBody)
	}
	if !strings.Contains(eventsBody, "DependencyViolation") {
		t.Errorf("stack events do not carry EC2's DependencyViolation code:\n%s", eventsBody)
	}

	// And: the resource stays listed as DELETE_FAILED, with the reason
	status, reason := listedStackResource(t, srv, "ec2-subnet-teardown-stack", "AppSubnet")
	if status != "DELETE_FAILED" {
		t.Errorf("ListStackResources AppSubnet status = %q, want DELETE_FAILED", status)
	}
	if !strings.Contains(reason, wantMessage) {
		t.Errorf("ListStackResources AppSubnet reason = %q, want it to contain %q", reason, wantMessage)
	}

	// And: clearing the dependency and retrying finishes the teardown
	delENI := ec2Query(t, srv, "DeleteNetworkInterface", url.Values{"NetworkInterfaceId": []string{eniID}})
	delENI.Body.Close()
	helpers.AssertStatus(t, delENI, http.StatusOK)

	retry := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"ec2-subnet-teardown-stack"}})
	defer retry.Body.Close()
	helpers.AssertStatus(t, retry, http.StatusOK)
	waitForStackStatus(t, srv, "ec2-subnet-teardown-stack", "DELETE_COMPLETE")
}

// TestDeleteStack_ec2VpcBlockedByExternalSubnet covers DeleteVpc: it now
// answers DependencyViolation while a subnet still lives in the VPC, matching
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteVpc.html
func TestDeleteStack_ec2VpcBlockedByExternalSubnet(t *testing.T) {
	// Given: a stack owning a single VPC
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppVPC": {
      "Type": "AWS::EC2::VPC",
      "Properties": { "CidrBlock": "10.79.0.0/16" }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ec2-vpc-teardown-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "ec2-vpc-teardown-stack", "CREATE_COMPLETE")

	vpcID := listedStackResourcePhysicalID(t, srv, "ec2-vpc-teardown-stack", "AppVPC")

	// And: something outside the stack creates a subnet in the VPC
	sub := ec2Query(t, srv, "CreateSubnet", url.Values{
		"VpcId":     []string{vpcID},
		"CidrBlock": []string{"10.79.1.0/24"},
	})
	subBody := string(readBody(t, sub))
	sub.Body.Close()
	helpers.AssertStatus(t, sub, http.StatusOK)
	subnetID := xmlTagValue(subBody, "subnetId")
	if subnetID == "" {
		t.Fatalf("CreateSubnet returned no subnetId: %s", subBody)
	}

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"ec2-vpc-teardown-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: the stack fails rather than claiming the VPC is gone
	waitForStackStatus(t, srv, "ec2-vpc-teardown-stack", "DELETE_FAILED")

	// And: the VPC really is still there
	describe := ec2Query(t, srv, "DescribeVpcs", url.Values{"VpcId.1": []string{vpcID}})
	describeBody := string(readBody(t, describe))
	describe.Body.Close()
	helpers.AssertStatus(t, describe, http.StatusOK)
	if !strings.Contains(describeBody, vpcID) {
		t.Errorf("DescribeVpcs after failed teardown = %s, want %q still listed", describeBody, vpcID)
	}

	// And: EC2's own refusal reached the stack event
	const wantMessage = "has dependencies and cannot be deleted"
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{"ec2-vpc-teardown-stack"}})
	eventsBody := string(readBody(t, events))
	events.Body.Close()
	if !strings.Contains(eventsBody, wantMessage) {
		t.Errorf("stack events do not carry EC2's refusal message %q:\n%s", wantMessage, eventsBody)
	}
	if !strings.Contains(eventsBody, "DependencyViolation") {
		t.Errorf("stack events do not carry EC2's DependencyViolation code:\n%s", eventsBody)
	}

	// And: the resource stays listed as DELETE_FAILED, with the reason
	status, reason := listedStackResource(t, srv, "ec2-vpc-teardown-stack", "AppVPC")
	if status != "DELETE_FAILED" {
		t.Errorf("ListStackResources AppVPC status = %q, want DELETE_FAILED", status)
	}
	if !strings.Contains(reason, wantMessage) {
		t.Errorf("ListStackResources AppVPC reason = %q, want it to contain %q", reason, wantMessage)
	}

	// And: clearing the dependency and retrying finishes the teardown
	delSub := ec2Query(t, srv, "DeleteSubnet", url.Values{"SubnetId": []string{subnetID}})
	delSub.Body.Close()
	helpers.AssertStatus(t, delSub, http.StatusOK)

	retry := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"ec2-vpc-teardown-stack"}})
	defer retry.Body.Close()
	helpers.AssertStatus(t, retry, http.StatusOK)
	waitForStackStatus(t, srv, "ec2-vpc-teardown-stack", "DELETE_COMPLETE")
}

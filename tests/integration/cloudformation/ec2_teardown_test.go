package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// TestDeleteStack_ec2InternetGatewayBlockedByExternalAttachment covers EC2's
// half of the deletion-blocked sentinel that iam_teardown_test.go covers for
// IAM: DeleteInternetGateway answers DependencyViolation while the gateway is
// still attached to a VPC, and that refusal has to fail the stack rather than
// being recorded as an ordinary — and possibly transient — failure.
//
// The attaching VPC lives outside the stack, the same shape the IAM tests use
// for "something the stack cannot clear for itself": CloudFormation's own
// teardown ordering only detaches what its own AWS::EC2::VPCGatewayAttachment
// resource created, so an attachment made by something else is exactly the
// case that must survive as DELETE_FAILED rather than a claimed
// DELETE_COMPLETE over a gateway that is still standing.
func TestDeleteStack_ec2InternetGatewayBlockedByExternalAttachment(t *testing.T) {
	// Given: a VPC created outside any stack
	srv := helpers.NewTestServer(t)
	createVPC := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{"10.77.0.0/16"}})
	vpcBody := string(readBody(t, createVPC))
	createVPC.Body.Close()
	helpers.AssertStatus(t, createVPC, http.StatusOK)
	vpcID := xmlTagValue(vpcBody, "vpcId")
	if vpcID == "" {
		t.Fatalf("CreateVpc returned no vpcId: %s", vpcBody)
	}

	// And: a stack owning a single internet gateway
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppIGW": { "Type": "AWS::EC2::InternetGateway" }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ec2-igw-teardown-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "ec2-igw-teardown-stack", "CREATE_COMPLETE")

	igwID := listedStackResourcePhysicalID(t, srv, "ec2-igw-teardown-stack", "AppIGW")

	// And: something outside the stack attaches it to the VPC
	attach := ec2Query(t, srv, "AttachInternetGateway", url.Values{
		"InternetGatewayId": []string{igwID},
		"VpcId":             []string{vpcID},
	})
	attach.Body.Close()
	helpers.AssertStatus(t, attach, http.StatusOK)

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"ec2-igw-teardown-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: the stack fails rather than claiming the gateway is gone
	waitForStackStatus(t, srv, "ec2-igw-teardown-stack", "DELETE_FAILED")

	// And: the gateway really is still there
	describe := ec2Query(t, srv, "DescribeInternetGateways", url.Values{"InternetGatewayId.1": []string{igwID}})
	describeBody := string(readBody(t, describe))
	describe.Body.Close()
	helpers.AssertStatus(t, describe, http.StatusOK)
	if !strings.Contains(describeBody, igwID) {
		t.Errorf("DescribeInternetGateways after failed teardown = %s, want %q still listed", describeBody, igwID)
	}

	// And: EC2's own refusal reached the stack event
	const wantMessage = "has dependencies and cannot be deleted"
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{"ec2-igw-teardown-stack"}})
	eventsBody := string(readBody(t, events))
	events.Body.Close()
	if !strings.Contains(eventsBody, wantMessage) {
		t.Errorf("stack events do not carry EC2's refusal message %q:\n%s", wantMessage, eventsBody)
	}
	if !strings.Contains(eventsBody, "DependencyViolation") {
		t.Errorf("stack events do not carry EC2's DependencyViolation code:\n%s", eventsBody)
	}

	// And: the resource stays listed as DELETE_FAILED, with the reason, so a
	// retry knows what is still standing and why.
	status, reason := listedStackResource(t, srv, "ec2-igw-teardown-stack", "AppIGW")
	if status != "DELETE_FAILED" {
		t.Errorf("ListStackResources AppIGW status = %q, want DELETE_FAILED", status)
	}
	if !strings.Contains(reason, wantMessage) {
		t.Errorf("ListStackResources AppIGW reason = %q, want it to contain %q", reason, wantMessage)
	}

	// And: clearing the dependency and retrying finishes the teardown, the
	// same recovery real CloudFormation and the IAM refusal tests both allow.
	detach := ec2Query(t, srv, "DetachInternetGateway", url.Values{
		"InternetGatewayId": []string{igwID},
		"VpcId":             []string{vpcID},
	})
	detach.Body.Close()
	helpers.AssertStatus(t, detach, http.StatusOK)

	retry := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"ec2-igw-teardown-stack"}})
	defer retry.Body.Close()
	helpers.AssertStatus(t, retry, http.StatusOK)
	waitForStackStatus(t, srv, "ec2-igw-teardown-stack", "DELETE_COMPLETE")
}

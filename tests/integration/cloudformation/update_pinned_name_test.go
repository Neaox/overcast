package cloudformation_test

// update_pinned_name_test.go — property-only updates to resources with pinned
// names, whose services reject duplicate creates.
//
// These types cannot be updated by replacement at all: replacement creates
// the new resource before deleting the old, the name is taken, and the create
// 409s (the `cdk bootstrap` re-run failure, generalised — see PR #396 for the
// ECR original). Real CloudFormation updates them in place, replacing only on
// a name change. One test per handler family: query-protocol (RDS), JSON with
// an update op (EKS), and JSON without one (scheduler group).

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestUpdateStack_rdsSubnetGroupDescriptionChange_updatesInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "subnetgrp-upd"
	template := `{
  "Resources": {
    "Grp": {
      "Type": "AWS::RDS::DBSubnetGroup",
      "Properties": {
        "DBSubnetGroupName": "pinned-subnet-group",
        "DBSubnetGroupDescription": "%s",
        "SubnetIds": ["subnet-aaa", "subnet-bbb"]
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(template, "v1")},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	originalID := describeStackResourceIDs(t, srv, stackName)["Grp"]

	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(template, "v2")},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := describeStackResourceIDs(t, srv, stackName)["Grp"]; got != originalID {
		t.Errorf("subnet group was replaced: physical ID %q, want original %q", got, originalID)
	}
}

func TestUpdateStack_eksClusterVersionChange_updatesInPlace(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "eks-upd"
	template := `{
  "Resources": {
    "Cluster": {
      "Type": "AWS::EKS::Cluster",
      "Properties": {
        "Name": "pinned-eks-cluster",
        "RoleArn": "arn:aws:iam::000000000000:role/eks-role",
        "Version": "%s",
        "ResourcesVpcConfig": {"SubnetIds": ["subnet-aaa", "subnet-bbb"]}
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(template, "1.31")},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	originalID := describeStackResourceIDs(t, srv, stackName)["Cluster"]

	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(template, "1.32")},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := describeStackResourceIDs(t, srv, stackName)["Cluster"]; got != originalID {
		t.Errorf("cluster was replaced: physical ID %q, want original %q", got, originalID)
	}

	// And the cluster still exists with the new version applied. EKS is
	// restJson1 with no X-Amz-Target dispatch (#1226), so this reads its own
	// REST binding rather than an invented JSON target.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/clusters/pinned-eks-cluster", nil)
	if err != nil {
		t.Fatalf("build DescribeCluster request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))
	if !strings.Contains(body, "1.32") {
		t.Errorf("cluster version was not updated in place; body:\n%s", body)
	}
}

func TestUpdateStack_schedulerScheduleGroupPropertyChange_keepsGroup(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "schedgrp-upd"
	template := `{
  "Resources": {
    "Grp": {
      "Type": "AWS::Scheduler::ScheduleGroup",
      "Properties": {
        "Name": "pinned-schedule-group",
        "Tags": [{"Key": "rev", "Value": "%s"}]
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(template, "v1")},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	originalID := describeStackResourceIDs(t, srv, stackName)["Grp"]

	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {fmt.Sprintf(template, "v2")},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := describeStackResourceIDs(t, srv, stackName)["Grp"]; got != originalID {
		t.Errorf("schedule group was replaced: physical ID %q, want original %q", got, originalID)
	}
}

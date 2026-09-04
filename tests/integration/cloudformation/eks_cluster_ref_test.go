package cloudformation_test

// eks_cluster_ref_test.go — AWS::EKS::Cluster's Ref must resolve to the
// cluster name, not its ARN.
//
// Per AWS docs (https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-cluster.html#aws-resource-eks-cluster-return-values-ref):
// "When you pass the logical ID of this resource to the intrinsic Ref
// function, Ref returns the resource name." The ARN is exposed separately as
// the Arn Fn::GetAtt attribute
// (https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-cluster.html#aws-resource-eks-cluster-return-values-fn--getatt).
//
// Before the fix, eksClusterHandler.Create recorded the ARN as the physical
// ID, so Ref returned the ARN. Any stack that wires a child EKS resource to
// its cluster by Ref — the pattern CDK's L2 Nodegroup/FargateProfile/Addon
// constructs use — failed locally with "No cluster found for name:
// arn:aws:eks:...:cluster%2F<name>" while it deploys fine on real AWS.
// generated_names_test.go's EKS rows work around this with a literal cluster
// name plus DependsOn instead of Ref; this file is the reproduction and
// regression test for the underlying bug (issue #1690).

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// eksClusterRefTemplate wires a Nodegroup and a FargateProfile to their
// cluster purely by {"Ref": "Cluster"} — no literal name, no DependsOn. Real
// CloudFormation infers the dependency from the Ref, and the child resource's
// ClusterName must resolve to the name CreateNodegroup/CreateFargateProfile
// expect, not the ARN.
const eksClusterRefTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::EKS::Cluster",
      "Properties": {
        "Name": "ref-test-cluster",
        "RoleArn": "arn:aws:iam::000000000000:role/eks",
        "ResourcesVpcConfig": {"SubnetIds": ["subnet-1"]}
      }
    },
    "Nodegroup": {
      "Type": "AWS::EKS::Nodegroup",
      "Properties": {
        "ClusterName": {"Ref": "Cluster"},
        "NodeRole": "arn:aws:iam::000000000000:role/ng",
        "Subnets": ["subnet-1"]
      }
    },
    "FargateProfile": {
      "Type": "AWS::EKS::FargateProfile",
      "Properties": {
        "ClusterName": {"Ref": "Cluster"},
        "PodExecutionRoleArn": "arn:aws:iam::000000000000:role/fp"
      }
    }
  },
  "Outputs": {
    "ClusterRef": {"Value": {"Ref": "Cluster"}},
    "ClusterArn": {"Value": {"Fn::GetAtt": ["Cluster", "Arn"]}}
  }
}`

func TestCreateStack_eksNodegroupWiredByClusterRef_reachesCreateComplete(t *testing.T) {
	// Given: a stack with an EKS cluster and a nodegroup + Fargate profile
	// that reference it only via {"Ref": "Cluster"}
	srv := helpers.NewTestServer(t)
	const stackName = "eks-cluster-ref-stack"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {eksClusterRefTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	// When: the stack is created
	status := waitForStackStatusIn(t, srv, stackName,
		"CREATE_COMPLETE", "CREATE_FAILED", "ROLLBACK_COMPLETE", "ROLLBACK_IN_PROGRESS")

	// Then: it reaches CREATE_COMPLETE. Before the fix this failed because
	// CreateNodegroup/CreateFargateProfile received the cluster ARN (what Ref
	// used to resolve to) as ClusterName instead of the name.
	if status != "CREATE_COMPLETE" {
		t.Fatalf("stack status = %s, want CREATE_COMPLETE; reasons:\n%s",
			status, strings.Join(describeStackEventReasons(t, srv, stackName), "\n"))
	}

	// And: the physical ID CloudFormation recorded for the cluster — what
	// DescribeStackResources shows on AWS — is the name, not the ARN.
	resourceIDs := describeStackResourceIDs(t, srv, stackName)
	clusterPhysicalID := resourceIDs["Cluster"]
	if clusterPhysicalID != "ref-test-cluster" {
		t.Errorf("Cluster physical ID = %q, want the cluster name %q", clusterPhysicalID, "ref-test-cluster")
	}
	if strings.HasPrefix(clusterPhysicalID, "arn:") {
		t.Errorf("Cluster physical ID = %q, still ARN-shaped", clusterPhysicalID)
	}

	// And: the nodegroup and Fargate profile were created against the real
	// cluster name — their own physical IDs are "<clusterName>/<name>".
	if ng := resourceIDs["Nodegroup"]; !strings.HasPrefix(ng, "ref-test-cluster/") {
		t.Errorf("Nodegroup physical ID = %q, want it prefixed with the cluster name", ng)
	}
	if fp := resourceIDs["FargateProfile"]; !strings.HasPrefix(fp, "ref-test-cluster/") {
		t.Errorf("FargateProfile physical ID = %q, want it prefixed with the cluster name", fp)
	}

	// And: Ref and Fn::GetAtt Arn diverge as AWS documents — Ref is the name,
	// GetAtt Arn is still the ARN.
	desc := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {stackName}})
	defer desc.Body.Close()
	helpers.AssertStatus(t, desc, http.StatusOK)
	body := string(readBody(t, desc))

	clusterRef := outputValue(t, body, "ClusterRef")
	if clusterRef != "ref-test-cluster" {
		t.Errorf("Ref(Cluster) output = %q, want the cluster name %q", clusterRef, "ref-test-cluster")
	}

	clusterArn := outputValue(t, body, "ClusterArn")
	if !strings.HasPrefix(clusterArn, "arn:aws:eks:") || !strings.HasSuffix(clusterArn, "cluster/ref-test-cluster") {
		t.Errorf("GetAtt(Cluster, Arn) output = %q, want an EKS cluster ARN ending in cluster/ref-test-cluster", clusterArn)
	}
}

// TestDeleteStack_eksCluster_deletesByName is the delete-path counterpart:
// once the physical ID is the name, DeleteStack must resolve DeleteCluster by
// that name (Delete used to strip the name back out of the ARN it was handed;
// now it is handed the name directly and must not mis-treat it as needing
// extraction).
func TestDeleteStack_eksCluster_deletesByName(t *testing.T) {
	// Given: a stack with a standalone EKS cluster
	srv := helpers.NewTestServer(t)
	const stackName = "eks-cluster-delete-stack"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
			"Resources": {
				"Cluster": {"Type": "AWS::EKS::Cluster", "Properties": {
					"Name": "delete-test-cluster",
					"RoleArn": "arn:aws:iam::000000000000:role/eks",
					"ResourcesVpcConfig": {"SubnetIds": ["subnet-1"]}
				}}
			}
		}`},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE", "CREATE_FAILED")

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)
	status := waitForStackStatusIn(t, srv, stackName, "DELETE_COMPLETE", "DELETE_FAILED")

	// Then: the stack deletes cleanly
	if status != "DELETE_COMPLETE" {
		t.Fatalf("stack status = %s, want DELETE_COMPLETE; reasons:\n%s",
			status, strings.Join(describeStackEventReasons(t, srv, stackName), "\n"))
	}

	// And: the underlying EKS cluster is gone too — DescribeCluster answers
	// ResourceNotFoundException.
	rec := eksClustersGet(t, srv, "delete-test-cluster")
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusNotFound {
		t.Errorf("DescribeCluster after stack delete: status = %d, want 404", rec.StatusCode)
	}
}

// eksClustersGet issues a plain DescribeCluster REST call against the
// emulator's EKS API — used to confirm the cluster the CloudFormation
// provisioner deleted is actually gone from the service, not just off the
// stack's resource list.
func eksClustersGet(t *testing.T, srv *helpers.TestServer, name string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/clusters/"+url.QueryEscape(name), nil)
	if err != nil {
		t.Fatalf("build DescribeCluster request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}
	return resp
}

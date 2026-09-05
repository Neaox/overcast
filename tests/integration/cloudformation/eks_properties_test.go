package cloudformation_test

// eks_properties_test.go — AWS::EKS::Cluster and AWS::EKS::Nodegroup property
// threading (#540).
//
// EKS is Docker-backed: CreateCluster starts a k3s control-plane container,
// and `resourcesVpcConfig.subnetIds` is what decides which VPC network that
// container joins (clusterSubnetIDs, internal/services/eks/live_runtime.go).
// The handler forwarded the template's `ResourcesVpcConfig` verbatim, so the
// runtime looked for `subnetIds` in a map that only had `SubnetIds` and every
// CDK-provisioned cluster landed on the default plane. Everything else on
// both resource types beyond a handful of properties was dropped outright.
//
// These read back through the service's own Describe API rather than
// asserting on the request the handler built, which is the shape #540 asks
// for: a property that reaches the service but is not stored is as invisible
// to a user as one that never left the handler. They do not need Docker — the
// stored record is written on the same path in either mode.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// eksClusterPropertiesTemplate sets every AWS::EKS::Cluster property the
// handler forwards, plus one it does not (UpgradePolicy) to pin the
// unconsumed-property notice.
const eksClusterPropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::EKS::Cluster",
      "Properties": {
        "Name": "cfn-props-cluster",
        "RoleArn": "arn:aws:iam::000000000000:role/eks",
        "Version": "1.30",
        "ResourcesVpcConfig": {
          "SubnetIds": ["subnet-aaa", "subnet-bbb"],
          "SecurityGroupIds": ["sg-aaa"],
          "EndpointPublicAccess": false,
          "EndpointPrivateAccess": true
        },
        "KubernetesNetworkConfig": {"IpFamily": "ipv4", "ServiceIpv4Cidr": "10.100.0.0/16"},
        "AccessConfig": {"AuthenticationMode": "API", "BootstrapClusterCreatorAdminPermissions": false},
        "EncryptionConfig": [
          {"Provider": {"KeyArn": "arn:aws:kms:us-east-1:000000000000:key/abc"}, "Resources": ["secrets"]}
        ],
        "Logging": {"ClusterLogging": {"EnabledTypes": [{"Type": "api"}, {"Type": "audit"}]}},
        "UpgradePolicy": {"SupportType": "STANDARD"},
        "Tags": {"env": "prod", "Owner": "platform"}
      }
    }
  }
}`

// TestCreateStack_EKSCluster_propertiesThreaded is the failing-first case for
// the AWS::EKS::Cluster half of #540. Before the fix the handler sent only
// name, roleArn, version and a verbatim resourcesVpcConfig, so
// KubernetesNetworkConfig, AccessConfig, EncryptionConfig, Logging and Tags
// were all dropped and the VPC config arrived under keys the runtime does not
// read.
func TestCreateStack_EKSCluster_propertiesThreaded(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "eks-cluster-properties-stack"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{eksClusterPropertiesTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	cluster := eksDescribeCluster(t, srv, "cfn-props-cluster")

	// The behavioural one: the runtime reads resourcesVpcConfig.subnetIds to
	// place the k3s container on a VPC network. Under the template's own
	// casing it read nothing and left the cluster on the default plane.
	vpcConfig, _ := cluster["resourcesVpcConfig"].(map[string]any)
	subnets, _ := vpcConfig["subnetIds"].([]any)
	if len(subnets) != 2 || subnets[0] != "subnet-aaa" {
		t.Errorf("resourcesVpcConfig.subnetIds = %v, want the template's two subnets; got config %v",
			subnets, vpcConfig)
	}
	if got, ok := vpcConfig["endpointPrivateAccess"].(bool); !ok || !got {
		t.Errorf("resourcesVpcConfig.endpointPrivateAccess = %v, want true", vpcConfig["endpointPrivateAccess"])
	}

	if got := cluster["version"]; got != "1.30" {
		t.Errorf("version = %v, want 1.30", got)
	}

	netConfig, _ := cluster["kubernetesNetworkConfig"].(map[string]any)
	if got := netConfig["serviceIpv4Cidr"]; got != "10.100.0.0/16" {
		t.Errorf("kubernetesNetworkConfig.serviceIpv4Cidr = %v, want 10.100.0.0/16 (got %v)", got, netConfig)
	}

	accessConfig, _ := cluster["accessConfig"].(map[string]any)
	if got := accessConfig["authenticationMode"]; got != "API" {
		t.Errorf("accessConfig.authenticationMode = %v, want API (got %v)", got, accessConfig)
	}

	encryption, _ := cluster["encryptionConfig"].([]any)
	if len(encryption) != 1 {
		t.Fatalf("encryptionConfig = %v, want one entry", cluster["encryptionConfig"])
	}
	entry, _ := encryption[0].(map[string]any)
	provider, _ := entry["provider"].(map[string]any)
	if got := provider["keyArn"]; got != "arn:aws:kms:us-east-1:000000000000:key/abc" {
		t.Errorf("encryptionConfig[0].provider.keyArn = %v, want the template's key ARN (got %v)", got, entry)
	}

	// Logging is the one property whose CloudFormation shape is not the API's:
	// {ClusterLogging: {EnabledTypes: [{Type}]}} becomes
	// {clusterLogging: [{types: [...], enabled: true}]}.
	assertEKSClusterLogging(t, cluster, "api", "audit")

	tags, _ := cluster["tags"].(map[string]any)
	if tags["env"] != "prod" || tags["Owner"] != "platform" {
		t.Errorf("tags = %v, want the template's two tags with their keys unchanged", tags)
	}

	// And the property the handler does not act on is reported rather than
	// dropped in silence — see noteUnconsumedProperties.
	reasons := describeStackResourceReasons(t, srv, stackName)
	if !strings.Contains(reasons, "UpgradePolicy") {
		t.Errorf("expected the cluster's ResourceStatusReason to name the unapplied UpgradePolicy, got: %s", reasons)
	}
}

// eksClusterLoggingUpdateTemplate is eksClusterPropertiesTemplate with the
// logging types changed, and nothing else.
const eksClusterLoggingUpdateTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::EKS::Cluster",
      "Properties": {
        "Name": "cfn-props-cluster",
        "RoleArn": "arn:aws:iam::000000000000:role/eks",
        "Version": "1.30",
        "ResourcesVpcConfig": {"SubnetIds": ["subnet-aaa", "subnet-ccc"]},
        "KubernetesNetworkConfig": {"IpFamily": "ipv4", "ServiceIpv4Cidr": "10.100.0.0/16"},
        "Logging": {"ClusterLogging": {"EnabledTypes": [{"Type": "scheduler"}]}}
      }
    }
  }
}`

// TestUpdateStack_EKSCluster_loggingConvergesWithCreate is #540's pattern 2 for
// EKS: Create and Update must reach the same state for the same template, so a
// property is not something that only appears after a second deploy. Logging
// was the asymmetry here — UpdateClusterConfig has always accepted it and
// CreateCluster never sent it.
func TestUpdateStack_EKSCluster_loggingConvergesWithCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "eks-cluster-logging-update-stack"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{eksClusterPropertiesTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// The first deploy already has it — nothing waits for a second one.
	assertEKSClusterLogging(t, eksDescribeCluster(t, srv, "cfn-props-cluster"), "api", "audit")

	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{eksClusterLoggingUpdateTemplate},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	cluster := eksDescribeCluster(t, srv, "cfn-props-cluster")
	assertEKSClusterLogging(t, cluster, "scheduler")

	// The VPC config change in the same update reaches the service too, still
	// under the keys the runtime reads.
	vpcConfig, _ := cluster["resourcesVpcConfig"].(map[string]any)
	subnets, _ := vpcConfig["subnetIds"].([]any)
	if len(subnets) != 2 || subnets[1] != "subnet-ccc" {
		t.Errorf("after update, resourcesVpcConfig.subnetIds = %v, want the updated subnets", subnets)
	}
}

// eksNodegroupPropertiesTemplate sets every AWS::EKS::Nodegroup property the
// handler forwards, plus RemoteAccess, which the EKS service does not model.
const eksNodegroupPropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::EKS::Cluster",
      "Properties": {
        "Name": "cfn-ng-cluster",
        "RoleArn": "arn:aws:iam::000000000000:role/eks",
        "ResourcesVpcConfig": {"SubnetIds": ["subnet-aaa"]}
      }
    },
    "Nodegroup": {
      "Type": "AWS::EKS::Nodegroup",
      "Properties": {
        "ClusterName": {"Ref": "Cluster"},
        "NodegroupName": "cfn-props-ng",
        "NodeRole": "arn:aws:iam::000000000000:role/ng",
        "Subnets": ["subnet-aaa"],
        "InstanceTypes": ["m5.large", "m5.xlarge"],
        "AmiType": "AL2023_x86_64_STANDARD",
        "CapacityType": "SPOT",
        "DiskSize": 50,
        "Version": "1.30",
        "ScalingConfig": {"MinSize": 1, "MaxSize": 5, "DesiredSize": 2},
        "UpdateConfig": {"MaxUnavailable": 1},
        "LaunchTemplate": {"Name": "ng-template", "Version": "3"},
        "Labels": {"Environment": "prod", "team": "platform"},
        "Taints": [{"Key": "dedicated", "Value": "gpu", "Effect": "NO_SCHEDULE"}],
        "RemoteAccess": {"Ec2SshKey": "my-key"},
        "Tags": {"env": "prod"}
      }
    }
  }
}`

// TestCreateStack_EKSNodegroup_propertiesThreaded is the failing-first case for
// the AWS::EKS::Nodegroup half of #540. CreateNodegroup already accepted every
// one of these members; the handler sent four of them.
func TestCreateStack_EKSNodegroup_propertiesThreaded(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "eks-nodegroup-properties-stack"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{eksNodegroupPropertiesTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	ng := eksDescribeNodegroup(t, srv, "cfn-ng-cluster", "cfn-props-ng")

	instanceTypes, _ := ng["instanceTypes"].([]any)
	if len(instanceTypes) != 2 || instanceTypes[0] != "m5.large" {
		t.Errorf("instanceTypes = %v, want the template's two types", ng["instanceTypes"])
	}
	if got := ng["amiType"]; got != "AL2023_x86_64_STANDARD" {
		t.Errorf("amiType = %v, want AL2023_x86_64_STANDARD", got)
	}
	if got := ng["capacityType"]; got != "SPOT" {
		t.Errorf("capacityType = %v, want SPOT", got)
	}
	if got, _ := ng["diskSize"].(float64); got != 50 {
		t.Errorf("diskSize = %v, want 50", ng["diskSize"])
	}
	if got := ng["version"]; got != "1.30" {
		t.Errorf("version = %v, want 1.30", got)
	}

	scaling, _ := ng["scalingConfig"].(map[string]any)
	if got, _ := scaling["desiredSize"].(float64); got != 2 {
		t.Errorf("scalingConfig.desiredSize = %v, want 2 (got %v)", scaling["desiredSize"], scaling)
	}
	updateConfig, _ := ng["updateConfig"].(map[string]any)
	if got, _ := updateConfig["maxUnavailable"].(float64); got != 1 {
		t.Errorf("updateConfig.maxUnavailable = %v, want 1 (got %v)", updateConfig["maxUnavailable"], updateConfig)
	}
	launchTemplate, _ := ng["launchTemplate"].(map[string]any)
	if launchTemplate["name"] != "ng-template" || launchTemplate["version"] != "3" {
		t.Errorf("launchTemplate = %v, want the template's name and version", launchTemplate)
	}

	// Labels are the user's own keys: they must survive verbatim, not be
	// lower-camel-cased along with the modelled member names around them.
	labels, _ := ng["labels"].(map[string]any)
	if labels["Environment"] != "prod" || labels["team"] != "platform" {
		t.Errorf("labels = %v, want both keys with their original casing", labels)
	}

	taints, _ := ng["taints"].([]any)
	if len(taints) != 1 {
		t.Fatalf("taints = %v, want one taint", ng["taints"])
	}
	taint, _ := taints[0].(map[string]any)
	if taint["key"] != "dedicated" || taint["effect"] != "NO_SCHEDULE" {
		t.Errorf("taints[0] = %v, want the template's taint under the API's member names", taint)
	}

	tags, _ := ng["tags"].(map[string]any)
	if tags["env"] != "prod" {
		t.Errorf("tags = %v, want env=prod", tags)
	}

	// RemoteAccess has no member on the EKS service, so it is reported rather
	// than dropped in silence.
	reasons := describeStackResourceReasons(t, srv, stackName)
	if !strings.Contains(reasons, "RemoteAccess") {
		t.Errorf("expected the nodegroup's ResourceStatusReason to name the unapplied RemoteAccess, got: %s", reasons)
	}
}

// eksNodegroupScalingUpdateTemplate changes ScalingConfig and UpdateConfig on
// the nodegroup and nothing else.
const eksNodegroupScalingUpdateTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::EKS::Cluster",
      "Properties": {
        "Name": "cfn-ng-cluster",
        "RoleArn": "arn:aws:iam::000000000000:role/eks",
        "ResourcesVpcConfig": {"SubnetIds": ["subnet-aaa"]}
      }
    },
    "Nodegroup": {
      "Type": "AWS::EKS::Nodegroup",
      "Properties": {
        "ClusterName": {"Ref": "Cluster"},
        "NodegroupName": "cfn-props-ng",
        "NodeRole": "arn:aws:iam::000000000000:role/ng",
        "Subnets": ["subnet-aaa"],
        "ScalingConfig": {"MinSize": 2, "MaxSize": 8, "DesiredSize": 4},
        "UpdateConfig": {"MaxUnavailable": 3}
      }
    }
  }
}`

// TestUpdateStack_EKSNodegroup_updateConfigApplied covers the Update half:
// UpdateNodegroupConfig accepts updateConfig as well as scalingConfig, and the
// handler sent only the latter, so a MaxUnavailable change never landed.
func TestUpdateStack_EKSNodegroup_updateConfigApplied(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "eks-nodegroup-update-stack"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{eksNodegroupPropertiesTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{eksNodegroupScalingUpdateTemplate},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	ng := eksDescribeNodegroup(t, srv, "cfn-ng-cluster", "cfn-props-ng")
	scaling, _ := ng["scalingConfig"].(map[string]any)
	if got, _ := scaling["desiredSize"].(float64); got != 4 {
		t.Errorf("after update, scalingConfig.desiredSize = %v, want 4", scaling["desiredSize"])
	}
	updateConfig, _ := ng["updateConfig"].(map[string]any)
	if got, _ := updateConfig["maxUnavailable"].(float64); got != 3 {
		t.Errorf("after update, updateConfig.maxUnavailable = %v, want 3", updateConfig["maxUnavailable"])
	}
}

// assertEKSClusterLogging checks that logging.clusterLogging carries exactly
// the enabled types the template named, in the shape the API models.
func assertEKSClusterLogging(t *testing.T, cluster map[string]any, want ...string) {
	t.Helper()
	logging, _ := cluster["logging"].(map[string]any)
	setups, _ := logging["clusterLogging"].([]any)
	if len(setups) != 1 {
		t.Fatalf("logging.clusterLogging = %v, want one LogSetup", cluster["logging"])
	}
	setup, _ := setups[0].(map[string]any)
	if enabled, _ := setup["enabled"].(bool); !enabled {
		t.Errorf("logging.clusterLogging[0].enabled = %v, want true", setup["enabled"])
	}
	types, _ := setup["types"].([]any)
	if len(types) != len(want) {
		t.Fatalf("logging.clusterLogging[0].types = %v, want %v", setup["types"], want)
	}
	for i, wantType := range want {
		if types[i] != wantType {
			t.Errorf("logging.clusterLogging[0].types[%d] = %v, want %s", i, types[i], wantType)
		}
	}
}

// eksDescribeCluster reads a cluster back through DescribeCluster.
func eksDescribeCluster(t *testing.T, srv *helpers.TestServer, name string) map[string]any {
	t.Helper()
	resp := eksCFNCall(t, srv, http.MethodGet, "/clusters/"+url.PathEscape(name))
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Cluster map[string]any `json:"cluster"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("DescribeCluster %s: parse response: %v", name, err)
	}
	return out.Cluster
}

// eksDescribeNodegroup reads a nodegroup back through DescribeNodegroup.
func eksDescribeNodegroup(t *testing.T, srv *helpers.TestServer, clusterName, nodegroupName string) map[string]any {
	t.Helper()
	resp := eksCFNCall(t, srv, http.MethodGet,
		"/clusters/"+url.PathEscape(clusterName)+"/node-groups/"+url.PathEscape(nodegroupName))
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Nodegroup map[string]any `json:"nodegroup"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("DescribeNodegroup %s/%s: parse response: %v", clusterName, nodegroupName, err)
	}
	return out.Nodegroup
}

// describeStackResourceReasons returns the DescribeStackResources body, which
// is where a resource's ResourceStatusReason — the channel an unapplied
// property is reported on — appears beside the resource it belongs to.
func describeStackResourceReasons(t *testing.T, srv *helpers.TestServer, stackName string) string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackResources", url.Values{"StackName": []string{stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	return string(readBody(t, resp))
}

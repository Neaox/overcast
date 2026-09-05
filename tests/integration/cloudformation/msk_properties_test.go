package cloudformation_test

// msk_properties_test.go — AWS::MSK::Cluster property threading (#540).
//
// MSK is Docker-backed: a cluster starts a Redpanda broker. The handler sent
// clusterName, kafkaVersion, numberOfBrokerNodes and brokerNodeGroupInfo, and
// dropped ClientAuthentication, EncryptionInfo, ConfigurationInfo, LoggingInfo,
// EnhancedMonitoring, OpenMonitoring, StorageMode and Tags — every one of them
// a member AWS's own ClusterInfo carries, so DescribeCluster described a
// cluster configured differently from the one the template asked for.
//
// The broker does not act on any of it. That is why the security-shaped pair
// is reported on the resource rather than echoed in silence: a cluster that
// describes itself as SASL-authenticated while listening in plaintext is worse
// than one that admits it. These tests pin both halves — the echo and the
// notice — and need no Docker daemon.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const mskClusterPropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::MSK::Cluster",
      "Properties": {
        "ClusterName": "cfn-props-msk",
        "KafkaVersion": "3.5.1",
        "NumberOfBrokerNodes": 2,
        "BrokerNodeGroupInfo": {
          "InstanceType": "kafka.m5.large",
          "ClientSubnets": ["subnet-aaa", "subnet-bbb"],
          "SecurityGroups": ["sg-aaa"]
        },
        "ClientAuthentication": {"Sasl": {"Scram": {"Enabled": true}}, "Unauthenticated": {"Enabled": false}},
        "EncryptionInfo": {"EncryptionInTransit": {"ClientBroker": "TLS", "InCluster": true}},
        "ConfigurationInfo": {"Arn": "arn:aws:kafka:us-east-1:000000000000:configuration/cfg/1", "Revision": 4},
        "LoggingInfo": {"BrokerLogs": {"CloudWatchLogs": {"Enabled": true, "LogGroup": "/msk/cfn"}}},
        "EnhancedMonitoring": "PER_TOPIC_PER_BROKER",
        "OpenMonitoring": {"Prometheus": {"JmxExporter": {"EnabledInBroker": true}}},
        "StorageMode": "TIERED",
        "Tags": {"env": "prod", "Owner": "platform"}
      }
    }
  },
  "Outputs": {
    "ClusterArn": {"Value": {"Fn::GetAtt": ["Cluster", "Arn"]}}
  }
}`

// TestCreateStack_MSKCluster_propertiesThreaded is the failing-first case for
// #540's MSK half: every property above reaches the service and comes back
// from DescribeCluster under the member name AWS binds it to.
func TestCreateStack_MSKCluster_propertiesThreaded(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "msk-cluster-properties-stack"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{mskClusterPropertiesTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	clusterARN := describeStackOutputs(t, srv, stackName)["ClusterArn"]
	if clusterARN == "" {
		t.Fatal("stack produced no cluster ARN")
	}
	info := mskDescribeCluster(t, srv, clusterARN)

	brokerInfo, _ := info["brokerNodeGroupInfo"].(map[string]any)
	if brokerInfo["instanceType"] != "kafka.m5.large" {
		t.Errorf("brokerNodeGroupInfo.instanceType = %v, want kafka.m5.large (got %v)",
			brokerInfo["instanceType"], brokerInfo)
	}

	auth, _ := info["clientAuthentication"].(map[string]any)
	sasl, _ := auth["sasl"].(map[string]any)
	scram, _ := sasl["scram"].(map[string]any)
	if enabled, _ := scram["enabled"].(bool); !enabled {
		t.Errorf("clientAuthentication.sasl.scram.enabled = %v, want true (got %v)", scram["enabled"], auth)
	}

	encryption, _ := info["encryptionInfo"].(map[string]any)
	inTransit, _ := encryption["encryptionInTransit"].(map[string]any)
	if inTransit["clientBroker"] != "TLS" {
		t.Errorf("encryptionInfo.encryptionInTransit.clientBroker = %v, want TLS (got %v)",
			inTransit["clientBroker"], encryption)
	}

	logging, _ := info["loggingInfo"].(map[string]any)
	brokerLogs, _ := logging["brokerLogs"].(map[string]any)
	cwLogs, _ := brokerLogs["cloudWatchLogs"].(map[string]any)
	if cwLogs["logGroup"] != "/msk/cfn" {
		t.Errorf("loggingInfo.brokerLogs.cloudWatchLogs.logGroup = %v, want /msk/cfn (got %v)",
			cwLogs["logGroup"], logging)
	}

	monitoring, _ := info["openMonitoring"].(map[string]any)
	prometheus, _ := monitoring["prometheus"].(map[string]any)
	jmx, _ := prometheus["jmxExporter"].(map[string]any)
	if enabled, _ := jmx["enabledInBroker"].(bool); !enabled {
		t.Errorf("openMonitoring.prometheus.jmxExporter.enabledInBroker = %v, want true (got %v)",
			jmx["enabledInBroker"], monitoring)
	}

	if info["enhancedMonitoring"] != "PER_TOPIC_PER_BROKER" {
		t.Errorf("enhancedMonitoring = %v, want PER_TOPIC_PER_BROKER", info["enhancedMonitoring"])
	}
	if info["storageMode"] != "TIERED" {
		t.Errorf("storageMode = %v, want TIERED", info["storageMode"])
	}

	// AWS binds no configurationInfo member on ClusterInfo: the configuration
	// a cluster runs is read back from currentBrokerSoftwareInfo.
	software, _ := info["currentBrokerSoftwareInfo"].(map[string]any)
	if software["configurationArn"] != "arn:aws:kafka:us-east-1:000000000000:configuration/cfg/1" {
		t.Errorf("currentBrokerSoftwareInfo.configurationArn = %v, want the template's configuration ARN (got %v)",
			software["configurationArn"], software)
	}
	if rev, _ := software["configurationRevision"].(float64); rev != 4 {
		t.Errorf("currentBrokerSoftwareInfo.configurationRevision = %v, want 4", software["configurationRevision"])
	}

	tags, _ := info["tags"].(map[string]any)
	if tags["env"] != "prod" || tags["Owner"] != "platform" {
		t.Errorf("tags = %v, want the template's two tags with their keys unchanged", tags)
	}

	// And the cluster says what it will not do: the broker behind it has no
	// authenticator, however the record describes itself.
	reasons := describeStackResourceReasons(t, srv, stackName)
	if !strings.Contains(reasons, "listens in plaintext") {
		t.Errorf("expected the cluster's ResourceStatusReason to say the broker does not enforce its auth config, got: %s",
			reasons)
	}
}

const mskClusterMinimalTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::MSK::Cluster",
      "Properties": {
        "ClusterName": "cfn-minimal-msk",
        "KafkaVersion": "3.5.1",
        "NumberOfBrokerNodes": 1,
        "BrokerNodeGroupInfo": {"InstanceType": "kafka.m5.large", "ClientSubnets": ["subnet-aaa"]}
      }
    }
  },
  "Outputs": {
    "ClusterArn": {"Value": {"Fn::GetAtt": ["Cluster", "Arn"]}}
  }
}`

// TestCreateStack_MSKCluster_minimalReportsNothingExtra is the negative case:
// a cluster that asks for none of the recorded-but-unenforced configuration
// gets no caveat on its resource and no invented members back, so the notice
// stays a signal rather than boilerplate every MSK stack carries.
func TestCreateStack_MSKCluster_minimalReportsNothingExtra(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "msk-cluster-minimal-stack"

	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{mskClusterMinimalTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	if reasons := describeStackResourceReasons(t, srv, stackName); strings.Contains(reasons, "Overcast:") {
		t.Errorf("a cluster asking for no unenforced configuration should carry no reason, got: %s", reasons)
	}

	info := mskDescribeCluster(t, srv, describeStackOutputs(t, srv, stackName)["ClusterArn"])
	for _, member := range []string{"clientAuthentication", "encryptionInfo", "loggingInfo", "openMonitoring", "storageMode"} {
		if _, present := info[member]; present {
			t.Errorf("DescribeCluster returned %s = %v for a cluster that never asked for one", member, info[member])
		}
	}
	// enhancedMonitoring is the exception: DEFAULT is CreateCluster's own
	// documented default, and AWS reports it on a cluster that named none.
	if info["enhancedMonitoring"] != "DEFAULT" {
		t.Errorf("enhancedMonitoring = %v, want the documented DEFAULT", info["enhancedMonitoring"])
	}
}

// mskDescribeCluster reads a cluster back through DescribeCluster, whose
// clusterArn is a non-greedy httpLabel and so travels as one escaped segment.
func mskDescribeCluster(t *testing.T, srv *helpers.TestServer, clusterARN string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/clusters/"+url.PathEscape(clusterARN), nil)
	if err != nil {
		t.Fatalf("build DescribeCluster request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DescribeCluster %s: %v", clusterARN, err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		ClusterInfo map[string]any `json:"clusterInfo"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("DescribeCluster %s: parse response: %v", clusterARN, err)
	}
	return out.ClusterInfo
}

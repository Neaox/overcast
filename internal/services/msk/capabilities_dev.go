//go:build dev

package msk

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Clusters
		capabilities.Capability{Service: "msk", Operation: "CreateCluster", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Docker-backed (Redpanda); async CREATING→ACTIVE via TCP health check; port auto-alloc from MSK_PORT_BASE"},
		capabilities.Capability{Service: "msk", Operation: "DescribeCluster", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Look up cluster by ARN"},
		capabilities.Capability{Service: "msk", Operation: "ListClusters", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "List all clusters; optional `clusterNameFilter` query param"},
		capabilities.Capability{Service: "msk", Operation: "DeleteCluster", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Sets state to \"DELETING\"; stops and removes Docker container asynchronously"},
		capabilities.Capability{Service: "msk", Operation: "GetBootstrapBrokers", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Returns `bootstrapBrokerString` with allocated host:port when Docker container is running"},
		capabilities.Capability{Service: "msk", Operation: "CreateClusterV2", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "PROVISIONED: same Docker/Redpanda lifecycle as v1; SERVERLESS: metadata-only, immediately ACTIVE"},
		capabilities.Capability{Service: "msk", Operation: "DescribeClusterV2", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Returns v2 shape with `clusterType` and `provisioned`/`serverless` sub-object"},
		capabilities.Capability{Service: "msk", Operation: "UpdateBrokerCount", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "UpdateBrokerStorage", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "UpdateBrokerType", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "UpdateMonitoring", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "UpdateSecurity", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "RebootBroker", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "BatchAssociateScramSecret", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "SCRAM authentication - not implemented"},
		capabilities.Capability{Service: "msk", Operation: "BatchDisassociateScramSecret", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "SCRAM authentication - not implemented"},
		capabilities.Capability{Service: "msk", Operation: "ListScramSecrets", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "SCRAM authentication - not implemented"},
		capabilities.Capability{Service: "msk", Operation: "CreateVpcConnection", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "DeleteVpcConnection", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "DescribeVpcConnection", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "msk", Operation: "ListVpcConnections", Category: "Clusters",
			Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},

		// Configurations
		capabilities.Capability{Service: "msk", Operation: "CreateConfiguration", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "Stores name, description, kafka versions"},
		capabilities.Capability{Service: "msk", Operation: "DescribeConfiguration", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "Look up configuration by ARN"},
		capabilities.Capability{Service: "msk", Operation: "ListConfigurations", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "List all configurations"},
		capabilities.Capability{Service: "msk", Operation: "DeleteConfiguration", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "Removes stored configuration"},
		capabilities.Capability{Service: "msk", Operation: "UpdateClusterConfiguration", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "Validates `currentVersion`; verifies configuration ARN exists; returns `clusterOperationArn`"},

		// Kafka versions
		capabilities.Capability{Service: "msk", Operation: "ListKafkaVersions", Category: "Kafka versions",
			Status: capabilities.StatusSupported, Notes: "Returns hardcoded list: 3.6.0, 3.5.1, 3.4.0, 2.8.1, 2.6.0"},

		// Tagging
		capabilities.Capability{Service: "msk", Operation: "TagResource", Category: "Tagging",
			Status: capabilities.StatusSupported, Notes: "ARN-scoped tag storage"},
		capabilities.Capability{Service: "msk", Operation: "ListTagsForResource", Category: "Tagging",
			Status: capabilities.StatusSupported, Notes: "Returns all tags for an ARN"},
		capabilities.Capability{Service: "msk", Operation: "UntagResource", Category: "Tagging",
			Status: capabilities.StatusSupported, Notes: "Removes specific tag keys"},
	)
}

//go:build dev

package msk

import "github.com/overcast-sh/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Clusters
		capabilities.Capability{Service: "msk", Operation: "CreateCluster", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Docker-backed (Redpanda); async CREATING→ACTIVE once the broker answers `ApiVersions`; a broker that never answers ends in `FAILED` with `stateInfo`; port auto-alloc from MSK_PORT_BASE; a cluster name already in use in the region is a `ConflictException`. `clientAuthentication`, `encryptionInfo`, `configurationInfo`, `loggingInfo`, `enhancedMonitoring`, `openMonitoring` and `storageMode` are stored and echoed by describe but not enforced — the broker listens in plaintext, and a request setting the first two is answered with an emulation-limitation header saying so"},
		capabilities.Capability{Service: "msk", Operation: "DescribeCluster", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Look up cluster by ARN"},
		capabilities.Capability{Service: "msk", Operation: "ListClusters", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "List all clusters; optional `clusterNameFilter` query param matches on the name prefix"},
		capabilities.Capability{Service: "msk", Operation: "DeleteCluster", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Sets state to \"DELETING\"; stops and removes Docker container asynchronously"},
		capabilities.Capability{Service: "msk", Operation: "GetBootstrapBrokers", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Returns `bootstrapBrokerString` with allocated host:port when Docker container is running"},
		capabilities.Capability{Service: "msk", Operation: "CreateClusterV2", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "PROVISIONED: same Docker/Redpanda lifecycle as v1, and the same stored-but-unenforced `provisioned` members as `CreateCluster`; SERVERLESS: metadata-only, immediately ACTIVE; rejects a request naming both or neither; shares one cluster-name namespace with `CreateCluster`, so a name already in use is a `ConflictException`"},
		capabilities.Capability{Service: "msk", Operation: "DescribeClusterV2", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "Returns v2 shape with `clusterType` and `provisioned`/`serverless` sub-object"},
		capabilities.Capability{Service: "msk", Operation: "ListClustersV2", Category: "Clusters",
			Status: capabilities.StatusSupported, Notes: "`clusterNameFilter` (prefix), `clusterTypeFilter`, `maxResults` and `nextToken` query params; page size capped at 100"},
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
			Status: capabilities.StatusSupported, Notes: "Stores name, description, kafka versions; a configuration name already in use in the region is a `ConflictException`"},
		capabilities.Capability{Service: "msk", Operation: "DescribeConfiguration", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "Look up configuration by ARN"},
		capabilities.Capability{Service: "msk", Operation: "ListConfigurations", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "List all configurations"},
		capabilities.Capability{Service: "msk", Operation: "DeleteConfiguration", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "Removes stored configuration"},
		capabilities.Capability{Service: "msk", Operation: "UpdateClusterConfiguration", Category: "Configurations",
			Status: capabilities.StatusSupported, Notes: "Reads the modeled `configurationInfo` object; validates `currentVersion`; a configuration ARN that does not exist is a `NotFoundException`; returns `clusterOperationArn`"},

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

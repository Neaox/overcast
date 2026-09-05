* [cloudformation/eks] `AWS::EKS::Cluster` sent its VPC config under the template's own key names, so a CDK cluster ignored `SubnetIds`.
  the k3s control plane reads `resourcesVpcConfig.subnetIds`, so the cluster stayed on the default network plane while VPC-placed resources in the same stack did not.
  `KubernetesNetworkConfig`, `AccessConfig`, `EncryptionConfig`, `Logging` and `Tags` were dropped outright and now reach the cluster on the first deploy.
* [cloudformation/eks] `AWS::EKS::Nodegroup` dropped `InstanceTypes`, `AmiType`, `DiskSize`, `CapacityType`, `Labels`, `Taints` and six more.
  `LaunchTemplate`, `UpdateConfig`, `Version`, `ReleaseVersion`, `Subnets` and `Tags` were the rest; `CreateNodegroup` accepted every one of them already.
  `UpdateConfig` now also applies on a stack update, not only at create.
* [cloudformation/msk] `AWS::MSK::Cluster` dropped `ClientAuthentication`, `EncryptionInfo`, `ConfigurationInfo`, `LoggingInfo` and four more.
  `EnhancedMonitoring`, `OpenMonitoring`, `StorageMode` and `Tags` were the rest. MSK now stores and echoes all of them, as AWS's own `ClusterInfo` does.
  the configuration ARN and revision come back under `currentBrokerSoftwareInfo`, which is where AWS binds them — `ClusterInfo` has no `configurationInfo` member.
+ [cloudformation] a resource property no handler acts on is reported on the resource instead of dropped in silence.
  it becomes the resource's `ResourceStatusReason`, so it appears in `cdk deploy` output beside the resource it was dropped from.
+ [msk] a cluster created with `clientAuthentication` or `encryptionInfo` now says the broker behind it does not enforce them.
  both are stored and returned by `DescribeCluster` as AWS does, but the Redpanda broker listens in plaintext, and a cluster that looked authenticated was worse than one that admits it.

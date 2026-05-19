import { awsClients } from "../aws-clients"
import {
  CreateClusterCommand,
  DeleteClusterCommand,
  DescribeClusterCommand,
  ListClustersCommand,
  GetBootstrapBrokersCommand,
  ListKafkaVersionsCommand,
  type ClusterInfo,
  type KafkaVersion,
  type BrokerNodeGroupInfo,
} from "@aws-sdk/client-kafka"

export type { ClusterInfo, KafkaVersion }

export const msk = {
  listClusters: async (): Promise<ClusterInfo[]> => {
    const res = await awsClients.kafka().send(new ListClustersCommand({}))
    return res.ClusterInfoList ?? []
  },

  describeCluster: async (clusterArn: string): Promise<ClusterInfo> => {
    const res = await awsClients.kafka().send(new DescribeClusterCommand({ ClusterArn: clusterArn }))
    if (!res.ClusterInfo) throw new Error(`Cluster "${clusterArn}" not found`)
    return res.ClusterInfo
  },

  createCluster: async (opts: {
    clusterName: string
    kafkaVersion: string
    numberOfBrokerNodes: number
    brokerNodeGroupInfo?: BrokerNodeGroupInfo
  }) => {
    const res = await awsClients.kafka().send(
      new CreateClusterCommand({
        ClusterName: opts.clusterName,
        KafkaVersion: opts.kafkaVersion,
        NumberOfBrokerNodes: opts.numberOfBrokerNodes,
        BrokerNodeGroupInfo: opts.brokerNodeGroupInfo ?? {
          InstanceType: "kafka.m5.large",
          ClientSubnets: [],
        },
      }),
    )
    return { clusterArn: res.ClusterArn, clusterName: res.ClusterName, state: res.State }
  },

  deleteCluster: async (clusterArn: string): Promise<void> => {
    await awsClients.kafka().send(new DeleteClusterCommand({ ClusterArn: clusterArn }))
  },

  getBootstrapBrokers: async (clusterArn: string): Promise<string> => {
    const res = await awsClients
      .kafka()
      .send(new GetBootstrapBrokersCommand({ ClusterArn: clusterArn }))
    return res.BootstrapBrokerString ?? ""
  },

  listKafkaVersions: async (): Promise<KafkaVersion[]> => {
    const res = await awsClients.kafka().send(new ListKafkaVersionsCommand({}))
    return res.KafkaVersions ?? []
  },
}

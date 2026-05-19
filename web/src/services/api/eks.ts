import {
  CreateClusterCommand,
  ListClustersCommand,
  DescribeClusterCommand,
} from "@aws-sdk/client-eks"
import type { EksCluster } from "@/types"
import { awsClients } from "../aws-clients"

export const eks = {
  createCluster: async (name: string): Promise<void> => {
    const client = awsClients.eks()
    await client.send(
      new CreateClusterCommand({
        name,
        roleArn: "arn:aws:iam::000000000000:role/eks-role",
        version: "1.31",
        resourcesVpcConfig: {
          subnetIds: ["subnet-00000000000000000"],
        },
      }),
    )
  },

  listClusters: async (): Promise<EksCluster[]> => {
    const client = awsClients.eks()
    const listRes = await client.send(new ListClustersCommand({}))
    const names = listRes.clusters ?? []
    if (names.length === 0) return []

    const clusters = await Promise.all(
      names.map(async (name) => {
        const descRes = await client.send(new DescribeClusterCommand({ name }))
        const c = descRes.cluster
        return {
          name: c?.name ?? name,
          arn: c?.arn ?? "",
          version: c?.version ?? "",
          status: c?.status ?? "UNKNOWN",
          endpoint: c?.endpoint,
          createdAt: c?.createdAt ? c.createdAt.getTime() : undefined,
        } satisfies EksCluster
      }),
    )

    return clusters.sort((a, b) => a.name.localeCompare(b.name))
  },
}

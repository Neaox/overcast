import { awsClients } from "../aws-clients"
import {
  CreateCacheClusterCommand,
  DescribeCacheClustersCommand,
  DeleteCacheClusterCommand,
  type CreateCacheClusterCommandInput,
} from "@aws-sdk/client-elasticache"

export const elasticache = {
  listClusters: async () => {
    const res = await awsClients.elasticache().send(new DescribeCacheClustersCommand({}))
    return res.CacheClusters ?? []
  },

  describeCluster: async (id: string) => {
    const res = await awsClients
      .elasticache()
      .send(new DescribeCacheClustersCommand({ CacheClusterId: id }))
    const clusters = res.CacheClusters ?? []
    if (clusters.length === 0) throw new Error(`Cache cluster "${id}" not found`)
    return clusters[0]
  },

  createCluster: async (opts: CreateCacheClusterCommandInput) => {
    const res = await awsClients.elasticache().send(new CreateCacheClusterCommand(opts))
    return res.CacheCluster!
  },

  deleteCluster: async (id: string) => {
    await awsClients
      .elasticache()
      .send(new DeleteCacheClusterCommand({ CacheClusterId: id }))
  },
}

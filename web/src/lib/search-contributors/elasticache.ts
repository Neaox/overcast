import { elasticache } from "@/services/api"
import { createSearchContributor } from "./create-contributor"
import type { CacheCluster } from "@aws-sdk/client-elasticache"

createSearchContributor<CacheCluster>({
  id: "elasticache",
  cacheKey: (ep) => [ep.baseUrl, ep.region, "elasticache", "clusters"] as const,
  fetchAll: () => elasticache.listClusters(),
  matchFields: (c) => [c.CacheClusterId, c.Engine, c.CacheNodeType],
  toResult: (c) => ({
    id: `elasticache:${c.CacheClusterId}`,
    label: c.CacheClusterId ?? "",
    sublabel: `${c.Engine} · ${c.CacheNodeType}`,
    service: "ElastiCache",
    serviceKey: "/elasticache",
    type: "Cache Cluster",
    href: `/elasticache`,
  }),
})

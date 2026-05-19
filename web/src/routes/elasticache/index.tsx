import { createFileRoute } from "@tanstack/react-router"
import { ClusterList } from "@/features/elasticache/components/cluster-list"

export const Route = createFileRoute("/elasticache/")({
  head: () => ({ meta: [{ title: "ElastiCache Clusters — Overcast" }] }),
  component: ClusterList,
})

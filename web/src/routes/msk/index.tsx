import { createFileRoute } from "@tanstack/react-router"
import { ClusterList } from "@/features/msk/components/cluster-list"

export const Route = createFileRoute("/msk/")({
  head: () => ({ meta: [{ title: "MSK Clusters — Overcast" }] }),
  component: ClusterList,
})

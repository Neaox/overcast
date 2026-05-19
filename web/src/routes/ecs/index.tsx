import { createFileRoute } from "@tanstack/react-router"
import { ClusterList } from "@/features/ecs/components/cluster-list"

export const Route = createFileRoute("/ecs/")({
  head: () => ({ meta: [{ title: "ECS Clusters — Overcast" }] }),
  component: ClusterList,
})

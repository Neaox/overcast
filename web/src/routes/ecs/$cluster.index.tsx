import { createFileRoute } from "@tanstack/react-router"
import { ClusterDetail } from "@/features/ecs/components/cluster-detail"

export const Route = createFileRoute("/ecs/$cluster/")({
  component: ClusterDetailRoute,
})

function ClusterDetailRoute() {
  const { cluster } = Route.useParams()
  const { tab, service } = Route.useSearch()
  return <ClusterDetail clusterName={cluster} initialTab={tab} initialService={service} />
}

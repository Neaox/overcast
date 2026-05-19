/**
 * Layout route for /ecs/$cluster.
 * Validates that the cluster exists before rendering the detail page.
 */
import { useEffect } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ecsClusterDetailQueryOptions } from "@/features/ecs/data"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"
import { ClusterDetail } from "@/features/ecs/components/cluster-detail"

export const Route = createFileRoute("/ecs/$cluster")({
  head: ({ params }) => ({
    meta: [{ title: `${params.cluster} — ECS — Overcast` }],
  }),
  component: ClusterLayout,
})

function ClusterLayout() {
  const { cluster } = Route.useParams()
  const navigate = useNavigate()
  const { toast } = useToast()

  const { isLoading, isError, error } = useQuery({
    ...ecsClusterDetailQueryOptions(cluster),
    retry: false,
  })

  useEffect(() => {
    if (!isError) return
    void navigate({ to: "/ecs" })
    toast({
      title: `Cluster "${cluster}" not found`,
      description: error.message,
      variant: "danger",
    })
  }, [isError, navigate, toast, cluster, error])

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (isError) return null

  return <ClusterDetail clusterName={cluster} />
}

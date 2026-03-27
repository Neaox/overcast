/**
 * Layout route for /sqs/$queue and all child routes.
 *
 * Checks that the queue exists before rendering. If it doesn't, redirects
 * to the queue list and shows a toast.
 */
import { useEffect } from "react"
import { Outlet, useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { api } from "@/services/api"
import { useEndpoint } from "@/hooks/use-endpoint"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"

export const Route = createFileRoute("/sqs/$queue")({
  component: QueueLayout,
})

function QueueLayout() {
  const { queue } = Route.useParams()
  const { endpoint } = useEndpoint()
  const navigate = useNavigate()
  const { toast } = useToast()

  const { isLoading, isError, error } = useQuery({
    queryKey: ["sqs", "queue-exists", endpoint.baseUrl, queue],
    queryFn: () => api.sqs.getQueue(queue),
    retry: false,
    staleTime: 30_000,
  })

  useEffect(() => {
    if (!isError) return
    navigate({ to: "/sqs" })
    toast({
      title: `Queue "${queue}" not found`,
      description: (error as Error)?.message,
      variant: "danger",
    })
  }, [isError]) // eslint-disable-line react-hooks/exhaustive-deps

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (isError) return null

  return <Outlet />
}

/**
 * Layout route for /cloudfront/$distributionId and all child routes.
 *
 * Checks that the distribution exists before rendering. If it doesn't,
 * redirects to the distribution list and shows a toast.
 */
import { useEffect } from "react"
import { Outlet, useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { cloudfrontDistributionQueryOptions } from "@/features/cloudfront/data"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"

export const Route = createFileRoute("/cloudfront/$distributionId")({
  head: ({ params }) => ({
    meta: [{ title: `${params.distributionId} — CloudFront — Overcast` }],
  }),
  component: DistributionLayout,
})

function DistributionLayout() {
  const { distributionId } = Route.useParams()
  const navigate = useNavigate()
  const { toast } = useToast()

  const { isLoading, isError, error } = useQuery({
    ...cloudfrontDistributionQueryOptions(distributionId),
    retry: false,
    staleTime: 30_000,
  })

  useEffect(() => {
    if (!isError) return
    void navigate({ to: "/cloudfront" })
    toast({
      title: `Distribution "${distributionId}" not found`,
      description: error.message,
      variant: "danger",
    })
  }, [isError, navigate, toast, distributionId, error])

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

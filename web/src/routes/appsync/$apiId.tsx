/**
 * Layout route for /appsync/$apiId and all child routes.
 *
 * Checks that the API exists before rendering. If it doesn't, redirects
 * to the API list and shows a toast.
 */
import { useEffect } from "react"
import { Outlet, useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { appsyncApiQueryOptions } from "@/features/appsync/data"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"

export const Route = createFileRoute("/appsync/$apiId")({
  head: ({ params }) => ({ meta: [{ title: `${params.apiId} — AppSync — Overcast` }] }),
  component: ApiLayout,
})

function ApiLayout() {
  const { apiId } = Route.useParams()
  const navigate = useNavigate()
  const { toast } = useToast()

  const { isLoading, isError, error } = useQuery({
    ...appsyncApiQueryOptions(apiId),
    retry: false,
    staleTime: 30_000,
  })

  useEffect(() => {
    if (isError) {
      toast({
        title: "API not found",
        description: `GraphQL API "${apiId}" does not exist.`,
        variant: "danger",
      })
      void navigate({ to: "/appsync" })
    }
  }, [isError, error, apiId, toast, navigate])

  if (isLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (isError) return null

  return <Outlet />
}

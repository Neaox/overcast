/**
 * Layout route for /s3/$bucket and all child routes.
 *
 * Checks that the bucket exists before rendering any child view. If it doesn't,
 * redirects to the bucket list and shows a toast. This means individual child
 * routes (BucketDetail, PutObject, …) never need to handle the bucket-missing
 * case themselves.
 */
import { useEffect } from "react"
import { Outlet, useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { s3BucketExistsQueryOptions } from "@/features/s3/data"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"

export const Route = createFileRoute("/s3/$bucket")({
  head: ({ params }) => ({ meta: [{ title: `${params.bucket} — S3 — Overcast` }] }),
  component: BucketLayout,
})

function BucketLayout() {
  const { bucket } = Route.useParams()
  const navigate = useNavigate()
  const { toast } = useToast()

  const { isLoading, isError, error } = useQuery(s3BucketExistsQueryOptions(bucket))

  useEffect(() => {
    if (!isError) return
    void navigate({ to: "/s3" })
    toast({
      title: `Bucket "${bucket}" not found`,
      description: error.message,
      variant: "danger",
    })
  }, [isError]) // eslint-disable-line react-hooks/exhaustive-deps -- navigate/toast/bucket/error are stable or only needed on error transition

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

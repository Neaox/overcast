/**
 * Layout route for /ec2/$instanceId.
 * Validates that the EC2 instance exists before rendering the detail page.
 */
import { useEffect } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ec2InstanceDetailQueryOptions } from "@/features/ec2/data"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"
import { InstanceDetail } from "@/features/ec2/components/instance-detail"

export const Route = createFileRoute("/ec2/$instanceId")({
  head: ({ params }) => ({
    meta: [{ title: `${params.instanceId} — EC2 — Overcast` }],
  }),
  component: InstanceLayout,
})

function InstanceLayout() {
  const { instanceId } = Route.useParams()
  const navigate = useNavigate()
  const { toast } = useToast()

  const { isLoading, isError, error } = useQuery({
    ...ec2InstanceDetailQueryOptions(instanceId),
    retry: false,
  })

  useEffect(() => {
    if (!isError) return
    void navigate({ to: "/ec2" })
    toast({
      title: `Instance "${instanceId}" not found`,
      description: error.message,
      variant: "danger",
    })
  }, [isError, navigate, toast, instanceId, error])

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (isError) return null

  return <InstanceDetail instanceId={instanceId} />
}

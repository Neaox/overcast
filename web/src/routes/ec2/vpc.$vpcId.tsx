/**
 * Layout route for /ec2/vpc/$vpcId.
 * Validates that the VPC exists before rendering the detail page.
 */

import { useEffect } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ec2VpcDetailQueryOptions } from "@/features/ec2/data"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"
import { VpcDetail } from "@/features/ec2/components/vpc-detail"

export const Route = createFileRoute("/ec2/vpc/$vpcId")({
  head: ({ params }) => ({
    meta: [{ title: `${params.vpcId} — VPC — Overcast` }],
  }),
  component: VpcLayout,
})

function VpcLayout() {
  const { vpcId } = Route.useParams()
  const navigate = useNavigate()
  const { toast } = useToast()

  const { isLoading, isError, error } = useQuery({
    ...ec2VpcDetailQueryOptions(vpcId),
    retry: false,
  })

  useEffect(() => {
    if (!isError) return
    void navigate({ to: "/ec2" })
    toast({
      title: `VPC "${vpcId}" not found`,
      description: error.message,
      variant: "danger",
    })
  }, [isError, navigate, toast, vpcId, error])

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (isError) return null

  return <VpcDetail vpcId={vpcId} />
}

/**
 * Layout route for /rds/$instance.
 * Validates that the DB instance exists before rendering the detail page.
 */
import { useEffect } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { rdsInstanceDetailQueryOptions } from "@/features/rds/data"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"
import { InstanceDetail } from "@/features/rds/components/instance-detail"

export const Route = createFileRoute("/rds/$instance")({
  head: ({ params }) => ({
    meta: [{ title: `${params.instance} — RDS — Overcast` }],
  }),
  component: InstanceLayout,
})

function InstanceLayout() {
  const { instance } = Route.useParams()
  const navigate = useNavigate()
  const { toast } = useToast()

  const { isLoading, isError, error } = useQuery({
    ...rdsInstanceDetailQueryOptions(instance),
    retry: false,
  })

  useEffect(() => {
    if (!isError) return
    void navigate({ to: "/rds" })
    toast({
      title: `DB instance "${instance}" not found`,
      description: error.message,
      variant: "danger",
    })
  }, [isError]) // eslint-disable-line react-hooks/exhaustive-deps -- navigate/toast/instance/error are stable or only needed on error transition

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (isError) return null

  return <InstanceDetail instanceId={instance} />
}

import { createFileRoute } from "@tanstack/react-router"
import { SsmParameterDetail } from "@/features/ssm/components/ssm-parameter-detail"

export const Route = createFileRoute("/ssm/$name")({
  head: ({ params }) => ({
    meta: [{ title: `${params.name} — SSM — Overcast` }],
  }),
  component: function SsmParameterDetailRoute() {
    const { name } = Route.useParams()
    return <SsmParameterDetail name={name} />
  },
})

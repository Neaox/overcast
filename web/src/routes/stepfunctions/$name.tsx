import { createFileRoute } from "@tanstack/react-router"
import { StateMachineDetail } from "@/features/stepfunctions/components/state-machine-detail"

export const Route = createFileRoute("/stepfunctions/$name")({
  head: ({ params }) => ({
    meta: [{ title: `${params.name} — Step Functions — Overcast` }],
  }),
  component: function StateMachineDetailRoute() {
    const { name } = Route.useParams()
    return <StateMachineDetail name={name} />
  },
})

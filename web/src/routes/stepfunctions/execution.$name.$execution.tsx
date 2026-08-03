import { createFileRoute } from "@tanstack/react-router"
import { ExecutionDetail } from "@/features/stepfunctions/components/execution-detail"

export const Route = createFileRoute("/stepfunctions/execution/$name/$execution")({
  head: ({ params }) => ({
    meta: [{ title: `${params.execution} — ${params.name} — Step Functions — Overcast` }],
  }),
  component: function ExecutionDetailRoute() {
    const { name, execution } = Route.useParams()
    return <ExecutionDetail name={name} execution={execution} />
  },
})

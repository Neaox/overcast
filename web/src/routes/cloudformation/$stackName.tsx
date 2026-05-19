import { createFileRoute } from "@tanstack/react-router"
import { StackDetail } from "@/features/cloudformation/components/stack-detail"

export const Route = createFileRoute("/cloudformation/$stackName")({
  head: ({ params }) => ({
    meta: [{ title: `${params.stackName} — CloudFormation — Overcast` }],
  }),
  component: function StackDetailRoute() {
    const { stackName } = Route.useParams()
    return <StackDetail stackName={stackName} />
  },
})

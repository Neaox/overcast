import { createFileRoute } from "@tanstack/react-router"
import { StackDetail } from "@/features/cloudformation/components/stack-detail"

type StackDetailSearch = {
  // The stack ID (ARN) captured at the link's origin, when the caller already
  // has one. A deleted stack resolves only by ARN on AWS (see #829) — the
  // stack's name becomes reusable the moment it reaches DELETE_COMPLETE — so
  // this is what makes a deleted stack's detail view reachable at all.
  stackId?: string
}

export const Route = createFileRoute("/cloudformation/$stackName")({
  head: ({ params }) => ({
    meta: [{ title: `${params.stackName} — CloudFormation — Overcast` }],
  }),
  validateSearch: (search: Record<string, unknown>): StackDetailSearch => ({
    stackId: typeof search.stackId === "string" ? search.stackId : undefined,
  }),
  component: function StackDetailRoute() {
    const { stackName } = Route.useParams()
    const { stackId } = Route.useSearch()
    return <StackDetail stackName={stackName} initialStackId={stackId} />
  },
})

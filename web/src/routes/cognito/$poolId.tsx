import { createFileRoute } from "@tanstack/react-router"
import { CognitoPoolDetail } from "@/features/cognito/components/cognito-pool-detail"

export const Route = createFileRoute("/cognito/$poolId")({
  head: ({ params }) => ({
    meta: [{ title: `${params.poolId} — Cognito — Overcast` }],
  }),
  component: function CognitoPoolDetailRoute() {
    const { poolId } = Route.useParams()
    return <CognitoPoolDetail poolId={poolId} />
  },
})

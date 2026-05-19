import { createFileRoute } from "@tanstack/react-router"
import { HttpApiDetail } from "@/features/apigateway/components/http-api-detail"

export const Route = createFileRoute("/apigateway/http/$apiId")({
  head: ({ params }) => ({
    meta: [{ title: `HTTP API ${params.apiId} — API Gateway — Overcast` }],
  }),
  component: function HttpApiDetailRoute() {
    const { apiId } = Route.useParams()
    return <HttpApiDetail apiId={apiId} />
  },
})

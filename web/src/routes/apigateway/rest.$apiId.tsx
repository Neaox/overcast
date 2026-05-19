import { createFileRoute } from "@tanstack/react-router"
import { RestApiDetail } from "@/features/apigateway/components/rest-api-detail"

export const Route = createFileRoute("/apigateway/rest/$apiId")({
  head: ({ params }) => ({
    meta: [{ title: `REST API ${params.apiId} — API Gateway — Overcast` }],
  }),
  component: function RestApiDetailRoute() {
    const { apiId } = Route.useParams()
    return <RestApiDetail apiId={apiId} />
  },
})

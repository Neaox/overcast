import { createFileRoute } from "@tanstack/react-router"
import { ApiDetail } from "@/features/appsync/components/api-detail"

export const Route = createFileRoute("/appsync/$apiId/")({
  component: function ApiDetailPage() {
    const { apiId } = Route.useParams()
    return <ApiDetail apiId={apiId} />
  },
})

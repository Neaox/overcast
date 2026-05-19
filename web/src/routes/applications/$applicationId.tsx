import { createFileRoute } from "@tanstack/react-router"
import { ApplicationDetail } from "@/features/applications/components/application-detail"

export const Route = createFileRoute("/applications/$applicationId")({
  head: ({ params }) => ({
    meta: [{ title: `${params.applicationId} — Applications — Overcast` }],
  }),
  component: function ApplicationDetailRoute() {
    const { applicationId } = Route.useParams()
    return <ApplicationDetail applicationId={applicationId} />
  },
})

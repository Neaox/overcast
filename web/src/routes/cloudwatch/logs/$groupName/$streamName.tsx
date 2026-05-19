import { createFileRoute } from "@tanstack/react-router"
import { LogEventsViewer } from "@/features/cloudwatch/logs/components/log-events-viewer"

export const Route = createFileRoute("/cloudwatch/logs/$groupName/$streamName")({
  head: ({ params }) => ({
    meta: [
      {
        title: `${params.streamName} — ${params.groupName} — CloudWatch — Overcast`,
      },
    ],
  }),
  component: function LogEventsPage() {
    const { groupName, streamName } = Route.useParams()
    return <LogEventsViewer groupName={groupName} streamName={streamName} />
  },
})

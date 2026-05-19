import { createFileRoute } from "@tanstack/react-router"
import { LogEventsViewer } from "@/features/cloudwatch/logs/components/log-events-viewer"

type StreamSearch = {
  groupName: string | undefined
  streamName: string | undefined
}

export const Route = createFileRoute("/cloudwatch/logs/stream")({
  validateSearch: (search: Record<string, unknown>): StreamSearch => ({
    groupName: typeof search.groupName === "string" ? search.groupName : undefined,
    streamName: typeof search.streamName === "string" ? search.streamName : undefined,
  }),
  component: function LogEventsPage() {
    const { groupName, streamName } = Route.useSearch()
    if (!groupName || !streamName) return null
    return <LogEventsViewer groupName={groupName} streamName={streamName} />
  },
})

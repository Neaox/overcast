import { createFileRoute } from "@tanstack/react-router"
import { LogEventsViewer } from "@/features/cloudwatch/logs/components/log-events-viewer"

type EventsSearch = {
  groupName: string | undefined
}

export const Route = createFileRoute("/cloudwatch/logs/events")({
  validateSearch: (search: Record<string, unknown>): EventsSearch => ({
    groupName: typeof search.groupName === "string" ? search.groupName : undefined,
  }),
  head: () => ({
    meta: [{ title: "CloudWatch Logs — Overcast" }],
  }),
  component: function LogGroupEventsPage() {
    const { groupName } = Route.useSearch()
    if (!groupName) return null
    return <LogEventsViewer groupName={groupName} />
  },
})

import { createFileRoute } from "@tanstack/react-router"
import { LogGroupDetail } from "@/features/cloudwatch/logs/components/log-group-detail"

type GroupSearch = {
  groupName: string | undefined
}

export const Route = createFileRoute("/cloudwatch/logs/group")({
  validateSearch: (search: Record<string, unknown>): GroupSearch => ({
    groupName: typeof search.groupName === "string" ? search.groupName : undefined,
  }),
  component: function LogGroupDetailPage() {
    const { groupName } = Route.useSearch()
    if (!groupName) return null
    return <LogGroupDetail groupName={groupName} />
  },
})

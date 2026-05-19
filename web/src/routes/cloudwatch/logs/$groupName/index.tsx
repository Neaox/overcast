import { createFileRoute } from "@tanstack/react-router"
import { LogGroupDetail } from "@/features/cloudwatch/logs/components/log-group-detail"

export const Route = createFileRoute("/cloudwatch/logs/$groupName/")({
  head: ({ params }) => ({
    meta: [{ title: `${params.groupName} — CloudWatch Logs — Overcast` }],
  }),
  component: function LogGroupDetailPage() {
    const { groupName } = Route.useParams()
    return <LogGroupDetail groupName={groupName} />
  },
})

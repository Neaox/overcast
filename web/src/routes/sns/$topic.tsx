import { createFileRoute } from "@tanstack/react-router"
import { TopicDetail } from "@/features/sns/components/topic-detail"

export const Route = createFileRoute("/sns/$topic")({
  head: ({ params }) => ({
    meta: [{ title: `${params.topic} — SNS — Overcast` }],
  }),
  component: function TopicDetailRoute() {
    const { topic } = Route.useParams()
    return <TopicDetail topicName={topic} />
  },
})

import { createFileRoute } from "@tanstack/react-router"
import { QueueDetail } from "@/features/sqs/components/queue-detail"

export const Route = createFileRoute("/sqs/$queue/")({
  component: function QueueDetailPage() {
    const { queue } = Route.useParams()
    return <QueueDetail queueName={queue} />
  },
})

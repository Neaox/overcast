import { createFileRoute } from "@tanstack/react-router"
import { QueueList } from "@/features/sqs/components/queue-list"

export const Route = createFileRoute("/sqs/")({
  component: QueueList,
})

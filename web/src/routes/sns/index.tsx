import { createFileRoute } from "@tanstack/react-router"
import { TopicList } from "@/features/sns/components/topic-list"

export const Route = createFileRoute("/sns/")({
  head: () => ({ meta: [{ title: "SNS Topics — Overcast" }] }),
  component: TopicList,
})

import { createFileRoute } from "@tanstack/react-router"
import { StreamList } from "@/features/kinesis/components/stream-list"

export const Route = createFileRoute("/kinesis/")({
  head: () => ({ meta: [{ title: "Kinesis Data Streams — Overcast" }] }),
  component: StreamList,
})

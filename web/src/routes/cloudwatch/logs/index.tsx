import { createFileRoute } from "@tanstack/react-router"
import { LogGroupList } from "@/features/cloudwatch/logs/components/log-group-list"

export const Route = createFileRoute("/cloudwatch/logs/")({
  head: () => ({ meta: [{ title: "CloudWatch Logs — Overcast" }] }),
  component: LogGroupList,
})

import { createFileRoute } from "@tanstack/react-router"
import { CloudwatchDashboard } from "@/features/cloudwatch/components/cloudwatch-dashboard"

export const Route = createFileRoute("/cloudwatch/")({
  head: () => ({ meta: [{ title: "CloudWatch — Overcast" }] }),
  component: CloudwatchDashboard,
})

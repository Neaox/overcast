import { createFileRoute } from "@tanstack/react-router"
import { MetricsPage } from "@/features/metrics/metrics-page"

export const Route = createFileRoute("/metrics")({
  head: () => ({ meta: [{ title: "Metrics — Overcast" }] }),
  component: MetricsPage,
})

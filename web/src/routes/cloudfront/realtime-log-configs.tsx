import { createFileRoute } from "@tanstack/react-router"
import { RealtimeLogConfigList } from "@/features/cloudfront/components/realtime-log-config-list"

export const Route = createFileRoute("/cloudfront/realtime-log-configs")({
  head: () => ({ meta: [{ title: "Realtime Log Configs — CloudFront — Overcast" }] }),
  component: RealtimeLogConfigList,
})

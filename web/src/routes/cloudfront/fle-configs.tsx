import { createFileRoute } from "@tanstack/react-router"
import { FLEConfigList } from "@/features/cloudfront/components/fle-config-list"

export const Route = createFileRoute("/cloudfront/fle-configs")({
  head: () => ({ meta: [{ title: "Field-Level Encryption Configs — CloudFront — Overcast" }] }),
  component: FLEConfigList,
})

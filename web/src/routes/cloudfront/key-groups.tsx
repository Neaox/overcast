import { createFileRoute } from "@tanstack/react-router"
import { KeyGroupList } from "@/features/cloudfront/components/key-group-list"

export const Route = createFileRoute("/cloudfront/key-groups")({
  head: () => ({ meta: [{ title: "Key Groups — CloudFront — Overcast" }] }),
  component: KeyGroupList,
})

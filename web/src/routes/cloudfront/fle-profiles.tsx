import { createFileRoute } from "@tanstack/react-router"
import { FLEProfileList } from "@/features/cloudfront/components/fle-profile-list"

export const Route = createFileRoute("/cloudfront/fle-profiles")({
  head: () => ({ meta: [{ title: "Field-Level Encryption Profiles — CloudFront — Overcast" }] }),
  component: FLEProfileList,
})

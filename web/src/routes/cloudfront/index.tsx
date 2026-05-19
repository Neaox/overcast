import { createFileRoute } from "@tanstack/react-router"
import { DistributionList } from "@/features/cloudfront/components/distribution-list"

export const Route = createFileRoute("/cloudfront/")({
  head: () => ({ meta: [{ title: "CloudFront Distributions — Overcast" }] }),
  component: DistributionList,
})

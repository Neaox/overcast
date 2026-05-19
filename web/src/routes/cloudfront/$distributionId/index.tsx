import { createFileRoute } from "@tanstack/react-router"
import { DistributionDetail } from "@/features/cloudfront/components/distribution-detail"

export const Route = createFileRoute("/cloudfront/$distributionId/")({
  component: function DistributionDetailPage() {
    return <DistributionDetail />
  },
})

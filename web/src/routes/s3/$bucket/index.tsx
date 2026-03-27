import { createFileRoute } from "@tanstack/react-router"
import { BucketDetail } from "@/features/s3/components/bucket-detail"

export const Route = createFileRoute("/s3/$bucket/")({
  component: BucketDetail,
})

import { createFileRoute } from "@tanstack/react-router"
import { BucketConfig } from "@/features/s3/components/bucket-config"

export const Route = createFileRoute("/s3/$bucket/config")({
  head: ({ params }) => ({ meta: [{ title: `${params.bucket} Config — S3 — Overcast` }] }),
  component: BucketConfig,
})

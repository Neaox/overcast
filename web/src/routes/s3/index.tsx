import { createFileRoute } from "@tanstack/react-router"
import { BucketList } from "@/features/s3/components/bucket-list"

export const Route = createFileRoute("/s3/")({
  head: () => ({ meta: [{ title: "S3 Buckets — Overcast" }] }),
  component: BucketList,
})

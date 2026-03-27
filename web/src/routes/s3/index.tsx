import { createFileRoute } from "@tanstack/react-router"
import { BucketList } from "@/features/s3/components/bucket-list"

export const Route = createFileRoute("/s3/")({
  component: BucketList,
})

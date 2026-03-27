import { createFileRoute } from "@tanstack/react-router"
import { PutObject } from "@/features/s3/components/put-object"

export const Route = createFileRoute("/s3/$bucket/upload")({
  component: PutObject,
})

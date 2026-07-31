import { createFileRoute } from "@tanstack/react-router"
import { FileSystemList } from "@/features/efs/components/file-system-list"

export const Route = createFileRoute("/efs/")({
  head: () => ({ meta: [{ title: "EFS File Systems — Overcast" }] }),
  component: FileSystemList,
})

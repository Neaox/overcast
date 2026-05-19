import { createFileRoute } from "@tanstack/react-router"
import { InstanceList } from "@/features/rds/components/instance-list"

export const Route = createFileRoute("/rds/")({
  head: () => ({ meta: [{ title: "RDS Instances — Overcast" }] }),
  component: InstanceList,
})

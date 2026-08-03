import { createFileRoute } from "@tanstack/react-router"
import { AutoScalingGroupList } from "@/features/autoscaling/components/group-list"

export const Route = createFileRoute("/autoscaling/")({
  head: () => ({ meta: [{ title: "Auto Scaling Groups — Overcast" }] }),
  component: AutoScalingGroupList,
})
